package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// mapKnowledgeService resolves documents from a fixed map and counts lookups
// so tests can assert that each ref is resolved exactly once.
type mapKnowledgeService struct {
	interfaces.KnowledgeService
	docs    map[string]*types.Knowledge
	lookups int
}

func (s *mapKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	s.lookups++
	if doc, ok := s.docs[id]; ok {
		return doc, nil
	}
	return nil, fmt.Errorf("record not found")
}

func TestResolveWikiSourceDocumentsRebuildsTitlesAndDedupes(t *testing.T) {
	service := &mapKnowledgeService{docs: map[string]*types.Knowledge{
		"doc-1": {ID: "doc-1", KnowledgeBaseID: "kb-1", Title: "Real title"},
		"doc-2": {ID: "doc-2", KnowledgeBaseID: "kb-1", FileName: "fallback.pdf"},
		"doc-3": {ID: "doc-3", KnowledgeBaseID: "kb-2"},
	}}
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1"}}
	docs, err := resolveWikiSourceDocuments(
		context.Background(),
		[]string{"doc-1|model-supplied title", " doc-1 ", "doc-2", "", "|orphan title"},
		service, targets, true,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	refs := wikiSourceRefs(docs)
	want := []string{"doc-1|Real title", "doc-2|fallback.pdf"}
	if strings.Join(refs, ",") != strings.Join(want, ",") {
		t.Fatalf("refs = %v, want %v", refs, want)
	}
	if got := wikiSourceKnowledgeIDs(docs); strings.Join(got, ",") != "doc-1,doc-2" {
		t.Fatalf("ids = %v", got)
	}
	// Two distinct documents, each looked up once; the duplicate and the
	// empty entries never reach the service.
	if service.lookups != 3 {
		t.Fatalf("lookups = %d, want 3 (duplicate doc-1 is looked up before dedupe, empties skipped)", service.lookups)
	}
}

func TestResolveWikiSourceDocumentsFailsClosedOutsideScope(t *testing.T) {
	service := &mapKnowledgeService{docs: map[string]*types.Knowledge{
		"doc-3": {ID: "doc-3", KnowledgeBaseID: "kb-2", Title: "Elsewhere"},
	}}
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1"}}
	if _, err := resolveWikiSourceDocuments(
		context.Background(), []string{"doc-3"}, service, targets, true,
	); err == nil {
		t.Fatal("document outside the Agent scope must be rejected")
	}
	if _, err := resolveWikiSourceDocuments(
		context.Background(), []string{"doc-missing"}, service, targets, false,
	); err == nil {
		t.Fatal("unknown document must be rejected even without scope enforcement")
	}
}

func TestResolveWikiSourceDocumentsWithoutKnowledgeServiceKeepsBareIDs(t *testing.T) {
	docs, err := resolveWikiSourceDocuments(
		context.Background(), []string{" doc-1|typed title ", "doc-1"}, nil, nil, false,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if refs := wikiSourceRefs(docs); len(refs) != 1 || refs[0] != "doc-1" {
		t.Fatalf("refs = %v, want a single bare doc-1 (model title is never trusted)", refs)
	}
	hints, err := wikiKnowledgeBasesForSourceDocuments(docs, []string{"kb-1"})
	if err != nil || len(hints) != 0 {
		t.Fatalf("documents with unknown KB must contribute no routing hint: hints=%v err=%v", hints, err)
	}
}

func TestWikiKnowledgeBasesForSourceDocuments(t *testing.T) {
	docs := []wikiSourceDocument{
		{ID: "doc-1", KnowledgeBaseID: "kb-1"},
		{ID: "doc-2", KnowledgeBaseID: "kb-1"},
		{ID: "doc-3", KnowledgeBaseID: "kb-2"},
	}
	hints, err := wikiKnowledgeBasesForSourceDocuments(docs, []string{"kb-1", "kb-2"})
	if err != nil || strings.Join(hints, ",") != "kb-1,kb-2" {
		t.Fatalf("hints = %v err = %v", hints, err)
	}
	if _, err := wikiKnowledgeBasesForSourceDocuments(docs, []string{"kb-1"}); err == nil {
		t.Fatal("a document owned by a KB outside the Wiki scope must fail routing")
	}
	if hints, err := wikiKnowledgeBasesForSourceDocuments(nil, []string{"kb-1"}); err != nil || hints != nil {
		t.Fatalf("no documents must yield no hints: %v %v", hints, err)
	}
}

func TestWikiReplaceTextRebuildsSourceRefsFromServer(t *testing.T) {
	service := &sourceRefWikiService{page: &types.WikiPage{
		KnowledgeBaseID: "kb-1",
		Slug:            "concept/a",
		Content:         "old body",
		SourceRefs:      types.StringArray{"doc-old|Old"},
	}}
	knowledgeService := &mapKnowledgeService{docs: map[string]*types.Knowledge{
		"doc-2": {ID: "doc-2", KnowledgeBaseID: "kb-1", Title: "Server title"},
	}}
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1"}}
	tool := NewWikiReplaceTextTool(service, []string{"kb-1"}, knowledgeService, NewWikiRouteResolver()).
		WithSearchTargets(targets)
	result, err := tool.Execute(context.Background(), []byte(
		`{"slug":"concept/a","old_text":"old","new_text":"new","source_refs":["doc-2|hallucinated"]}`,
	))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("replace failed: result=%+v err=%v", result, err)
	}
	if got := strings.Join(service.page.SourceRefs, ","); got != "doc-2|Server title" {
		t.Fatalf("source_refs = %q, want server-rebuilt title", got)
	}
}

func TestWikiFlagIssueStoresBareAuthorizedKnowledgeIDs(t *testing.T) {
	service := &flagIssueWikiService{page: &types.WikiPage{
		TenantID: 1, KnowledgeBaseID: "kb-1", Slug: "concept/a",
	}}
	knowledgeService := &mapKnowledgeService{docs: map[string]*types.Knowledge{
		"doc-2": {ID: "doc-2", KnowledgeBaseID: "kb-1", Title: "T"},
	}}
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1"}}
	tool := NewWikiFlagIssueTool(service, []string{"kb-1"}, NewWikiRouteResolver()).
		WithKnowledgeScope(knowledgeService, targets)
	result, err := tool.Execute(context.Background(), []byte(
		`{"slug":"concept/a","issue_type":"contradiction","description":"d",`+
			`"suspected_knowledge_ids":["doc-2|title","doc-2"]}`,
	))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("flag failed: result=%+v err=%v", result, err)
	}
	if service.issue == nil || strings.Join(service.issue.SuspectedKnowledgeIDs, ",") != "doc-2" {
		t.Fatalf("issue = %+v, want deduped bare doc-2", service.issue)
	}
	result, err = tool.Execute(context.Background(), []byte(
		`{"slug":"concept/a","issue_type":"contradiction","description":"d","suspected_knowledge_ids":["doc-unknown"]}`,
	))
	if err != nil || result == nil || result.Success {
		t.Fatalf("unknown suspected document must fail closed: result=%+v err=%v", result, err)
	}
}

// flagIssueWikiService is the minimal WikiPageService for wiki_flag_issue.
type flagIssueWikiService struct {
	interfaces.WikiPageService
	page  *types.WikiPage
	issue *types.WikiPageIssue
}

func (s *flagIssueWikiService) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	return s.page, nil
}

func (s *flagIssueWikiService) CreateIssue(
	_ context.Context, issue *types.WikiPageIssue,
) (*types.WikiPageIssue, error) {
	s.issue = issue
	return issue, nil
}
