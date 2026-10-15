package tools

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WithScope checks both the proposed and approved arguments before a direct Wiki write.
func (t *ConfirmedTool) WithScope(scope operations.Scope, wiki interfaces.WikiPageService) *ConfirmedTool {
	t.scopeCheck = func(ctx context.Context, args json.RawMessage) *types.ToolResult {
		if scope.Level.WithDefault() == types.AssistantScopeWorkspace {
			return nil
		}
		if !scope.Level.Valid() || scope.KnowledgeBaseID == "" {
			return scopeFailure(ctx, t.Name(), scope)
		}
		if scope.Level == types.AssistantScopeKnowledgeBase {
			return nil
		}
		if scope.AgentID != "" || scope.KnowledgeID != "" {
			return scopeFailure(ctx, t.Name(), scope)
		}
		if scope.WikiSlug == "" {
			return nil
		}
		var params struct {
			Slug    string `json:"slug"`
			IssueID string `json:"issue_id"`
		}
		if json.Unmarshal(args, &params) != nil {
			return scopeFailure(ctx, t.Name(), scope)
		}
		if t.Name() == ToolWikiUpdateIssue {
			if wiki == nil {
				return scopeFailure(ctx, t.Name(), scope)
			}
			issue, err := resolveWikiIssue(ctx, wiki, params.IssueID, []string{scope.KnowledgeBaseID})
			if err != nil {
				return scopeFailure(ctx, t.Name(), scope)
			}
			params.Slug = issue.Slug
		}
		slug, err := normalizeAndValidateWikiSlug(params.Slug)
		if err != nil || slug != scope.WikiSlug {
			return scopeFailure(ctx, t.Name(), scope)
		}
		if t.Name() == ToolWikiRenamePage || t.Name() == ToolWikiDeletePage {
			if wiki == nil {
				return scopeFailure(ctx, t.Name(), scope)
			}
			page, err := wiki.GetPageBySlug(ctx, scope.KnowledgeBaseID, slug)
			// These tools rewrite the content of every incoming link's page.
			if err != nil || page == nil || len(page.InLinks) > 0 {
				return scopeFailure(ctx, t.Name(), scope)
			}
		}
		return nil
	}
	return t
}
