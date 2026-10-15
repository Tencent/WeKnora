package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type assistantScopeWikiService struct {
	fakeWikiPageService
	issue *types.WikiPageIssue
	err   error
}

func (s *assistantScopeWikiService) ListIssues(
	context.Context, string, string, string,
) ([]*types.WikiPageIssue, error) {
	return []*types.WikiPageIssue{s.issue}, s.err
}

func TestAssistantWikiScopeBeforeApprovalAndAfterEdits(t *testing.T) {
	for _, tc := range []struct {
		name, tool, slug string
		scope            operations.Scope
		modified         json.RawMessage
		issue            *types.WikiPageIssue
		lookupErr        error
		inLinks          types.StringArray
		allowed          bool
	}{
		{name: "workspace", tool: ToolWikiWritePage, slug: "other", allowed: true},
		{name: "KB", tool: ToolWikiWritePage, slug: "other", scope: operations.Scope{
			Level: types.AssistantScopeKnowledgeBase, KnowledgeBaseID: "kb",
		}, allowed: true},
		{name: "no context", tool: ToolWikiWritePage, slug: "guide"},
		{name: "document", tool: ToolWikiWritePage, slug: "guide", scope: operations.Scope{KnowledgeID: "doc"}},
		{name: "agent", tool: ToolWikiWritePage, slug: "guide", scope: operations.Scope{AgentID: "a"}},
		{name: "page match", tool: ToolWikiWritePage, slug: "guide", allowed: true},
		{name: "page other", tool: ToolWikiWritePage, slug: "other"},
		{name: "edited other", tool: ToolWikiWritePage, slug: "guide", modified: json.RawMessage(`{"slug":"other"}`)},
		{
			name: "issue same",
			tool: ToolWikiUpdateIssue,
			issue: &types.WikiPageIssue{
				ID:              "i",
				KnowledgeBaseID: "kb",
				Slug:            "guide",
			},
			allowed: true,
		},
		{
			name: "issue other",
			tool: ToolWikiUpdateIssue,
			issue: &types.WikiPageIssue{
				ID:              "i",
				KnowledgeBaseID: "kb",
				Slug:            "other",
			},
		},
		{name: "issue lookup fails", tool: ToolWikiUpdateIssue, lookupErr: errors.New("unavailable")},
		{name: "delete incoming links", tool: ToolWikiDeletePage, slug: "guide", inLinks: types.StringArray{"other"}},
		{name: "rename incoming links", tool: ToolWikiRenamePage, slug: "guide", inLinks: types.StringArray{"other"}},
		{name: "delete only page", tool: ToolWikiDeletePage, slug: "guide", allowed: true},
		{name: "rename only page", tool: ToolWikiRenamePage, slug: "guide", allowed: true},
	} {
		for _, resumed := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: "/resume", false: "/live"}[resumed], func(t *testing.T) {
				scope := tc.scope
				if tc.name != "workspace" && scope.Level == "" {
					scope.Level = types.AssistantScopePage
					scope.KnowledgeBaseID = "kb"
					scope.WikiSlug = "guide"
				}
				if tc.name == "no context" {
					scope.KnowledgeBaseID = ""
				}
				wiki := &assistantScopeWikiService{issue: tc.issue, err: tc.lookupErr}
				wiki.pages = map[string]*types.WikiPage{
					wikiPageKey("kb", "guide"): {Slug: "guide", InLinks: tc.inLinks},
				}
				inner := &countingTool{BaseTool: NewBaseTool(tc.tool, "test", json.RawMessage(`{}`))}
				gate := &fakeApprover{decision: approval.Decision{
					Approved:     true,
					PendingID:    "p",
					ModifiedArgs: tc.modified,
				}}
				tool := NewConfirmedTool(inner, operations.RiskWrite, operations.Access{}, gate).WithScope(scope, wiki)
				args, _ := json.Marshal(map[string]any{"slug": tc.slug, "issue_id": "i", "new_slug": "new"})
				ctx := interactiveCtx()
				if resumed {
					meta, _ := ToolExecFromContext(ctx)
					meta.Answered = &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval}
				}
				result, err := tool.Execute(ctx, args)
				require.NoError(t, err)
				assert.Equal(t, tc.allowed, result.Success, result.Error)
				if tc.allowed {
					require.Len(t, inner.calls, 1)
				} else {
					assert.Empty(t, inner.calls)
					if len(tc.modified) == 0 {
						assert.Empty(t, gate.requests)
					}
				}
			})
		}
	}
}
