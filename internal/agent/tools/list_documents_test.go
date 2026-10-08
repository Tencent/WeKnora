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
	if len(rows) != 2 || rows[0]["knowledge_id"] != "doc-a" || rows[0]["is_faq"] != false {
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

func TestListDocumentsPinnedDocumentsPageAndCountOnlyInScope(t *testing.T) {
	tool := newListDocumentsFixture()
	tool.searchTargets = types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", TenantID: 7,
		KnowledgeIDs: []string{"doc-b", "doc-c"},
	}}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","page_size":1}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	rows, _ := res.Data["documents"].([]map[string]interface{})
	if len(rows) != 1 || res.Data["total_docs"] != int64(2) || res.Data["next_page"] != 2 {
		t.Fatalf("pinned scope must be counted and paged by itself: rows=%+v data=%+v", rows, res.Data)
	}

	res, _ = tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","page_size":1,"page":2}`))
	second, _ := res.Data["documents"].([]map[string]interface{})
	if len(second) != 1 || second[0]["knowledge_id"] == rows[0]["knowledge_id"] {
		t.Fatalf("page 2 must hold the other pinned document: %+v", second)
	}
	if _, ok := res.Data["next_page"]; ok {
		t.Fatalf("no page after the last pinned document: %+v", res.Data)
	}

	res, _ = tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","keyword":"beta"}`))
	filtered, _ := res.Data["documents"].([]map[string]interface{})
	if len(filtered) != 1 || filtered[0]["knowledge_id"] != "doc-b" || res.Data["total_docs"] != int64(1) {
		t.Fatalf("keyword applies to pinned documents: %+v data=%+v", filtered, res.Data)
	}
}

func TestListDocumentsTagScopeIsPushedIntoTheQuery(t *testing.T) {
	tool := newListDocumentsFixture()
	service := tool.knowledgeService.(*readDocKnowledgeService)
	service.tags = map[string][]*types.KnowledgeTag{
		"doc-a": {testKnowledgeTag("tag-x")},
		"doc-c": {testKnowledgeTag("tag-x")},
	}
	tool.searchTargets = types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 7, TagIDs: []string{"tag-x"},
	}}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1","page_size":1}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Data["total_docs"] != int64(2) || res.Data["next_page"] != 2 {
		t.Fatalf("tag scope total must count tagged documents only: %+v", res.Data)
	}
	last := service.listFilters[len(service.listFilters)-1]
	if len(last.TagIDs) != 1 || last.TagIDs[0] != "tag-x" {
		t.Fatalf("tag scope must reach the database filter: %+v", last)
	}
}

func TestListDocumentsUnionOfPinnedDocumentsAndTags(t *testing.T) {
	tool := newListDocumentsFixture()
	service := tool.knowledgeService.(*readDocKnowledgeService)
	service.tags = map[string][]*types.KnowledgeTag{"doc-c": {testKnowledgeTag("tag-x")}}
	tool.searchTargets = types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", TenantID: 7, KnowledgeIDs: []string{"doc-a"}},
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 7, TagIDs: []string{"tag-x"}},
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"knowledge_base_id":"kb-1"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	rows, _ := res.Data["documents"].([]map[string]interface{})
	got := make([]string, 0, len(rows))
	for _, r := range rows {
		got = append(got, r["knowledge_id"].(string))
	}
	if strings.Join(got, ",") != "doc-a,doc-c" && strings.Join(got, ",") != "doc-c,doc-a" {
		t.Fatalf("union = %v", got)
	}
	if res.Data["total_docs"] != int64(2) {
		t.Fatalf("union total = %+v", res.Data)
	}
}
