package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// newPlainDuckDB opens an in-memory DuckDB without optional extensions; CSV
// loading needs none of them.
func newPlainDuckDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("duckdb", ":memory:")
	if err != nil {
		t.Fatalf("open duckdb: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeCSV(t *testing.T, name string, rows ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(strings.Join(rows, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write csv: %v", err)
	}
	return path
}

type csvFileService struct {
	interfaces.FileService
	files map[string]string // file path -> CSV content
}

func (s *csvFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	content, ok := s.files[filePath]
	if !ok {
		return nil, fmt.Errorf("no such file %q", filePath)
	}
	return io.NopCloser(strings.NewReader(content)), nil
}

func TestValidateDataAnalysisSQLAcceptsLogicalAndPhysicalTables(t *testing.T) {
	schema := &TableSchema{TableName: "k_doc_a"}
	for _, query := range []string{
		`SELECT COUNT(*) FROM dataset`,
		`SELECT * FROM "dataset" WHERE "amount" > 1`,
		`SELECT * FROM "k_doc_a" LIMIT 10`,
	} {
		if err := validateDataAnalysisSQL(query, schema); err != nil {
			t.Errorf("%s: unexpected error %v", query, err)
		}
	}
}

func TestValidateDataAnalysisSQLRejectsOtherTablesWithGuidance(t *testing.T) {
	schema := &TableSchema{TableName: "k_doc_a"}
	for _, query := range []string{
		`SELECT * FROM d1`,
		`SELECT * FROM "d1"`,
		`SELECT * FROM k_doc_b`,
		`SELECT * FROM "3f1c2a8e-0000-4000-8000-000000000001"`,
	} {
		err := validateDataAnalysisSQL(query, schema)
		if err == nil {
			t.Errorf("%s: must be rejected", query)
			continue
		}
		if !strings.Contains(err.Error(), `"dataset"`) {
			t.Errorf("%s: error must tell the model to use the dataset table, got %v", query, err)
		}
	}
	if err := validateDataAnalysisSQL(`DROP TABLE dataset`, schema); err == nil {
		t.Error("non-SELECT statements must be rejected")
	}
	if err := validateDataAnalysisSQL(`SELECT * FROM 'd1'`, schema); err == nil {
		t.Error("a quoted literal in FROM must be rejected")
	}
}

func TestExecuteSingleQueryExposesSelectedDocumentAsDataset(t *testing.T) {
	db := newPlainDuckDB(t)
	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: db, sessionID: "test-dataset"}
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	if _, err := tool.LoadFromCSV(ctx, writeCSV(t, "a.csv", "id,amount", "1,10", "2,20"), "k_doc_a"); err != nil {
		t.Fatalf("load a: %v", err)
	}
	if _, err := tool.LoadFromCSV(ctx, writeCSV(t, "b.csv", "id,amount", "1,5", "2,6", "3,7"), "k_doc_b"); err != nil {
		t.Fatalf("load b: %v", err)
	}

	rowsA, err := tool.executeSingleQuery(ctx, `SELECT COUNT(*) AS n FROM dataset`, "k_doc_a")
	if err != nil || len(rowsA) != 1 || rowsA[0]["n"] != "2" {
		t.Fatalf("doc a via dataset: rows=%v err=%v", rowsA, err)
	}
	rowsB, err := tool.executeSingleQuery(ctx, `SELECT COUNT(*) AS n FROM "dataset"`, "k_doc_b")
	if err != nil || len(rowsB) != 1 || rowsB[0]["n"] != "3" {
		t.Fatalf("doc b via dataset: rows=%v err=%v", rowsB, err)
	}
	// The physical name keeps working for internal callers.
	rowsPhys, err := tool.executeSingleQuery(ctx, `SELECT COUNT(*) AS n FROM "k_doc_a"`, "k_doc_a")
	if err != nil || len(rowsPhys) != 1 || rowsPhys[0]["n"] != "2" {
		t.Fatalf("doc a via physical table: rows=%v err=%v", rowsPhys, err)
	}
	// The view never outlives the query: nothing named dataset is visible on
	// the shared pool afterwards.
	if _, err := db.ExecContext(ctx, `SELECT COUNT(*) FROM dataset`); err == nil {
		t.Fatal("temporary dataset view leaked past the query")
	}
}

func TestExecuteSingleQueryIsolatesConcurrentDocuments(t *testing.T) {
	db := newPlainDuckDB(t)
	tool := &DataAnalysisTool{BaseTool: dataAnalysisTool, db: db, sessionID: "test-concurrent"}
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	const docs = 4
	for i := 0; i < docs; i++ {
		rows := []string{"id"}
		for j := 0; j <= i; j++ {
			rows = append(rows, fmt.Sprint(j))
		}
		name := fmt.Sprintf("k_doc_%d", i)
		if _, err := tool.LoadFromCSV(ctx, writeCSV(t, name+".csv", rows...), name); err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, docs*20)
	for round := 0; round < 20; round++ {
		for i := 0; i < docs; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rows, err := tool.executeSingleQuery(
					ctx, `SELECT COUNT(*) AS n FROM dataset`, fmt.Sprintf("k_doc_%d", i),
				)
				if err != nil {
					errs <- err
					return
				}
				if want := fmt.Sprint(i + 1); len(rows) != 1 || rows[0]["n"] != want {
					errs <- fmt.Errorf("doc %d saw rows=%v, want n=%s", i, rows, want)
				}
			}(i)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestDataAnalysisExecuteEndToEndUsesDatasetTable(t *testing.T) {
	db := newPlainDuckDB(t)
	knowledge := &types.Knowledge{ID: "3f1c2a8e-0000-4000-8000-000000000001", FileType: "csv", FilePath: "doc-a.csv"}
	tool := NewDataAnalysisTool(
		nil,
		&mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}},
		nil,
		&csvFileService{files: map[string]string{"doc-a.csv": "id,amount\n1,10\n2,20\n3,30\n"}},
		db,
		"test-e2e",
	)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	args, _ := json.Marshal(DataAnalysisInput{
		KnowledgeID: knowledge.ID,
		SQL:         `SELECT COUNT(*) AS total FROM dataset WHERE "amount" <> '20'`,
	})
	result, err := tool.Execute(ctx, args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("execute failed: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Output, `"total":"2"`) {
		t.Fatalf("unexpected output:\n%s", result.Output)
	}
	// Neither the physical table name nor the knowledge ID is echoed back to
	// the model through the query text.
	if strings.Contains(result.Output, "k_3f1c2a8e") || strings.Contains(result.Output, knowledge.ID) {
		t.Fatalf("output leaks the physical table or knowledge ID:\n%s", result.Output)
	}

	// The legacy shape (document ID substituted into SQL) now fails with
	// guidance instead of being blindly rewritten.
	args, _ = json.Marshal(DataAnalysisInput{
		KnowledgeID: knowledge.ID,
		SQL:         fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, knowledge.ID),
	})
	result, err = tool.Execute(ctx, args)
	if err == nil || result == nil || result.Success {
		t.Fatalf("knowledge ID inside SQL must be rejected: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Error, `"dataset"`) {
		t.Fatalf("rejection must name the dataset table: %s", result.Error)
	}
}
