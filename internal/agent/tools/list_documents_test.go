package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func newListDocumentsFixture() *ListDocumentsTool {
	updated := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	docs := []*types.Knowledge{
		{
			ID: "doc-a", KnowledgeBaseID: "kb-1", Title: "Alpha Guide", FileType: "pdf",
			ParseStatus: "completed", UpdatedAt: updated, Description: "First",
		},
		{
			ID: "doc-b", KnowledgeBaseID: "kb-1", Title: "Beta Notes", FileType: "md",
			ParseStatus: "completed", UpdatedAt: updated,
		},
		{
			ID: "doc-c", KnowledgeBaseID: "kb-1", Title: "Alpha Errata", FileType: "txt",
			ParseStatus: "failed", UpdatedAt: updated,
		},
	}
	service := &readDocKnowledgeService{
		docs:  map[string]*types.Knowledge{"doc-a": docs[0], "doc-b": docs[1], "doc-c": docs[2]},
		pages: map[string]*types.PageResult{"kb-1": {Data: docs}},
	}
	return NewListDocumentsTool(service, types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 7,
	}})
}

func TestListDocumentsRequiresKnowledgeBaseInScope(t *testing.T) {
	tool := newListDocumentsFixture()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || res.Success {
		t.Fatalf("missing kb: res=%+v err=%v", res, err)
	}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-9"}`))
	if err == nil || res.Success {
		t.Fatalf("out-of-scope kb: res=%+v err=%v", res, err)
	}
}

func TestListDocumentsRendersRowsForModelAndUI(t *testing.T) {
	tool := newListDocumentsFixture()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","page_size":2}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	rows, _ := res.Data["documents"].([]map[string]interface{})
	if len(rows) != 3 || rows[0]["knowledge_id"] != "doc-a" || rows[0]["is_faq"] != false {
		t.Fatalf("rows = %+v", rows)
	}
	if res.Data["display_type"] != "document_info" || res.Data["total_docs"] != int64(3) || res.Data["page"] != 1 {
		t.Fatalf("data = %+v", res.Data)
	}
	if res.Data["next_page"] != 2 {
		t.Fatalf("3 documents at page_size 2 must advertise page 2: %+v", res.Data)
	}
	if !strings.Contains(res.Output, `<documents knowledge_base_id="kb-1" total="3" page="1" page_size="2">`) ||
		!strings.Contains(res.Output, `updated_at="2026-03-04"`) ||
		!strings.Contains(res.Output, ">First</document>") {
		t.Fatalf("output = %s", res.Output)
	}
}

func TestListDocumentsKeywordFilterAndEmptyStatement(t *testing.T) {
	tool := newListDocumentsFixture()
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","keyword":"alpha"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	rows, _ := res.Data["documents"].([]map[string]interface{})
	if len(rows) != 2 || res.Data["keyword"] != "alpha" {
		t.Fatalf("keyword filter rows = %+v data=%+v", rows, res.Data)
	}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","keyword":"zzz"}`))
	if err != nil || !res.Success || !strings.Contains(res.Output, `No documents matching "zzz"`) {
		t.Fatalf("empty keyword result: res=%+v err=%v", res, err)
	}
}

func TestListDocumentsHonoursPinnedDocumentScope(t *testing.T) {
	tool := newListDocumentsFixture()
	tool.searchTargets = types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", TenantID: 7, KnowledgeIDs: []string{"doc-b"},
	}}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	rows, _ := res.Data["documents"].([]map[string]interface{})
	if len(rows) != 1 || rows[0]["knowledge_id"] != "doc-b" || res.Data["hidden_by_scope"] != 2 {
		t.Fatalf("scope filter rows = %+v data=%+v", rows, res.Data)
	}
}
