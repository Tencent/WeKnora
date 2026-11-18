package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func TestTabularFileType(t *testing.T) {
	cases := map[string]string{
		".XLSX": "xlsx",
		" csv ": "csv",
		".xls":  "xls",
		"pdf":   "",
		".pdf":  "",
		"":      "",
		"docx":  "",
	}
	for in, want := range cases {
		if got := TabularFileType(in); got != want {
			t.Errorf("TabularFileType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChatTabularAttachmentsSetDropsNonTabularAndDedupes(t *testing.T) {
	store := NewChatTabularAttachments()
	store.Set([]ChatTabularAttachment{
		{ID: " excel ", FileName: "sales.xlsx", FileType: ".xlsx"},
		{ID: "excel", FileName: "again.xlsx", FileType: "xlsx"},
		{ID: "pdf-1", FileName: "manual.pdf", FileType: "pdf"},
		{ID: "", FileName: "missing.csv", FileType: "csv"},
	})
	items := store.List()
	if len(items) != 1 || items[0].ID != "excel" || items[0].FileType != "xlsx" {
		t.Fatalf("allowlist = %+v", items)
	}
	if _, ok := store.Get("pdf-1"); ok {
		t.Fatal("pdf must not be readable through the chat-upload allowlist")
	}
}

// panicChunkRepo fails the test if data_schema asks the knowledge base for
// table-summary chunks of a chat upload. Those chunks are never created for
// a temporary document.
type panicChunkRepo struct {
	interfaces.ChunkRepository
}

func (panicChunkRepo) ListPagedChunksByKnowledgeID(
	context.Context, uint64, string, *types.Pagination, []types.ChunkType,
	[]string, string, string, string, string, *bool,
) ([]*types.Chunk, int64, error) {
	panic("chunk repository must not be used for a chat upload")
}

// TestChatCSVAnalyzedWhilePDFStaysUnsupported is the tool-side regression for
// issue #4170. The chat spreadsheet is loaded from its own bytes even when
// the turn's search targets only contain a knowledge-base PDF. That PDF still
// returns "unsupported file type: pdf". An ID outside the allowlist is not
// opened through a caller-supplied reader and is not treated as the spreadsheet.
func TestChatCSVAnalyzedWhilePDFStaysUnsupported(t *testing.T) {
	db := newPlainDuckDB(t)
	const pdfID = "22222222-2222-4222-8222-222222222222"
	const excelID = "11111111-1111-4111-8111-111111111111"
	const unknownID = "33333333-3333-4333-8333-333333333333"

	knowledge := &mapKnowledgeService{docs: map[string]*types.Knowledge{
		pdfID: {
			ID:              pdfID,
			KnowledgeBaseID: "kb-1",
			FileType:        "pdf",
			FilePath:        "manual.pdf",
			Title:           "manual.pdf",
		},
	}}
	opened := 0
	poisoned := 0
	store := NewChatTabularAttachments()
	store.Set([]ChatTabularAttachment{
		{
			ID:       excelID,
			FileName: "sales.csv",
			FileType: ".csv",
			Open: func(context.Context) (io.ReadCloser, error) {
				opened++
				return io.NopCloser(strings.NewReader("name,qty\nalice,1\nbob,2\n")), nil
			},
		},
		{
			ID:       unknownID,
			FileName: "manual.pdf",
			FileType: "pdf",
			Open: func(context.Context) (io.ReadCloser, error) {
				poisoned++
				return io.NopCloser(strings.NewReader("%PDF-1.4")), nil
			},
		},
	})

	tool := NewDataAnalysisTool(
		nil,
		knowledge,
		nil,
		&csvFileService{files: map[string]string{"manual.pdf": "%PDF-1.4"}},
		db,
		"sess-1",
	).WithSearchTargets(types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
	}}).WithChatAttachments(store)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	schemaTool := NewDataSchemaTool(knowledge, panicChunkRepo{}).
		WithSearchTargets(tool.searchTargets).
		WithDataAnalysis(tool)

	schemaArgs, _ := json.Marshal(DataSchemaInput{KnowledgeID: excelID})
	schemaResult, schemaErr := schemaTool.Execute(ctx, schemaArgs)
	if schemaErr != nil || schemaResult == nil || !schemaResult.Success {
		t.Fatalf("schema failed: result=%+v err=%v", schemaResult, schemaErr)
	}
	if schemaResult.Data["file_type"] != "csv" || schemaResult.Data["source"] != "chat_upload" {
		t.Fatalf("schema data = %#v", schemaResult.Data)
	}
	for _, want := range []string{
		"chat-uploaded csv file", "sales.csv", "was not ingested", `"dataset"`, "name", "qty",
	} {
		if !strings.Contains(schemaResult.Output, want) {
			t.Fatalf("schema output missing %q:\n%s", want, schemaResult.Output)
		}
	}
	if strings.Contains(schemaResult.Output, excelID) {
		t.Fatalf("schema output leaked the durable id:\n%s", schemaResult.Output)
	}
	if knowledge.lookups != 0 {
		t.Fatalf("chat upload was looked up as knowledge %d times", knowledge.lookups)
	}
	if opened != 1 {
		t.Fatalf("schema opened the upload %d times, want 1", opened)
	}

	args, _ := json.Marshal(DataAnalysisInput{
		KnowledgeID: excelID,
		SQL:         `SELECT COUNT(*) AS n FROM dataset`,
	})
	result, err := tool.Execute(ctx, args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("chat csv analysis failed: result=%+v err=%v", result, err)
	}
	if !strings.Contains(result.Output, `"n":"2"`) {
		t.Fatalf("unexpected analysis output:\n%s", result.Output)
	}
	if strings.Contains(result.Output, excelID) {
		t.Fatalf("analysis output leaked the durable id:\n%s", result.Output)
	}
	if knowledge.lookups != 0 {
		t.Fatalf("analysis looked up the chat upload as knowledge")
	}
	if opened != 1 {
		t.Fatalf("shared loader re-opened the file (%d); the DuckDB table should be reused", opened)
	}

	pdfArgs, _ := json.Marshal(DataAnalysisInput{
		KnowledgeID: pdfID,
		SQL:         `SELECT COUNT(*) AS n FROM dataset`,
	})
	pdfResult, pdfErr := tool.Execute(ctx, pdfArgs)
	if pdfErr == nil || pdfResult == nil || !strings.Contains(pdfResult.Error, "unsupported file type: pdf") {
		t.Fatalf("expected unsupported file type: pdf, result=%+v err=%v", pdfResult, pdfErr)
	}
	if knowledge.lookups == 0 {
		t.Fatal("the knowledge-base PDF should still be resolved through the knowledge service")
	}

	unknownArgs, _ := json.Marshal(DataAnalysisInput{
		KnowledgeID: unknownID,
		SQL:         `SELECT COUNT(*) AS n FROM dataset`,
	})
	unknownResult, unknownErr := tool.Execute(ctx, unknownArgs)
	if unknownErr == nil || unknownResult == nil || unknownResult.Success {
		t.Fatalf("id outside the allowlist must not be analyzed: result=%+v err=%v", unknownResult, unknownErr)
	}
	if poisoned != 0 {
		t.Fatal("an opener dropped from the allowlist was called")
	}
	if strings.Contains(unknownResult.Error, "sales.csv") {
		t.Fatalf("unknown id was described as the chat upload: %s", unknownResult.Error)
	}
}

func TestChatXLSXAnalyzedWhenExcelExtensionAvailable(t *testing.T) {
	db := newTestDuckDB(t)
	const excelID = "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(t.TempDir(), "sales.xlsx")
	writeWorkbook(t, path, map[string][][]any{
		"Sheet1": {{"name", "qty"}, {"alice", 1}, {"bob", 2}},
	}, []string{"Sheet1"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read workbook: %v", err)
	}
	store := NewChatTabularAttachments()
	store.Set([]ChatTabularAttachment{{
		ID:       excelID,
		FileName: "sales.xlsx",
		FileType: ".xlsx",
		Open: func(context.Context) (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(data)), nil
		},
	}})
	tool := NewDataAnalysisTool(nil, &mapKnowledgeService{}, nil, nil, db, "sess-xlsx").
		WithSearchTargets(nil).
		WithChatAttachments(store)
	ctx := context.Background()
	t.Cleanup(func() { tool.Cleanup(ctx) })

	args, _ := json.Marshal(DataAnalysisInput{
		KnowledgeID: excelID,
		SQL:         `SELECT COUNT(*) AS n FROM dataset`,
	})
	result, execErr := tool.Execute(ctx, args)
	if execErr != nil || result == nil || !result.Success {
		t.Fatalf("xlsx analysis failed: result=%+v err=%v", result, execErr)
	}
	if !strings.Contains(result.Output, `"n":"2"`) && !strings.Contains(result.Output, `"n":"2.0"`) {
		t.Fatalf("unexpected xlsx output:\n%s", result.Output)
	}
	if strings.Contains(strings.ToLower(result.Output), "pdf") {
		t.Fatalf("xlsx result mentioned pdf:\n%s", result.Output)
	}
}
