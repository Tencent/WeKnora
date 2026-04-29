package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiUpdateIssueTool struct {
	BaseTool
	wikiService interfaces.WikiPageService
	kbIDs       []string
}

func NewWikiUpdateIssueTool(wikiService interfaces.WikiPageService, kbIDs []string) types.Tool {
	return &wikiUpdateIssueTool{
		BaseTool: NewBaseTool(
			ToolWikiUpdateIssue,
			"Update the status of a specific wiki page issue (e.g., set it to 'resolved' or 'ignored').",
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "issue_id": {
      "type": "string",
      "description": "The ID of the issue to update."
    },
    "status": {
      "type": "string",
      "enum": ["resolved", "ignored", "pending"],
      "description": "The new status for the issue."
    }
  },
  "required": ["issue_id", "status"]
}`),
		),
		wikiService: wikiService,
		kbIDs:       kbIDs,
	}
}

func (t *wikiUpdateIssueTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		IssueID string `json:"issue_id"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	if params.IssueID == "" {
		return &types.ToolResult{Success: false, Error: "issue_id is required"}, nil
	}
	if params.Status == "" {
		return &types.ToolResult{Success: false, Error: "status is required"}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available"}, nil
	}

	// Update issue status
	err := t.wikiService.UpdateIssueStatus(ctx, params.IssueID, params.Status)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to update issue status: " + err.Error()}, nil
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Successfully updated issue %s to status '%s'", params.IssueID, params.Status),
	}, nil
}
