package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---- fakes -----------------------------------------------------------------

type readDocKnowledgeService struct {
	interfaces.KnowledgeService
	docs  map[string]*types.Knowledge
	pages map[string]*types.PageResult // keyed by kb id
	tags  map[string][]*types.KnowledgeTag
}

func (f *readDocKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	if k, ok := f.docs[id]; ok {
		return k, nil
	}
	return nil, errors.New("record not found")
}

func (f *readDocKnowledgeService) GetKnowledgeTags(
	_ context.Context, ids []string,
) (map[string][]*types.KnowledgeTag, error) {
	out := make(map[string][]*types.KnowledgeTag, len(ids))
	for _, id := range ids {
		if tags, ok := f.tags[id]; ok {
			out[id] = tags
		}
	}
	return out, nil
}

func (f *readDocKnowledgeService) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context, kbID string, page *types.Pagination, filter types.KnowledgeListFilter,
) (*types.PageResult, error) {
	result, ok := f.pages[kbID]
	if !ok {
		return &types.PageResult{Page: page.Page, PageSize: page.PageSize, Data: []*types.Knowledge{}}, nil
	}
	rows, _ := result.Data.([]*types.Knowledge)
	if filter.Keyword != "" {
		filtered := make([]*types.Knowledge, 0, len(rows))
		for _, k := range rows {
			if strings.Contains(strings.ToLower(k.Title), strings.ToLower(filter.Keyword)) {
				filtered = append(filtered, k)
			}
		}
		rows = filtered
	}
	return &types.PageResult{Total: int64(len(rows)), Page: page.Page, PageSize: page.PageSize, Data: rows}, nil
}

type readDocChunkRepo struct {
	interfaces.ChunkRepository
	ordered  []*types.Chunk // document order
	listErr  error
	requests []types.Pagination
}

func (r *readDocChunkRepo) ListPagedChunksByKnowledgeID(
	_ context.Context, _ uint64, _ string, page *types.Pagination, _ []types.ChunkType,
	_ []string, _ string, _ string, _ string, _ string, _ *bool,
) ([]*types.Chunk, int64, error) {
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	r.requests = append(r.requests, *page)
	start := (page.Page - 1) * page.PageSize
	end := start + page.PageSize
	total := int64(len(r.ordered))
	if start >= len(r.ordered) {
		return nil, total, nil
	}
	if end > len(r.ordered) {
		end = len(r.ordered)
	}
	out := make([]*types.Chunk, end-start)
	copy(out, r.ordered[start:end])
	return out, total, nil
}

func (r *readDocChunkRepo) ListChunksByParentIDs(context.Context, uint64, []string) ([]*types.Chunk, error) {
	return nil, nil
}

type readDocChunkService struct {
	interfaces.ChunkService
	repo *readDocChunkRepo
}

func (s *readDocChunkService) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	for _, c := range s.repo.ordered {
		if c.ID == id {
			return c, nil
		}
	}
	return nil, errors.New("chunk not found")
}

func (s *readDocChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

func testDocumentChunks(knowledgeID string, n int) []*types.Chunk {
	chunks := make([]*types.Chunk, 0, n)
	for i := 0; i < n; i++ {
		chunks = append(chunks, &types.Chunk{
			ID: fmt.Sprintf("chunk-%d", i), TenantID: 7, KnowledgeID: knowledgeID, KnowledgeBaseID: "kb-1",
			ChunkIndex: i, ChunkType: types.ChunkTypeText, IsEnabled: true,
			Content: fmt.Sprintf("Section %d body. %s", i, engineSentence(i == 4 || i == 9)),
		})
	}
	return chunks
}

func engineSentence(present bool) string {
	if present {
		return "The psionic engine is here."
	}
	return ""
}

func newReadDocumentFixture(chunkCount int) (*ReadDocumentTool, *readDocChunkRepo) {
	doc := &types.Knowledge{
		ID: "doc-1", TenantID: 7, KnowledgeBaseID: "kb-1", Title: "Engine Manual", Type: "file",
		FileName: "manual.pdf", FileType: "pdf", FileSize: 2048, ParseStatus: "completed",
		Description: "How the engine works",
	}
	repo := &readDocChunkRepo{ordered: testDocumentChunks(doc.ID, chunkCount)}
	tool := NewReadDocumentTool(
		&readDocKnowledgeService{docs: map[string]*types.Knowledge{doc.ID: doc}},
		&readDocChunkService{repo: repo},
		types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 7}},
	)
	return tool, repo
}

func chunkIDsFromData(t *testing.T, data map[string]interface{}) []string {
	t.Helper()
	rows, ok := data["chunks"].([]map[string]interface{})
	if !ok {
		t.Fatalf("chunks payload = %#v", data["chunks"])
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, fmt.Sprint(r["chunk_id"]))
	}
	return ids
}

// ---- tests -----------------------------------------------------------------

func TestReadDocumentRequiresID(t *testing.T) {
	tool, _ := newReadDocumentFixture(3)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "id is required") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestReadDocumentPagesFromExactOffset(t *testing.T) {
	tool, repo := newReadDocumentFixture(12)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","offset":5,"limit":4}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := chunkIDsFromData(t, res.Data); strings.Join(got, ",") != "chunk-5,chunk-6,chunk-7,chunk-8" {
		t.Fatalf("window = %v", got)
	}
	if res.Data["next_offset"] != 9 || res.Data["total_chunks"] != int64(12) || res.Data["fetched_chunks"] != 4 {
		t.Fatalf("pagination data = %+v", res.Data)
	}
	if len(repo.requests) != 2 {
		t.Fatalf("an unaligned window needs exactly two page fetches, got %v", repo.requests)
	}
	info, _ := res.Data["document"].(map[string]interface{})
	if info["title"] != "Engine Manual" || info["file_type"] != "pdf" || info["chunk_count"] != int64(12) {
		t.Fatalf("document header = %+v", info)
	}
	if !strings.Contains(res.Output, `title="Engine Manual"`) ||
		!strings.Contains(res.Output, "<description>How the engine works</description>") {
		t.Fatalf("output header missing: %s", res.Output)
	}
}

func TestReadDocumentUnalignedWindowReachesDocumentEnd(t *testing.T) {
	tool, repo := newReadDocumentFixture(9)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","offset":6,"limit":4}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := chunkIDsFromData(t, res.Data); strings.Join(got, ",") != "chunk-6,chunk-7,chunk-8" {
		t.Fatalf("window past the document end = %v, want every remaining chunk", got)
	}
	if _, ok := res.Data["next_offset"]; ok {
		t.Fatalf("nothing remains after chunk-8: %+v", res.Data)
	}
	if len(repo.requests) != 2 {
		t.Fatalf("expected the second page to be fetched, requests=%v", repo.requests)
	}
}

func TestReadDocumentChunkContextNearDocumentEnd(t *testing.T) {
	tool, _ := newReadDocumentFixture(9)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"chunk-7","context":2}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := chunkIDsFromData(t, res.Data); strings.Join(got, ",") != "chunk-5,chunk-6,chunk-7,chunk-8" {
		t.Fatalf("context near the end = %v", got)
	}
	if res.Data["total_chunks"] != int64(9) {
		t.Fatalf("total must come from the window fetch: %+v", res.Data)
	}
}

func TestReadDocumentQueryWithChunkHandleSearchesOwningDocument(t *testing.T) {
	tool, _ := newReadDocumentFixture(12)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"chunk-1","query":"psionic"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Data["match_count"] != 2 || res.Data["query"] != "psionic" {
		t.Fatalf("query must run against the owning document, data=%+v", res.Data)
	}
}

func TestReadDocumentQueryStopsAtOutputBudget(t *testing.T) {
	tool, repo := newReadDocumentFixture(12)
	for _, c := range repo.ordered {
		c.Content = "needle " + strings.Repeat("x", 400)
	}
	ctx := WithOutputBudget(context.Background(), 1500)
	res, err := tool.Execute(ctx, json.RawMessage(`{"id":"doc-1","query":"needle"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Data["truncated"] != true {
		t.Fatalf("budget exhaustion must be reported: %+v", res.Data)
	}
	if fetched := res.Data["fetched_chunks"].(int); fetched == 0 || fetched >= 12 {
		t.Fatalf("expected a partial, budget-bound result, fetched=%d", fetched)
	}
}

func TestReadDocumentLastPageHasNoNextOffset(t *testing.T) {
	tool, _ := newReadDocumentFixture(5)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","offset":0,"limit":20}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, ok := res.Data["next_offset"]; ok {
		t.Fatalf("complete read must not advertise a next page: %+v", res.Data)
	}
	if got := chunkIDsFromData(t, res.Data); len(got) != 5 {
		t.Fatalf("expected all 5 chunks, got %v", got)
	}
}

func TestReadDocumentOffsetOutOfRangeSuggestsValidOffset(t *testing.T) {
	tool, _ := newReadDocumentFixture(5)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","offset":40,"limit":20}`))
	if err != nil {
		t.Fatalf("out-of-range is a tool-level error, not a Go error: %v", err)
	}
	if res.Success || !strings.Contains(res.Error, "offset 40 is out of range") ||
		!strings.Contains(res.Error, "offset=0") {
		t.Fatalf("res=%+v", res)
	}
}

func TestReadDocumentByChunkHandleWithContext(t *testing.T) {
	tool, _ := newReadDocumentFixture(10)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"chunk-4","context":1}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := chunkIDsFromData(t, res.Data); strings.Join(got, ",") != "chunk-3,chunk-4,chunk-5" {
		t.Fatalf("context window = %v", got)
	}
	rows := res.Data["chunks"].([]map[string]interface{})
	if rows[0]["role"] != "context_before" || rows[1]["role"] != "focus" || rows[2]["role"] != "context_after" {
		t.Fatalf("roles = %v %v %v", rows[0]["role"], rows[1]["role"], rows[2]["role"])
	}
	if res.Data["focus_chunk_id"] != "chunk-4" || res.Data["single_chunk"] != false {
		t.Fatalf("data = %+v", res.Data)
	}
}

func TestReadDocumentByChunkHandleAloneIsSingleChunk(t *testing.T) {
	tool, _ := newReadDocumentFixture(3)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"chunk-2"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := chunkIDsFromData(t, res.Data); strings.Join(got, ",") != "chunk-2" {
		t.Fatalf("chunks = %v", got)
	}
	if res.Data["single_chunk"] != true || res.Data["knowledge_title"] != "Engine Manual" {
		t.Fatalf("data = %+v", res.Data)
	}
}

func TestReadDocumentQueryReturnsMatchesWithContext(t *testing.T) {
	tool, _ := newReadDocumentFixture(12)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","query":"PSIONIC engine"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := "chunk-3,chunk-4,chunk-5,chunk-8,chunk-9,chunk-10"
	if got := chunkIDsFromData(t, res.Data); strings.Join(got, ",") != want {
		t.Fatalf("matches with context = %v", got)
	}
	if res.Data["match_count"] != 2 || res.Data["truncated"] != false || res.Data["query"] != "PSIONIC engine" {
		t.Fatalf("data = %+v", res.Data)
	}
	rows := res.Data["chunks"].([]map[string]interface{})
	if rows[1]["role"] != "match" || rows[1]["match_snippet"] == nil {
		t.Fatalf("match row = %+v", rows[1])
	}
	if !strings.Contains(res.Output, "<query>PSIONIC engine</query>") {
		t.Fatalf("output = %s", res.Output)
	}
}

func TestReadDocumentQueryIsLiteralUnlessRegex(t *testing.T) {
	tool, repo := newReadDocumentFixture(3)
	repo.ordered[1].Content = "Compile with C++ and check v1.2"
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","query":"C++"}`))
	if err != nil || !res.Success || res.Data["match_count"] != 1 {
		t.Fatalf("literal C++ should match once: res=%+v err=%v", res, err)
	}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","query":"C++","regex":true}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "invalid regex") {
		t.Fatalf("C++ is not a valid regex: res=%+v err=%v", res, err)
	}
	alternation := json.RawMessage(`{"id":"doc-1","query":"v1\\.2|nothing","regex":true}`)
	res, err = tool.Execute(context.Background(), alternation)
	if err != nil || !res.Success || res.Data["match_count"] != 1 {
		t.Fatalf("regex alternation: res=%+v err=%v", res, err)
	}
}

func TestReadDocumentQueryWithoutMatchesKeepsHeader(t *testing.T) {
	tool, _ := newReadDocumentFixture(3)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1","query":"absent"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Data["match_count"] != 0 || res.Data["fetched_chunks"] != 0 {
		t.Fatalf("data = %+v", res.Data)
	}
	if info, _ := res.Data["document"].(map[string]interface{}); info["title"] != "Engine Manual" {
		t.Fatalf("header must survive an empty match set: %+v", res.Data)
	}
}

func TestReadDocumentRejectsUnknownIDAndOutOfScopeDocument(t *testing.T) {
	tool, _ := newReadDocumentFixture(3)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"nope"}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "neither a document") {
		t.Fatalf("unknown id: res=%+v err=%v", res, err)
	}

	foreign := &types.Knowledge{ID: "doc-2", TenantID: 9, KnowledgeBaseID: "kb-9", Title: "Other"}
	tool.knowledgeService = &readDocKnowledgeService{docs: map[string]*types.Knowledge{"doc-2": foreign}}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-2"}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "not accessible") {
		t.Fatalf("out-of-scope document: res=%+v err=%v", res, err)
	}
}

func TestReadDocumentSurfacesRepositoryFailure(t *testing.T) {
	tool, repo := newReadDocumentFixture(3)
	repo.listErr = errors.New("db down")
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"doc-1"}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "db down") {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestReadDocumentFAQChunkExposesQuestion(t *testing.T) {
	tool, repo := newReadDocumentFixture(2)
	faq := repo.ordered[0]
	faq.ChunkType = types.ChunkTypeFAQ
	meta := &types.FAQChunkMetadata{StandardQuestion: "How to reset?", Answers: []string{"Hold the button."}}
	if err := faq.SetFAQMetadata(meta); err != nil {
		t.Fatal(err)
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"chunk-0"}`))
	if err != nil || !res.Success {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Data["faq_question"] != "How to reset?" || res.Data["faq_id"] != "chunk-0" {
		t.Fatalf("data = %+v", res.Data)
	}
	if !strings.Contains(res.Output, "<answer>Hold the button.</answer>") {
		t.Fatalf("output = %s", res.Output)
	}
}
