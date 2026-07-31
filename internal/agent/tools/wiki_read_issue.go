package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiReadIssueTool struct {
	BaseTool
	wikiService interfaces.WikiPageService
	kbIDs       []string
}

func NewWikiReadIssueTool(wikiService interfaces.WikiPageService, kbIDs []string) types.Tool {
	return &wikiReadIssueTool{
		BaseTool: NewBaseTool(
			ToolWikiReadIssue,
			"Read the details of a specific wiki page issue or list actionable issues for a wiki page.",
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "issue_id": {
      "type": "string",
      "description": "Optional: The short iN ID of a specific issue from an earlier wiki_read_issue result."
    },
	"issue_ids": {
	  "type": "array",
	  "items": {"type": "string"},
	  "description": "Optional list of short iN issue IDs to read in one call."
	},
    "slug": {
      "type": "string",
      "description": "Optional: The slug of the wiki page to list actionable issues for."
    }
  },
  "description": "Provide either issue_id or slug to read issue(s)."
}`),
		),
		wikiService: wikiService,
		kbIDs:       kbIDs,
	}
}

func (t *wikiReadIssueTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		IssueID  string   `json:"issue_id"`
		IssueIDs []string `json:"issue_ids"`
		Slug     string   `json:"slug"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	issueID := strings.TrimSpace(params.IssueID)
	issueIDs := append([]string(nil), params.IssueIDs...)
	if issueID != "" {
		issueIDs = append(issueIDs, issueID)
	}
	slug := strings.TrimSpace(params.Slug)

	if len(issueIDs) == 0 && slug == "" {
		return &types.ToolResult{Success: false, Error: "Either issue_id or slug is required"}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available"}, nil
	}

	if len(issueIDs) > 0 {
		resolved := make([]*types.WikiPageIssue, 0, len(issueIDs))
		for _, id := range dedupNonEmptyStrings(issueIDs) {
			issue, err := resolveWikiIssue(ctx, t.wikiService, id, t.kbIDs)
			if err != nil {
				return &types.ToolResult{Success: false, Error: err.Error()}, nil
			}
			resolved = append(resolved, issue)
		}
		var payload interface{} = resolved
		if len(resolved) == 1 {
			payload = resolved[0]
		}
		out, _ := json.MarshalIndent(payload, "", "  ")
		return &types.ToolResult{Success: true, Output: string(out)}, nil
	}

	var issues []*types.WikiPageIssue
	for _, kbID := range dedupNonEmptyStrings(t.kbIDs) {
		kbIssues, err := t.wikiService.ListIssues(ctx, kbID, slug, "actionable")
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to list issues: " + err.Error()}, nil
		}
		for _, issue := range kbIssues {
			if issue != nil && issue.KnowledgeBaseID != "" && issue.KnowledgeBaseID != kbID {
				return &types.ToolResult{
					Success: false,
					Error: "Issue result returned knowledge base " + issue.KnowledgeBaseID +
						" while resolving allowed scope " + kbID,
				}, nil
			}
		}
		issues = append(issues, kbIssues...)
	}

	if len(issues) == 0 {
		return &types.ToolResult{Success: true, Output: "No actionable issues found for slug: " + slug}, nil
	}

	out, _ := json.MarshalIndent(issues, "", "  ")
	return &types.ToolResult{Success: true, Output: string(out)}, nil
}
