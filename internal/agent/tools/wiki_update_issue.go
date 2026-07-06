package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Tencent/WeKnora/internal/application/repository"
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
    "knowledge_base_id": {
      "type": "string",
      "description": "The knowledge base ID the issue belongs to. If not provided, uses the first available wiki KB."
    },
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
		IssueID         string `json:"issue_id"`
		Status          string `json:"status"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
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

	var kbID string
	if params.KnowledgeBaseID != "" {
		if !containsString(t.kbIDs, params.KnowledgeBaseID) {
			return &types.ToolResult{Success: false, Error: "knowledge_base_id not in allowed scope"}, nil
		}
		kbID = params.KnowledgeBaseID
	} else if len(t.kbIDs) > 0 {
		kbID = t.kbIDs[0]
	} else {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available"}, nil
	}

	// Update issue status. kbID is applied server-side as a scope guard so an
	// out-of-scope issue_id cannot be mutated even when the agent knows an
	// in-scope kbID. ErrWikiIssueNotFound covers both "no such issue" and
	// "issue exists but belongs to a different KB".
	err := t.wikiService.UpdateIssueStatus(ctx, kbID, params.IssueID, params.Status)
	if err != nil {
		if errors.Is(err, repository.ErrWikiIssueNotFound) {
			return &types.ToolResult{Success: false, Error: fmt.Sprintf("Issue %s not found in knowledge base %s", params.IssueID, kbID)}, nil
		}
		return &types.ToolResult{Success: false, Error: "Failed to update issue status: " + err.Error()}, nil
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Successfully updated issue %s to status '%s'", params.IssueID, params.Status),
	}, nil
}
