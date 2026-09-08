package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type v2MutationGuardService struct {
	interfaces.WikiPageService
	page        *types.WikiPage
	updateCalls int
	createCalls int
	deleteCalls int
}

func (s *v2MutationGuardService) GetPageBySlug(context.Context, string, string) (*types.WikiPage, error) {
	copy := *s.page
	return &copy, nil
}

func (s *v2MutationGuardService) UpdatePage(context.Context, *types.WikiPage) (*types.WikiPage, error) {
	s.updateCalls++
	return s.page, nil
}

func (s *v2MutationGuardService) CreatePage(context.Context, *types.WikiPage) (*types.WikiPage, error) {
	s.createCalls++
	return s.page, nil
}

func (s *v2MutationGuardService) DeletePage(context.Context, string, string) error {
	s.deleteCalls++
	return nil
}

func TestWikiReplaceTextCannotMutateV2Identity(t *testing.T) {
	const content = `---
knowledge_object_id: object-1
type: concept
source_video_id: video-1
source_document_id: source-1
transcript_generation: generation-1
---

# 测试概念`
	for _, test := range []struct {
		name    string
		oldText string
		newText string
	}{
		{name: "remove full frontmatter", oldText: strings.Split(content, "# 测试概念")[0], newText: ""},
		{name: "remove identity field", oldText: "knowledge_object_id: object-1\n", newText: ""},
		{name: "change source identity", oldText: "source_video_id: video-1", newText: "source_video_id: video-2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &v2MutationGuardService{page: &types.WikiPage{KnowledgeBaseID: "kb-1", Slug: "concept/test", Content: content}}
			tool := NewWikiReplaceTextTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
			result := executeV2Mutation(t, tool, map[string]string{
				"slug": "concept/test", "old_text": test.oldText, "new_text": test.newText,
			})
			assertV2MutationBlocked(t, service, result)
		})
	}
}

func TestWikiRenameAndDeleteCannotChangeV2PageIdentity(t *testing.T) {
	const content = "---\nknowledge_object_id: object-1\ntype: concept\nsource_video_id: video-1\ntranscript_generation: generation-1\n---\n"
	for _, test := range []struct {
		name string
		tool func(*v2MutationGuardService) types.Tool
		args map[string]string
	}{
		{name: "rename", tool: func(service *v2MutationGuardService) types.Tool {
			return NewWikiRenamePageTool(service, []string{"kb-1"}, NewWikiRouteResolver())
		}, args: map[string]string{"slug": "concept/test", "new_slug": "concept/renamed"}},
		{name: "delete", tool: func(service *v2MutationGuardService) types.Tool {
			return NewWikiDeletePageTool(service, []string{"kb-1"}, NewWikiRouteResolver())
		}, args: map[string]string{"slug": "concept/test"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &v2MutationGuardService{page: &types.WikiPage{KnowledgeBaseID: "kb-1", Slug: "concept/test", Content: content}}
			result := executeV2Mutation(t, test.tool(service), test.args)
			assertV2MutationBlocked(t, service, result)
		})
	}
}

func TestWikiMutationToolsCannotChangeVideoIndex(t *testing.T) {
	for _, test := range []struct {
		name string
		tool func(*v2MutationGuardService) types.Tool
		args map[string]string
	}{
		{name: "replace", tool: func(service *v2MutationGuardService) types.Tool {
			return NewWikiReplaceTextTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
		}, args: map[string]string{"slug": "video/video-1", "old_text": "index", "new_text": "changed"}},
		{name: "rename", tool: func(service *v2MutationGuardService) types.Tool {
			return NewWikiRenamePageTool(service, []string{"kb-1"}, NewWikiRouteResolver())
		}, args: map[string]string{"slug": "video/video-1", "new_slug": "video/video-2"}},
		{name: "delete", tool: func(service *v2MutationGuardService) types.Tool {
			return NewWikiDeletePageTool(service, []string{"kb-1"}, NewWikiRouteResolver())
		}, args: map[string]string{"slug": "video/video-1"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &v2MutationGuardService{page: &types.WikiPage{KnowledgeBaseID: "kb-1", Slug: "video/video-1", Content: "index"}}
			result := executeV2Mutation(t, test.tool(service), test.args)
			assertV2MutationBlocked(t, service, result)
		})
	}
}

func executeV2Mutation(t *testing.T, tool types.Tool, args map[string]string) *types.ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertV2MutationBlocked(t *testing.T, service *v2MutationGuardService, result *types.ToolResult) {
	t.Helper()
	if result == nil || result.Success || !strings.Contains(result.Error, protectedKnowledgeV2MutationError) {
		t.Fatalf("V2 mutation must be rejected: %+v", result)
	}
	if service.updateCalls != 0 || service.createCalls != 0 || service.deleteCalls != 0 {
		t.Fatalf("V2 mutation reached persistence: updates=%d creates=%d deletes=%d", service.updateCalls, service.createCalls, service.deleteCalls)
	}
}
