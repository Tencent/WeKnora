package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type replaceTextWikiService struct {
	interfaces.WikiPageService
	page        *types.WikiPage
	updateCalls int
}

func (s *replaceTextWikiService) GetPageBySlug(_ context.Context, _, _ string) (*types.WikiPage, error) {
	pageCopy := *s.page
	return &pageCopy, nil
}

func (s *replaceTextWikiService) UpdatePage(_ context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	s.updateCalls++
	pageCopy := *page
	s.page = &pageCopy
	return &pageCopy, nil
}

func executeReplaceText(t *testing.T, content, oldText, newText string) (*replaceTextWikiService, *types.ToolResult) {
	t.Helper()
	service := &replaceTextWikiService{page: &types.WikiPage{
		KnowledgeBaseID: "kb-1",
		Slug:            "concept/repeated",
		Title:           "Repeated",
		Content:         content,
	}}
	tool := NewWikiReplaceTextTool(service, []string{"kb-1"}, nil, NewWikiRouteResolver())
	args, err := json.Marshal(map[string]string{
		"slug":     "concept/repeated",
		"old_text": oldText,
		"new_text": newText,
	})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result == nil {
		t.Fatal("Execute returned nil result")
	}
	return service, result
}

func TestWikiReplaceTextReplacesAllExactMatches(t *testing.T) {
	service, result := executeReplaceText(
		t,
		"alpha target beta target gamma target",
		"target",
		"replacement",
	)

	if !result.Success {
		t.Fatalf("replace failed: %+v", result)
	}
	const want = "alpha replacement beta replacement gamma replacement"
	if service.page.Content != want {
		t.Fatalf("content = %q, want %q", service.page.Content, want)
	}
	if service.updateCalls != 1 {
		t.Fatalf("UpdatePage calls = %d, want 1", service.updateCalls)
	}
	if got := result.Data["replacement_count"]; got != 3 {
		t.Fatalf("replacement_count = %#v, want 3", got)
	}
}

func TestWikiReplaceTextExactMatchCases(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		oldText   string
		newText   string
		want      string
		wantCount int
	}{
		{
			name: "single match remains compatible", content: "before target after",
			oldText: "target", newText: "replacement", want: "before replacement after", wantCount: 1,
		},
		{
			name: "new text containing old text is not replaced recursively", content: "a a",
			oldText: "a", newText: "aa", want: "aa aa", wantCount: 2,
		},
		{
			name: "empty new text deletes every match", content: "keep-X-keep-X",
			oldText: "X", newText: "", want: "keep--keep-", wantCount: 2,
		},
		{
			name: "unicode exact matches", content: "旧文本与旧文本",
			oldText: "旧文本", newText: "新文本", want: "新文本与新文本", wantCount: 2,
		},
		{
			name: "markdown links remain plain exact text", content: "See [[concept/old]] and [[concept/old]].",
			oldText: "[[concept/old]]", newText: "[[concept/new]]", want: "See [[concept/new]] and [[concept/new]].", wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, result := executeReplaceText(t, tt.content, tt.oldText, tt.newText)
			if !result.Success {
				t.Fatalf("replace failed: %+v", result)
			}
			if service.page.Content != tt.want {
				t.Fatalf("content = %q, want %q", service.page.Content, tt.want)
			}
			if result.Data["replacement_count"] != tt.wantCount {
				t.Fatalf("replacement_count = %#v, want %d", result.Data["replacement_count"], tt.wantCount)
			}
			if service.updateCalls != 1 {
				t.Fatalf("UpdatePage calls = %d, want 1", service.updateCalls)
			}
		})
	}
}

func TestWikiReplaceTextNotFoundDoesNotPersist(t *testing.T) {
	service, result := executeReplaceText(t, "keep this content", "missing", "replacement")

	if result.Success {
		t.Fatalf("expected failure, got %+v", result)
	}
	if !strings.Contains(result.Error, "old_text not found") {
		t.Fatalf("error = %q, want old_text not found", result.Error)
	}
	if service.page.Content != "keep this content" {
		t.Fatalf("stored content changed to %q", service.page.Content)
	}
	if service.updateCalls != 0 {
		t.Fatalf("UpdatePage calls = %d, want 0", service.updateCalls)
	}
}
