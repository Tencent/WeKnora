package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestDataAnalysisOverlappingRunsOfSameDocument(t *testing.T) {
	for _, format := range []string{"csv", "xlsx"} {
		for _, sessionIDs := range [][2]string{{"session-a", "session-b"}, {"same-session", "same-session"}} {
			t.Run(format+"/"+strings.Join(sessionIDs[:], "/"), func(t *testing.T) {
				ctx := context.Background()
				db := newPlainDuckDB(t)
				content := "id,amount\n1,10\n2,20\n"
				if format == "xlsx" {
					db = newTestDuckDB(t)
					path := filepath.Join(t.TempDir(), "shared.xlsx")
					writeWorkbook(t, path, map[string][][]any{
						"Sales": {{"id", "amount"}, {1, 10}, {2, 20}},
					}, []string{"Sales"})
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					content = string(data)
				}
				knowledge := &types.Knowledge{ID: "shared-doc", FileType: format, FilePath: "shared." + format}
				knowledgeService := &mapKnowledgeService{docs: map[string]*types.Knowledge{knowledge.ID: knowledge}}
				files := &csvFileService{files: map[string]string{knowledge.FilePath: content}}
				first := NewDataAnalysisTool(nil, knowledgeService, nil, files, db, sessionIDs[0])
				second := NewDataAnalysisTool(nil, knowledgeService, nil, files, db, sessionIDs[1])
				t.Cleanup(func() {
					first.Cleanup(ctx)
					second.Cleanup(ctx)
				})
				args, err := json.Marshal(DataAnalysisInput{
					KnowledgeID: knowledge.ID,
					SQL:         "SELECT COUNT(*) AS total FROM dataset",
				})
				if err != nil {
					t.Fatal(err)
				}
				checkQuery := func(label string, tool *DataAnalysisTool) {
					t.Helper()
					result, err := tool.Execute(ctx, args)
					if err != nil || result == nil || !result.Success {
						t.Errorf("%s: result=%+v err=%v", label, result, err)
						return
					}
					if !strings.Contains(result.Output, `"total":"2"`) {
						t.Errorf("%s: unexpected output %s", label, result.Output)
					}
				}

				// Interleave the lifetimes deterministically: the second run loads
				// the same document before the first run finishes using its table.
				checkQuery("first run", first)
				checkQuery("second overlapping run", second)
				second.Cleanup(ctx)
				checkQuery("first run after second cleanup", first)
				checkQuery("first run reuses its loaded table", first)

				first.Cleanup(ctx)
				var tables int
				if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM duckdb_tables()").Scan(&tables); err != nil {
					t.Fatal(err)
				}
				if tables != 0 {
					t.Fatalf("cleanup left %d tables, want none", tables)
				}
				checkQuery("new load after cleanup", second)
			})
		}
	}
}
