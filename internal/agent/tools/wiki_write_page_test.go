package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	customknowledge "github.com/Tencent/WeKnora/internal/custom/service/knowledge"
	transcriptservice "github.com/Tencent/WeKnora/internal/custom/service/transcript"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type sourceRefWikiService struct {
	interfaces.WikiPageService
	page          *types.WikiPage
	createdKB     string
	createCount   int
	updateCount   int
	pagesByID     map[string]*types.WikiPage
	semanticPages []*types.WikiPage
}

type uncertainSemanticAdapter struct{}

func (uncertainSemanticAdapter) Compare(context.Context, customknowledge.IdentityCandidate, customknowledge.IdentityCandidate) (customknowledge.SemanticIdentityAssessment, error) {
	return customknowledge.SemanticIdentityAssessment{Decision: "uncertain", Reason: "model timeout", Confidence: 0.2}, nil
}

type fixedSemanticAdapter struct {
	assessment customknowledge.SemanticIdentityAssessment
}

func (a fixedSemanticAdapter) Compare(context.Context, customknowledge.IdentityCandidate, customknowledge.IdentityCandidate) (customknowledge.SemanticIdentityAssessment, error) {
	return a.assessment, nil
}

func (s *sourceRefWikiService) ListPagesCursor(context.Context, string, string, int) ([]*types.WikiPage, string, error) {
	return s.semanticPages, "", nil
}

func (s *sourceRefWikiService) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	return s.page, nil
}

func (s *sourceRefWikiService) GetPageByID(_ context.Context, id string) (*types.WikiPage, error) {
	if page := s.pagesByID[id]; page != nil {
		return page, nil
	}
	return nil, errors.New("wiki page not found")
}

func (s *sourceRefWikiService) RepairContentLinks(_ context.Context, _, _, content string) (string, bool, error) {
	return content, false, nil
}

func (s *sourceRefWikiService) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.updateCount++
	if page.ID == "" {
		page.ID = "existing-page-id"
	}
	s.page = page
	return page, nil
}

func (s *sourceRefWikiService) CreatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.createCount++
	if page.ID == "" {
		page.ID = "created-page-id"
	}
	s.page = page
	s.createdKB = page.KnowledgeBaseID
	return page, nil
}

func (s *sourceRefWikiService) InjectCrossLinks(context.Context, string, []string) {}
func (s *sourceRefWikiService) RebuildIndexPage(context.Context, string) error     { return nil }

func v2WriteContext() context.Context {
	return v2WriteContextFor("video-1", "generation-1")
}

func v2WriteContextFor(videoID, generation string) context.Context {
	return WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID: "session-1", ProductionTaskID: "job-1", ToolCallID: "call-1",
		ProductionVideoID: videoID, ProductionGeneration: generation,
		PinnedSkillNames: []string{"extract-video-knowledge"},
	})
}

func transcriptSourceKnowledge(t *testing.T, sourceID, videoID, generation string) *types.Knowledge {
	t.Helper()
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: videoID, TranscriptGeneration: generation, Title: "测试视频", DurationSeconds: 10,
		Chapters: []transcriptservice.InputChapter{{
			Index: 0, Title: "正文", Paragraphs: []transcriptservice.InputParagraph{{
				Index: 0, Sentences: []transcriptservice.InputSentence{{
					SourceSentenceID: "sentence-1", EvidenceSentenceID: "evidence-real", Text: "真实证据", StartMs: 1000, EndMs: 2000,
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	documentJSON, err := doc.JSON()
	if err != nil {
		t.Fatal(err)
	}
	knowledge := &types.Knowledge{ID: sourceID, KnowledgeBaseID: "kb-1", Type: types.KnowledgeTypeManual, Title: "测试视频转写"}
	if err := knowledge.SetManualMetadata(types.NewManualKnowledgeMetadata(
		transcriptservice.SourceContent(doc, documentJSON, "hash-1"), types.ManualKnowledgeStatusPublish, 1,
	)); err != nil {
		t.Fatal(err)
	}
	return knowledge
}

func TestNormalizeAndValidateWikiSlug(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "already valid", in: "entity/acme-corp", want: "entity/acme-corp"},
		{name: "lowercased and spaces", in: "Entity/Acme Corp", want: "entity/acme-corp"},
		{name: "trimmed", in: "  concept/rag  ", want: "concept/rag"},
		{name: "cjk kept", in: "entity/上海中心大厦", want: "entity/上海中心大厦"},
		{name: "uuid summary", in: "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e", want: "summary/07a20bb1-a662-47cf-9929-06fb5d5b5b5e"},
		{name: "empty", in: "   ", wantErr: true},
		{name: "leading slash", in: "/entity/x", wantErr: true},
		{name: "trailing slash", in: "entity/x/", wantErr: true},
		{name: "double slash", in: "entity//x", wantErr: true},
		{name: "invalid char", in: "entity/x!y", wantErr: true},
		{name: "invalid space-only becomes empty", in: "  ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAndValidateWikiSlug(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got slug %q", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("normalizeAndValidateWikiSlug(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsSummaryNamespace(t *testing.T) {
	if !isSummaryNamespace("summary/abc") {
		t.Fatal("summary/abc must be in the summary namespace")
	}
	if isSummaryNamespace("summary") {
		t.Fatal("bare 'summary' (no slash) must not count as the summary namespace")
	}
	if isSummaryNamespace("entity/summary-of-x") {
		t.Fatal("entity/summary-of-x must not count as the summary namespace")
	}
}

func TestWikiWritePageDistinguishesOmittedAndExplicitEmptySourceRefs(t *testing.T) {
	for _, test := range []struct {
		name string
		args string
		want int
	}{
		{
			name: "omitted preserves provenance",
			args: `{"slug":"concept/a","title":"A","summary":"S","content":"C","page_type":"concept"}`,
			want: 1,
		},
		{
			name: "empty clears provenance",
			args: `{"slug":"concept/a","title":"A","summary":"S","content":"C","page_type":"concept","source_refs":[]}`,
			want: 0,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &sourceRefWikiService{page: &types.WikiPage{
				KnowledgeBaseID: "kb-1",
				Slug:            "concept/a",
				SourceRefs:      types.StringArray{"doc-real|Document"},
			}}
			tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
			result, err := tool.Execute(context.Background(), json.RawMessage(test.args))
			if err != nil || result == nil || !result.Success {
				t.Fatalf("write failed: result=%+v err=%v", result, err)
			}
			if got := len(service.page.SourceRefs); got != test.want {
				t.Fatalf("source_refs length = %d, want %d", got, test.want)
			}
			if _, exists := result.Data["canonical_wiki_page_id"]; exists {
				t.Fatal("ordinary Wiki writes must not expose knowledge-object canonical identity")
			}
		})
	}
}

func TestWikiWritePageDistinguishesOmittedAndExplicitEmptyAliases(t *testing.T) {
	for _, test := range []struct {
		name string
		args string
		want []string
	}{
		{
			name: "omitted preserves stored aliases",
			args: `{"slug":"concept/a","title":"A","summary":"S","content":"C","page_type":"concept"}`,
			want: []string{"kept"},
		},
		{
			name: "empty clears stored aliases",
			args: `{"slug":"concept/a","title":"A","summary":"S","content":"C","page_type":"concept","aliases":[]}`,
			want: nil,
		},
		{
			name: "explicit list replaces stored aliases",
			args: `{"slug":"concept/a","title":"A","summary":"S","content":"C","page_type":"concept","aliases":["fresh"]}`,
			want: []string{"fresh"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &sourceRefWikiService{page: &types.WikiPage{
				KnowledgeBaseID: "kb-1",
				Slug:            "concept/a",
				Aliases:         types.StringArray{"kept"},
			}}
			tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
			result, err := tool.Execute(context.Background(), json.RawMessage(test.args))
			if err != nil || result == nil || !result.Success {
				t.Fatalf("write failed: result=%+v err=%v", result, err)
			}
			if len(service.page.Aliases) != len(test.want) {
				t.Fatalf("aliases = %v, want %v", service.page.Aliases, test.want)
			}
			for i := range test.want {
				if service.page.Aliases[i] != test.want[i] {
					t.Fatalf("aliases = %v, want %v", service.page.Aliases, test.want)
				}
			}
		})
	}
}

func TestWikiWritePageRoutesNewPageFromAuthorizedSourceRefs(t *testing.T) {
	service := &sourceRefWikiService{}
	knowledgeService := &scopeKnowledgeService{knowledge: &types.Knowledge{
		ID: "doc-2", KnowledgeBaseID: "kb-2", Title: "Document 2",
	}}
	searchTargets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-2", KnowledgeIDs: []string{"doc-2"},
	}}
	tool := NewWikiWritePageTool(
		service, []string{"kb-1", "kb-2"}, knowledgeService, NewWikiRouteResolver(),
	).WithSearchTargets(searchTargets)

	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"slug":"concept/new","title":"New","summary":"Summary","content":"Content","page_type":"concept","source_refs":["doc-2"]}`,
	))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("write failed: result=%+v err=%v", result, err)
	}
	if service.createdKB != "kb-2" {
		t.Fatalf("new page routed to %q, want source-owned kb-2", service.createdKB)
	}
}

func TestWikiWritePageRejectsInvalidKnowledgeObjectBeforePersistence(t *testing.T) {
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	content := `---
knowledge_object_id: ko-invalid
type: concept
primary_type: concept
title: 无核心内容概念
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [ev-1]
source_refs: [doc-1]
structure_fields:
  definition: 这是定义
  components: 这是组成
relations: []
---

# 无核心内容概念

## 定义

这是定义。`

	args, err := json.Marshal(map[string]any{
		"slug":      "concept/invalid-without-core-content",
		"title":     "无核心内容概念",
		"summary":   "工具参数摘要不能替代页面内容契约",
		"content":   content,
		"page_type": "index",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("invalid knowledge object must be rejected, got %+v", result)
	}
	if !strings.Contains(result.Error, "frontmatter.core_content") {
		t.Fatalf("error must identify the invalid field, got %q", result.Error)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("invalid page reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsEvidenceOutsideAuthorizedTranscriptSourceBeforePersistence(t *testing.T) {
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "测试视频", DurationSeconds: 10,
		Chapters: []transcriptservice.InputChapter{{
			Index: 0, Title: "正文", Paragraphs: []transcriptservice.InputParagraph{{
				Index: 0, Sentences: []transcriptservice.InputSentence{{
					SourceSentenceID: "sentence-1", EvidenceSentenceID: "evidence-real", Text: "真实证据", StartMs: 1000, EndMs: 2000,
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	documentJSON, err := doc.JSON()
	if err != nil {
		t.Fatal(err)
	}
	source := &types.Knowledge{ID: "source-1", KnowledgeBaseID: "kb-1", Type: types.KnowledgeTypeManual, Title: "测试视频转写"}
	if err := source.SetManualMetadata(types.NewManualKnowledgeMetadata(
		transcriptservice.SourceContent(doc, documentJSON, "hash-1"), types.ManualKnowledgeStatusPublish, 1,
	)); err != nil {
		t.Fatal(err)
	}
	knowledgeService := &scopeKnowledgeService{knowledge: source}
	searchTargets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"source-1"},
	}}
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, knowledgeService, NewWikiRouteResolver()).WithSearchTargets(searchTargets)
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
title: 测试概念
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [evidence-invented]
source_refs: [source-1]
core_content: 这是一个测试概念。
structure_fields:
  definition: 测试定义
  components: 测试组成
relations: []
---

# 测试概念`
	args, err := json.Marshal(map[string]any{
		"slug": "concept/test", "title": "测试概念", "summary": "这是一个测试概念。",
		"content": content, "page_type": "index", "source_refs": []string{"source-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("invented evidence must be rejected, got %+v", result)
	}
	if !strings.Contains(result.Error, "evidence-invented") || !strings.Contains(result.Error, "current transcript") {
		t.Fatalf("error must identify the unknown evidence, got %q", result.Error)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("invalid evidence reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageAcceptsEvidenceFromAuthorizedTranscriptSource(t *testing.T) {
	doc, err := transcriptservice.Build(transcriptservice.Input{
		VideoID: "video-1", TranscriptGeneration: "generation-1", Title: "测试视频", DurationSeconds: 10,
		Chapters: []transcriptservice.InputChapter{{
			Index: 0, Title: "正文", Paragraphs: []transcriptservice.InputParagraph{{
				Index: 0, Sentences: []transcriptservice.InputSentence{{
					SourceSentenceID: "sentence-1", EvidenceSentenceID: "evidence-real", Text: "真实证据", StartMs: 1000, EndMs: 2000,
				}},
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	documentJSON, err := doc.JSON()
	if err != nil {
		t.Fatal(err)
	}
	source := &types.Knowledge{ID: "source-1", KnowledgeBaseID: "kb-1", Type: types.KnowledgeTypeManual, Title: "测试视频转写"}
	if err := source.SetManualMetadata(types.NewManualKnowledgeMetadata(
		transcriptservice.SourceContent(doc, documentJSON, "hash-1"), types.ManualKnowledgeStatusPublish, 1,
	)); err != nil {
		t.Fatal(err)
	}
	knowledgeService := &scopeKnowledgeService{knowledge: source}
	searchTargets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"source-1"},
	}}
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, knowledgeService, NewWikiRouteResolver()).WithSearchTargets(searchTargets)
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
title: 测试概念
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [evidence-real]
source_refs: [source-1]
core_content: 这是一个测试概念。
structure_fields:
  definition: 测试定义
  components: 测试组成
relations: []
---

# 测试概念`
	args, err := json.Marshal(map[string]any{
		"slug": "concept/test", "title": "测试概念", "summary": "这是一个测试概念。",
		"content": content, "page_type": "index", "source_refs": []string{"source-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	execCtx := WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID: "session-1", ToolCallID: "call-1",
		ProductionTaskID:  "job-1",
		ProductionVideoID: "video-1", ProductionGeneration: "generation-1",
		PinnedSkillNames: []string{"extract-video-knowledge"},
	})
	result, err := tool.Execute(execCtx, args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("valid transcript evidence was rejected: result=%+v err=%v", result, err)
	}
	if service.createCount != 1 || service.updateCount != 0 {
		t.Fatalf("valid evidence persistence = creates=%d updates=%d", service.createCount, service.updateCount)
	}
	if result.Data["canonical_wiki_page_id"] != "created-page-id" {
		t.Fatalf("canonical_wiki_page_id = %v, want created-page-id", result.Data["canonical_wiki_page_id"])
	}
	productionSource, ok := result.Data["production_source"].(map[string]interface{})
	if !ok {
		t.Fatalf("production_source missing from V2 Skill write result: %+v", result.Data)
	}
	if productionSource["page_producer"] != "extract_video_knowledge_v2" ||
		productionSource["task_id"] != "job-1" ||
		productionSource["skill_name"] != "extract-video-knowledge" ||
		productionSource["tool_name"] != "wiki_write_page" ||
		productionSource["tool_call_id"] != "call-1" ||
		productionSource["source_document_id"] != "source-1" {
		t.Fatalf("unexpected V2 production source: %+v", productionSource)
	}
}

func TestWikiWritePagePreservesVideoIndexStorageType(t *testing.T) {
	service := &sourceRefWikiService{page: &types.WikiPage{
		ID: "video-index-1", KnowledgeBaseID: "kb-1", Slug: "video/video-1", PageType: "index", Status: types.WikiPageStatusPublished,
	}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	content := `---
page_type: index
type: knowledge_base
title: 测试视频_知识底座
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
audit_status: aligned
source_refs: [source-1]
---

# 测试视频_知识底座`
	args, err := json.Marshal(map[string]any{
		"slug": "video/video-1", "title": "测试视频_知识底座", "summary": "测试视频知识索引",
		"content": content, "page_type": "concept", "source_refs": []string{"source-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("valid video index update failed: result=%+v err=%v", result, err)
	}
	if service.page.PageType != "index" {
		t.Fatalf("video index page_type = %q, want index", service.page.PageType)
	}
}

func TestWikiWritePageAuditsV2VideoIndex(t *testing.T) {
	service := &sourceRefWikiService{page: &types.WikiPage{
		ID: "video-index-1", KnowledgeBaseID: "kb-1", Slug: "video/video-1", PageType: "index", Status: types.WikiPageStatusPublished,
	}}
	knowledgeService := &scopeKnowledgeService{knowledge: transcriptSourceKnowledge(t, "source-1", "video-1", "generation-1")}
	searchTargets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"source-1"},
	}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, knowledgeService, NewWikiRouteResolver()).WithSearchTargets(searchTargets)
	content := `---
page_type: index
type: knowledge_base
title: 测试视频_知识底座
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
audit_status: aligned
source_refs: [source-1]
---

# 测试视频_知识底座`
	args, err := json.Marshal(map[string]any{
		"slug": "video/video-1", "title": "测试视频_知识底座", "summary": "测试视频知识索引",
		"content": content, "page_type": "index", "source_refs": []string{"source-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	execCtx := WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID: "session-1", ProductionTaskID: "job-1", ToolCallID: "call-index-1",
		ProductionVideoID: "video-1", ProductionGeneration: "generation-1",
		PinnedSkillNames: []string{"extract-video-knowledge"},
	})
	result, err := tool.Execute(execCtx, args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("V2 video index update failed: result=%+v err=%v", result, err)
	}
	productionSource, ok := result.Data["production_source"].(map[string]interface{})
	if !ok || productionSource["task_id"] != "job-1" || productionSource["source_document_id"] != "source-1" {
		t.Fatalf("unexpected V2 video index production source: %+v", productionSource)
	}
	if _, exists := result.Data["canonical_wiki_page_id"]; exists {
		t.Fatalf("video index must not be returned as a canonical knowledge object: %+v", result.Data)
	}
}

func TestWikiWritePageRejectsIncompleteV2ProvenanceBeforePersistence(t *testing.T) {
	service := &sourceRefWikiService{page: &types.WikiPage{
		ID: "video-index-1", KnowledgeBaseID: "kb-1", Slug: "video/video-1", PageType: "index", Status: types.WikiPageStatusPublished,
	}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	content := `---
page_type: index
type: knowledge_base
title: 测试视频_知识底座
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
audit_status: aligned
source_refs: [source-1]
---

# 测试视频_知识底座`
	args, err := json.Marshal(map[string]any{
		"slug": "video/video-1", "title": "测试视频_知识底座", "summary": "测试视频知识索引",
		"content": content, "page_type": "index", "source_refs": []string{"source-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	execCtx := WithToolExecContext(context.Background(), &ToolExecContext{
		SessionID: "session-1", ToolCallID: "call-index-1",
		ProductionVideoID: "video-1", ProductionGeneration: "generation-1",
		PinnedSkillNames: []string{"extract-video-knowledge"},
	})
	result, err := tool.Execute(execCtx, args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "task_id_missing") {
		t.Fatalf("missing V2 task ID must be rejected: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("incomplete V2 provenance reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsV2PageWithoutV2ExecutionContext(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{name: "missing execution context", ctx: context.Background(), want: "knowledge_v2_execution_context_missing"},
		{name: "missing V2 skill", ctx: WithToolExecContext(context.Background(), &ToolExecContext{
			SessionID: "session-1", ProductionTaskID: "job-1", ToolCallID: "call-1", PinnedSkillNames: []string{"another-skill"},
			ProductionVideoID: "video-1", ProductionGeneration: "generation-1",
		}), want: "knowledge_v2_skill_missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &sourceRefWikiService{page: &types.WikiPage{
				ID: "video-index-1", KnowledgeBaseID: "kb-1", Slug: "video/video-1", PageType: "index", Status: types.WikiPageStatusPublished,
			}}
			tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
			content := `---
page_type: index
type: knowledge_base
title: 测试视频_知识底座
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
audit_status: aligned
source_refs: [source-1]
---

# 测试视频_知识底座`
			args, err := json.Marshal(map[string]any{
				"slug": "video/video-1", "title": "测试视频_知识底座", "summary": "测试视频知识索引",
				"content": content, "page_type": "index", "source_refs": []string{"source-1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(test.ctx, args)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || result.Success || !strings.Contains(result.Error, test.want) {
				t.Fatalf("V2 provenance bypass was not rejected: %+v", result)
			}
			if service.createCount != 0 || service.updateCount != 0 {
				t.Fatalf("V2 provenance bypass reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
			}
		})
	}
}

func TestWikiWritePageRejectsV2IndexWhoseSourceIdentityDoesNotMatch(t *testing.T) {
	service := &sourceRefWikiService{page: &types.WikiPage{
		ID: "video-index-1", KnowledgeBaseID: "kb-1", Slug: "video/video-wrong", PageType: "index", Status: types.WikiPageStatusPublished,
	}}
	knowledgeService := &scopeKnowledgeService{knowledge: transcriptSourceKnowledge(t, "source-1", "video-real", "generation-real")}
	searchTargets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-1", KnowledgeIDs: []string{"source-1"},
	}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, knowledgeService, NewWikiRouteResolver()).WithSearchTargets(searchTargets)
	content := `---
page_type: index
type: knowledge_base
title: 错误视频_知识底座
source_video_id: video-wrong
source_document_id: source-1
transcript_generation: generation-wrong
audit_status: aligned
source_refs: [source-1]
---

# 错误视频_知识底座`
	args, err := json.Marshal(map[string]any{
		"slug": "video/video-wrong", "title": "错误视频_知识底座", "summary": "错误索引",
		"content": content, "page_type": "index", "source_refs": []string{"source-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(v2WriteContextFor("video-wrong", "generation-wrong"), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "source_document_identity_mismatch") {
		t.Fatalf("mismatched source identity must be rejected: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("mismatched source identity reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsKnowledgeObjectContentAtVideoIndexSlug(t *testing.T) {
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	content := `---
knowledge_object_id: object-1
type: concept
primary_type: concept
title: 错误对象
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [evidence-1]
source_refs: [source-1]
core_content: 这个对象不能占用视频索引页。
structure_fields:
  definition: 错误定义
  components: 错误组成
relations: []
---

# 错误对象`
	args, err := json.Marshal(map[string]any{
		"slug": "video/video-1", "title": "错误对象", "summary": "错误对象",
		"content": content, "page_type": "index",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "frontmatter.type must be knowledge_base") {
		t.Fatalf("knowledge object must not replace the video index: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("invalid video index reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsRelationTargetPathBeforePersistence(t *testing.T) {
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	content := `---
knowledge_object_id: ko-source
type: concept
primary_type: concept
title: 有效概念
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [ev-1]
source_refs: [doc-1]
core_content: 这是一个有效概念。
structure_fields:
  definition: 这是定义
  components: 这是组成
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: ko-target
    target_wiki_page_id: concept/target
    evidence_ids: [ev-1]
    time_range: 00:00-00:10
    confidence: 0.9
---

# 有效概念

## 一句话概述

这是一个有效概念。`
	args, err := json.Marshal(map[string]any{
		"slug":      "concept/source",
		"title":     "有效概念",
		"summary":   "这是一个有效概念。",
		"content":   content,
		"page_type": "index",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if result == nil || result.Success {
		t.Fatalf("relation target path must be rejected, got %+v", result)
	}
	if !strings.Contains(result.Error, "target_wiki_page_id") {
		t.Fatalf("error must identify the invalid relation target, got %q", result.Error)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("invalid relation reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageAcceptsRelationToValidatedWikiPageID(t *testing.T) {
	targetContent := `---
knowledge_object_id: ko-target
type: methodology
primary_type: methodology
title: 目标方法
source_video_id: video-1
transcript_generation: generation-1
audit_status: passed
information_nature: 方法论
classification_confidence: 0.9
evidence_ids: [ev-2]
source_refs: [doc-1]
core_content: 这是一个可重复执行的方法。
structure_fields:
  input: 输入材料
  steps: 先读取，再执行
relations: []
---

# 目标方法

## 一句话概述

这是一个可重复执行的方法。`
	service := &sourceRefWikiService{pagesByID: map[string]*types.WikiPage{
		"page-target": {
			ID: "page-target", KnowledgeBaseID: "kb-1", Slug: "methodology/target",
			Title: "目标方法", PageType: "index", Status: types.WikiPageStatusPublished, Content: targetContent,
		},
	}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
	sourceContent := `---
knowledge_object_id: ko-source
type: concept
primary_type: concept
title: 来源概念
source_video_id: video-1
source_document_id: doc-1
transcript_generation: generation-1
audit_status: passed
information_nature: 概念
classification_confidence: 0.9
evidence_ids: [ev-1]
source_refs: [doc-1]
core_content: 这是一个解释目标方法的概念。
structure_fields:
  definition: 这是定义
  components: 这是组成
relations:
  - relation_id: relation-1
    relation_type: explains
    target_object_id: ko-target
    target_wiki_page_id: page-target
    evidence_ids: [ev-1]
    time_range: 00:00-00:10
    confidence: 0.9
---

# 来源概念

## 一句话概述

这是一个解释目标方法的概念。`
	args, err := json.Marshal(map[string]any{
		"slug":      "concept/source",
		"title":     "来源概念",
		"summary":   "这是一个解释目标方法的概念。",
		"content":   sourceContent,
		"page_type": "index",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("valid relation write failed: result=%+v err=%v", result, err)
	}
	if service.createCount != 1 || service.updateCount != 0 {
		t.Fatalf("valid page persistence = creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageReturnsPersistedWikiPageID(t *testing.T) {
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
	result, err := tool.Execute(context.Background(), json.RawMessage(
		`{"slug":"concept/new","title":"New","summary":"Summary","content":"Content","page_type":"concept"}`,
	))
	if err != nil || result == nil || !result.Success {
		t.Fatalf("write failed: result=%+v err=%v", result, err)
	}
	if got := result.Data["wiki_page_id"]; got != "created-page-id" {
		t.Fatalf("wiki_page_id = %#v, want created-page-id", got)
	}
	if !strings.Contains(result.Output, "Wiki page ID: created-page-id") {
		t.Fatalf("tool output must expose the persisted page ID: %q", result.Output)
	}
}

func TestWikiWritePageReusesSemanticIdentityInsteadOfCreatingDecoratedDuplicate(t *testing.T) {
	canonical := semanticEntityWikiPage(
		"canonical-page", "entity/deepseek", "DeepSeek", "deepseek-canonical",
		"DeepSeek 是可读写 Obsidian 本地文件并调用工具的 AI 助手。",
	)
	service := &sourceRefWikiService{semanticPages: []*types.WikiPage{canonical}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	duplicate := semanticEntityWikiPage(
		"", "entity/deepseek-v2", "DeepSeek（实体）", "deepseek-generated",
		"视频中的 DeepSeek 是能够读写本地文件、调用工具的 AI Agent 配套工具。",
	)
	args, err := json.Marshal(map[string]any{
		"slug": duplicate.Slug, "title": duplicate.Title, "summary": duplicate.Summary,
		"content": duplicate.Content, "page_type": duplicate.PageType,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("semantic reuse failed: result=%+v err=%v", result, err)
	}
	if service.createCount != 0 || service.updateCount != 1 {
		t.Fatalf("semantic reuse persistence = creates:%d updates:%d", service.createCount, service.updateCount)
	}
	if result.Data["wiki_page_id"] != canonical.ID || result.Data["slug"] != canonical.Slug {
		t.Fatalf("semantic reuse identity = %#v", result.Data)
	}
	if !strings.Contains(service.page.Content, "knowledge_object_id: deepseek-canonical") || strings.Contains(service.page.Content, "deepseek-generated") {
		t.Fatalf("semantic reuse did not preserve canonical object ID:\n%s", service.page.Content)
	}
	if service.page.Title != "DeepSeek" || !strings.Contains(service.page.Content, "# DeepSeek\n") {
		t.Fatalf("semantic reuse did not preserve canonical title: title=%q\n%s", service.page.Title, service.page.Content)
	}
}

func TestWikiWritePageNormalizesDecoratedTitleBeforeFirstPersistence(t *testing.T) {
	service := &sourceRefWikiService{}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
	incoming := semanticEntityWikiPage(
		"", "entity/codex-entity", "Codex（实体）", "codex-generated",
		"Codex 是能够读写本地文件并调用工具的 AI 助手。",
	)
	args, err := json.Marshal(map[string]any{
		"slug": incoming.Slug, "title": incoming.Title, "summary": incoming.Summary,
		"content": incoming.Content, "page_type": incoming.PageType,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("decorated title normalization failed: result=%+v err=%v", result, err)
	}
	if service.createCount != 1 || service.updateCount != 0 {
		t.Fatalf("first normalized persistence = creates:%d updates:%d", service.createCount, service.updateCount)
	}
	if service.page.Title != "Codex" || result.Data["title"] != "Codex" {
		t.Fatalf("canonical title was not persisted: page=%q data=%#v", service.page.Title, result.Data)
	}
	for _, forbidden := range []string{"title: Codex（实体）", "canonical_name: Codex（实体）", "# Codex（实体）"} {
		if strings.Contains(service.page.Content, forbidden) {
			t.Fatalf("type decoration leaked through %q:\n%s", forbidden, service.page.Content)
		}
	}
}

func TestWikiWritePageSharesCanonicalIdentityAcrossVideosWithoutOverwritingEvidencePage(t *testing.T) {
	canonical := semanticEntityWikiPage(
		"canonical-page-id", "entity/deepseek", "DeepSeek", "deepseek-canonical",
		"DeepSeek 是可读写本地文件并调用工具的 AI 助手。",
	)
	service := &sourceRefWikiService{semanticPages: []*types.WikiPage{canonical}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	incoming := strings.NewReplacer(
		"source_video_id: video-1", "source_video_id: video-2",
		"source_document_id: doc-1", "source_document_id: doc-2",
		"transcript_generation: generation-1", "transcript_generation: generation-2",
		"evidence_ids: [ev-1]", "evidence_ids: [ev-2]",
		"source_refs: [doc-1]", "source_refs: [doc-2]",
	).Replace(semanticEntityWikiPage(
		"", "entity/deepseek-video-2", "DeepSeek（实体）", "deepseek-generated",
		"视频中的 DeepSeek 是能够读写本地文件、调用工具的 AI Agent 配套工具。",
	).Content)
	args, err := json.Marshal(map[string]any{
		"slug": "entity/deepseek-video-2", "title": "DeepSeek（实体）",
		"summary": "第二个视频中的 DeepSeek 证据。", "content": incoming, "page_type": "index",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContextFor("video-2", "generation-2"), args)
	if err != nil || result == nil || !result.Success {
		t.Fatalf("cross-video semantic write failed: result=%+v err=%v", result, err)
	}
	if service.createCount != 0 || service.updateCount != 1 {
		t.Fatalf("cross-video canonical persistence = creates:%d updates:%d", service.createCount, service.updateCount)
	}
	if service.page.Slug != canonical.Slug || service.page.ID != canonical.ID {
		t.Fatalf("cross-video canonical page was not reused: %#v", service.page)
	}
	for _, required := range []string{
		"knowledge_object_id: deepseek-canonical", "title: DeepSeek", "source_video_id: video-1",
		"transcript_generation: generation-1", "video_id: video-2", "transcript_generation: generation-2", "ev-2", "doc-2",
	} {
		if !strings.Contains(service.page.Content, required) {
			t.Fatalf("canonical cross-video content missing %q:\n%s", required, service.page.Content)
		}
	}
	if strings.Contains(service.page.Content, "deepseek-generated") || strings.Contains(service.page.Content, "# DeepSeek（实体）") {
		t.Fatalf("provisional identity leaked into cross-video occurrence:\n%s", service.page.Content)
	}
}

func TestWikiWritePageFailsClosedWhenSemanticAdapterIsUncertain(t *testing.T) {
	canonical := semanticEntityWikiPage(
		"canonical-page", "entity/deepseek", "DeepSeek", "deepseek-canonical",
		"DeepSeek 是可读写本地文件并调用工具的 AI 助手。",
	)
	service := &sourceRefWikiService{semanticPages: []*types.WikiPage{canonical}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(uncertainSemanticAdapter{})
	incoming := semanticEntityWikiPage(
		"", "entity/deepseek-v2", "DeepSeek（实体）", "deepseek-generated",
		"视频中的 DeepSeek 能够读写本地文件并调用工具。",
	)
	args, err := json.Marshal(map[string]any{
		"slug": incoming.Slug, "title": incoming.Title, "summary": incoming.Summary,
		"content": incoming.Content, "page_type": incoming.PageType,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "uncertain") {
		t.Fatalf("uncertain semantic result must fail closed: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("uncertain semantic result reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsDifferentObjectAtExistingSlug(t *testing.T) {
	canonical := semanticEntityWikiPage(
		"canonical-page", "entity/atlas", "Atlas", "atlas-database",
		"Atlas 是一个数据库产品。",
	)
	service := &sourceRefWikiService{page: canonical}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(fixedSemanticAdapter{assessment: customknowledge.SemanticIdentityAssessment{
			Decision: "different_object", Confidence: 0.97, Reason: "同名但分别指向数据库产品和机器人系统",
		}})
	incoming := semanticEntityWikiPage(
		"", "entity/atlas", "Atlas", "atlas-robot",
		"Atlas 是一套人形机器人系统。",
	)
	args, err := json.Marshal(map[string]any{
		"slug": incoming.Slug, "title": incoming.Title, "summary": incoming.Summary,
		"content": incoming.Content, "page_type": incoming.PageType,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "different knowledge object") {
		t.Fatalf("different object at occupied slug must be rejected: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("slug collision reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageFailsClosedWhenSemanticAdapterIsMissing(t *testing.T) {
	canonical := semanticEntityWikiPage(
		"canonical-page", "entity/deepseek", "DeepSeek", "deepseek-canonical",
		"DeepSeek 是可读写本地文件并调用工具的 AI 助手。",
	)
	service := &sourceRefWikiService{semanticPages: []*types.WikiPage{canonical}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
	incoming := semanticEntityWikiPage(
		"", "entity/deepseek-v2", "DeepSeek", "deepseek-generated",
		"视频中的 DeepSeek 能够读写本地文件并调用工具。",
	)
	args, err := json.Marshal(map[string]any{
		"slug": incoming.Slug, "title": incoming.Title, "summary": incoming.Summary,
		"content": incoming.Content, "page_type": incoming.PageType,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "not configured") {
		t.Fatalf("missing semantic adapter must fail closed: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("missing semantic adapter reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsSameNameDifferentType(t *testing.T) {
	canonical := semanticEntityWikiPage("canonical-page", "entity/deepseek", "DeepSeek", "entity-id", "DeepSeek 是一款 AI 助手。")
	concept := strings.NewReplacer(
		"type: entity", "type: concept", "primary_type: entity", "primary_type: concept",
		"entity_sub_type: product\n", "", "information_nature: 产品", "information_nature: 概念",
	).Replace(canonical.Content)
	concept = strings.ReplaceAll(concept, "structure_fields:\n  product_type: AI 编程与工具调用助手\n  target_users: 个人知识库用户\n  core_function: 读写本地 Markdown 文件并调用工具\n  tech_basis: 大语言模型和工具调用\n  differentiation: 可直接操作本地文件", "structure_fields:\n  definition: 一个概念定义\n  components: 由多个部分构成\n  mechanism: 通过机制运行")
	canonical.Content = concept
	service := &sourceRefWikiService{semanticPages: []*types.WikiPage{canonical}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	incoming := semanticEntityWikiPage("", "entity/deepseek-new", "DeepSeek", "generated", "DeepSeek 是一款 AI 助手。")
	args, _ := json.Marshal(map[string]any{"slug": incoming.Slug, "title": incoming.Title, "summary": incoming.Summary, "content": incoming.Content, "page_type": incoming.PageType})
	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "uncertain") {
		t.Fatalf("same-name type conflict must fail closed: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("type conflict reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsMultipleCanonicalMatches(t *testing.T) {
	first := semanticEntityWikiPage("page-1", "entity/deepseek-a", "DeepSeek", "object-a", "DeepSeek 是一款 AI 助手。")
	second := semanticEntityWikiPage("page-2", "entity/deepseek-b", "DeepSeek", "object-b", "DeepSeek 是一款 AI 助手。")
	service := &sourceRefWikiService{semanticPages: []*types.WikiPage{first, second}}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver()).
		WithSemanticIdentityAdapter(customknowledge.RuleSemanticIdentityAdapter{})
	incoming := semanticEntityWikiPage("", "entity/deepseek-new", "DeepSeek", "generated", "DeepSeek 是一款 AI 助手。")
	args, _ := json.Marshal(map[string]any{"slug": incoming.Slug, "title": incoming.Title, "summary": incoming.Summary, "content": incoming.Content, "page_type": incoming.PageType})
	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, "multiple canonical object IDs") {
		t.Fatalf("multiple canonical matches must be rejected: %+v", result)
	}
	if service.createCount != 0 || service.updateCount != 0 {
		t.Fatalf("multiple canonical matches reached persistence: creates=%d updates=%d", service.createCount, service.updateCount)
	}
}

func TestWikiWritePageRejectsDowngradingExistingV2Page(t *testing.T) {
	service := &sourceRefWikiService{page: semanticEntityWikiPage(
		"object-1", "concept/test", "测试概念", "object-1", "这是一个测试概念。",
	)}
	tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
	args, err := json.Marshal(map[string]any{
		"slug": "concept/test", "title": "普通页面", "summary": "试图移除 V2 身份",
		"content": "# 普通页面\n\n不再包含知识对象元数据。", "page_type": "concept", "source_refs": []string{"doc-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(v2WriteContext(), args)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Success || !strings.Contains(result.Error, protectedKnowledgeV2MutationError) {
		t.Fatalf("existing V2 page downgrade must be rejected: %+v", result)
	}
	if service.updateCount != 0 {
		t.Fatalf("existing V2 page downgrade reached persistence: updates=%d", service.updateCount)
	}
}

func TestWikiWritePageRejectsRemovingExistingV2ContractFields(t *testing.T) {
	base := semanticEntityWikiPage("object-1", "entity/test", "测试实体", "object-1", "这是一个测试实体。")
	for _, test := range []struct {
		name    string
		content string
	}{
		{name: "remove object identity", content: strings.Replace(base.Content, "knowledge_object_id: object-1\n", "", 1)},
		{name: "remove business type", content: strings.NewReplacer("type: entity\n", "", "primary_type: entity\n", "").Replace(base.Content)},
		{name: "remove core contract field", content: strings.Replace(base.Content, "core_content: 这是一个测试实体。\n", "", 1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			pageCopy := *base
			service := &sourceRefWikiService{page: &pageCopy}
			tool := NewWikiWritePageTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
			args, err := json.Marshal(map[string]any{
				"slug": base.Slug, "title": base.Title, "summary": base.Summary,
				"content": test.content, "page_type": "index", "source_refs": []string{"doc-1"},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := tool.Execute(v2WriteContext(), args)
			if err != nil {
				t.Fatal(err)
			}
			if result == nil || result.Success {
				t.Fatalf("removing V2 contract fields must be rejected: %+v", result)
			}
			if service.updateCount != 0 {
				t.Fatalf("invalid V2 contract reached persistence: updates=%d", service.updateCount)
			}
		})
	}
}

func semanticEntityWikiPage(id, slug, title, objectID, core string) *types.WikiPage {
	content := fmt.Sprintf(`---
knowledge_object_id: %s
type: entity
primary_type: entity
entity_sub_type: product
title: %s
canonical_name: %s
source_video_id: video-1
source_document_id: doc-1
transcript_generation: generation-1
audit_status: passed
information_nature: 产品
classification_confidence: 0.9
evidence_ids: [ev-1]
source_refs: [doc-1]
core_content: %s
structure_fields:
  product_type: AI 编程与工具调用助手
  target_users: 个人知识库用户
  core_function: 读写本地 Markdown 文件并调用工具
  tech_basis: 大语言模型和工具调用
  differentiation: 可直接操作本地文件
relations: []
---

# %s

## 一句话概述

%s`, objectID, title, title, core, title, core)
	return &types.WikiPage{
		ID: id, KnowledgeBaseID: "kb-1", Slug: slug, Title: title, Summary: core,
		PageType: "index", Status: types.WikiPageStatusPublished, Content: content,
	}
}
