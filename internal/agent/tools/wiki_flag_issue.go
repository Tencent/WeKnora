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
	wikiService      interfaces.WikiPageService
	kbIDs            []string
	routes           *WikiRouteResolver
	knowledgeService interfaces.KnowledgeService
	searchTargets    types.SearchTargets
	scopeEnforced    bool
}

func NewWikiFlagIssueTool(
	wikiService interfaces.WikiPageService,
	kbIDs []string,
	routes ...*WikiRouteResolver,
) *wikiFlagIssueTool {
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
      "description": "Optional list of short dN document IDs from the <sources> block that you suspect are causing the pollution or error."
    }
  },
  "required": ["slug", "issue_type", "description"]
}`),
		),
		wikiService: wikiService,
		kbIDs:       kbIDs,
		routes:      firstWikiRoute(routes),
	}
}

func (t *wikiFlagIssueTool) WithKnowledgeScope(
	knowledgeService interfaces.KnowledgeService,
	searchTargets types.SearchTargets,
) *wikiFlagIssueTool {
	t.knowledgeService = knowledgeService
	t.searchTargets = searchTargets
	t.scopeEnforced = true
	return t
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
	normalizedSlug, slugErr := normalizeAndValidateWikiSlug(slug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	slug = normalizedSlug

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for issue tracking"}, nil
	}

	page, kbID, err := resolveUniqueWikiPage(ctx, t.wikiService, slug, t.kbIDs, t.routes)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	suspectedKnowledgeIDs := params.SuspectedKnowledgeIDs
	if t.scopeEnforced && len(suspectedKnowledgeIDs) > 0 {
		resolved, scopeErr := resolveAuthorizedSourceRefs(
			ctx, t.searchTargets, suspectedKnowledgeIDs, t.knowledgeService,
		)
		if scopeErr != nil {
			return &types.ToolResult{Success: false, Error: "Invalid suspected_knowledge_ids: " + scopeErr.Error()}, nil
		}
		suspectedKnowledgeIDs = make([]string, 0, len(resolved))
		for _, ref := range resolved {
			suspectedKnowledgeIDs = append(suspectedKnowledgeIDs, strings.SplitN(ref, "|", 2)[0])
		}
	}

	issue := &types.WikiPageIssue{
		TenantID:              page.TenantID,
		KnowledgeBaseID:       kbID,
		PageID:                page.ID,
		Slug:                  slug,
		IssueType:             params.IssueType,
		Description:           params.Description,
		SuspectedKnowledgeIDs: suspectedKnowledgeIDs,
		Severity:              "warning",
		Source:                types.WikiIssueSourceAgent,
		RepairMode:            types.WikiIssueRepairAgent,
		DetectedPageVersion:   page.Version,
		ReportedBy:            "wiki-researcher-agent",
		Status:                types.WikiIssueStatusOpen,
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
