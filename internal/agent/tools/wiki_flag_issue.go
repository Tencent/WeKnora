package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiFlagIssueTool struct {
	BaseTool
	wikiService interfaces.WikiPageService
	kbIDs       []string
}

func NewWikiFlagIssueTool(wikiService interfaces.WikiPageService, kbIDs []string) types.Tool {
	return &wikiFlagIssueTool{
		BaseTool: NewBaseTool(
			ToolWikiFlagIssue,
			`Flag a wiki page that contains errors, mixed entities, or outdated information.
Use this tool when you or the user identifies that a wiki page is factually incorrect or wrongly merged (e.g., a page contains information about two different products).
This will log an issue for human review or automated maintenance.`,
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "slug": {
      "type": "string",
      "description": "The slug of the wiki page that has an issue (e.g. 'entity/hunyuan-damoxing')"
    },
    "issue_type": {
      "type": "string",
      "enum": ["mixed_entities", "contradictory_facts", "out_of_date", "other"],
      "description": "The category of the issue"
    },
    "description": {
      "type": "string",
      "description": "A detailed explanation of what is wrong with the page and what should be fixed."
    },
    "suspected_knowledge_ids": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional list of knowledge_ids (from the <sources> block) that you suspect are causing the pollution or error."
    }
  },
  "required": ["slug", "issue_type", "description"]
}`),
		),
		wikiService: wikiService,
		kbIDs:       kbIDs,
	}
}

func (t *wikiFlagIssueTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug                  string   `json:"slug"`
		IssueType             string   `json:"issue_type"`
		Description           string   `json:"description"`
		SuspectedKnowledgeIDs []string `json:"suspected_knowledge_ids"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	slug := strings.TrimSpace(params.Slug)
	if slug == "" {
		return &types.ToolResult{Success: false, Error: "slug is required"}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for issue tracking"}, nil
	}
	
	// Default to first KB ID if multiple (normally there's only one in this context)
	kbID := t.kbIDs[0]

	// Verify the page exists
	page, err := t.wikiService.GetPageBySlug(ctx, kbID, slug)
	if err != nil || page == nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Wiki page with slug '%s' not found", slug)}, nil
	}

	issue := &types.WikiPageIssue{
		TenantID:              page.TenantID,
		KnowledgeBaseID:       kbID,
		Slug:                  slug,
		IssueType:             params.IssueType,
		Description:           params.Description,
		SuspectedKnowledgeIDs: params.SuspectedKnowledgeIDs,
		ReportedBy:            "wiki-researcher-agent",
		Status:                "pending",
	}

	_, err = t.wikiService.CreateIssue(ctx, issue)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to create issue: " + err.Error()}, nil
	}

	return &types.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("Successfully flagged issue for %s. A maintenance ticket has been created for review.", slug),
	}, nil
}