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
			"Read the details of a specific wiki page issue or list pending issues for a wiki page.",
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "issue_id": {
      "type": "string",
      "description": "Optional: The ID of a specific issue to read."
    },
    "slug": {
      "type": "string",
      "description": "Optional: The slug of the wiki page to list pending issues for."
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
		IssueID string `json:"issue_id"`
		Slug    string `json:"slug"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	issueID := strings.TrimSpace(params.IssueID)
	slug := strings.TrimSpace(params.Slug)

	if issueID == "" && slug == "" {
		return &types.ToolResult{Success: false, Error: "Either issue_id or slug is required"}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available"}, nil
	}

	kbID := t.kbIDs[0]

	if issueID != "" {
		// Just reuse ListIssues since there's no GetIssueByID yet
		issues, err := t.wikiService.ListIssues(ctx, kbID, "", "")
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to list issues: " + err.Error()}, nil
		}
		
		for _, issue := range issues {
			if issue.ID == issueID {
				out, _ := json.MarshalIndent(issue, "", "  ")
				return &types.ToolResult{Success: true, Output: string(out)}, nil
			}
		}
		return &types.ToolResult{Success: false, Error: "Issue not found"}, nil
	}

	issues, err := t.wikiService.ListIssues(ctx, kbID, slug, "pending")
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to list issues: " + err.Error()}, nil
	}

	if len(issues) == 0 {
		return &types.ToolResult{Success: true, Output: "No pending issues found for slug: " + slug}, nil
	}

	out, _ := json.MarshalIndent(issues, "", "  ")
	return &types.ToolResult{Success: true, Output: string(out)}, nil
}
