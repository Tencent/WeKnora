package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiWritePageTool struct {
	BaseTool
	wikiPageService  interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	kbIDs            []string
}

// NewWikiWritePageTool creates a new wiki_write_page tool
func NewWikiWritePageTool(wikiPageService interfaces.WikiPageService, kbIDs []string, knowledgeService interfaces.KnowledgeService) types.Tool {
	return &wikiWritePageTool{
		BaseTool: NewBaseTool(
			ToolWikiWritePage,
			"Create a new Wiki page or completely overwrite an existing one. Automatically handles outbound links.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {
						"type": "string",
						"description": "The slug of the Wiki page (e.g. 'entity/hunyuan-damoxing')"
					},
					"title": {
						"type": "string",
						"description": "The title of the page"
					},
					"summary": {
						"type": "string",
						"description": "A one-sentence summary for the index listing"
					},
					"content": {
						"type": "string",
						"description": "The FULL, complete Markdown content of the page. Do NOT use placeholders."
					},
					"page_type": {
						"type": "string",
						"description": "The page type, e.g., 'summary', 'entity', 'concept', 'synthesis', 'comparison'"
					},
					"aliases": {
						"type": "array",
						"items": {"type": "string"},
						"description": "A list of aliases for the page (optional)"
					},
					"source_refs": {
						"type": "array",
						"items": {"type": "string"},
						"description": "A list of source knowledge IDs (UUIDs only) that contributed to this page. If provided, these will COMPLETELY REPLACE the existing source_refs of the page."
					}
				},
				"required": ["slug", "title", "summary", "content", "page_type"]
			}`),
		),
		wikiPageService:  wikiPageService,
		knowledgeService: knowledgeService,
		kbIDs:            kbIDs,
	}
}

func (t *wikiWritePageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug       string   `json:"slug"`
		Title      string   `json:"title"`
		Summary    string   `json:"summary"`
		Content    string   `json:"content"`
		PageType   string   `json:"page_type"`
		Aliases    []string `json:"aliases"`
		SourceRefs []string `json:"source_refs"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	kbID := t.kbIDs[0]

	if params.Title == "" || params.PageType == "" || params.Content == "" || params.Summary == "" {
		return &types.ToolResult{Success: false, Error: "title, summary, content, and page_type are required for write action"}, nil
	}

	// Try to get the existing page
	existingPage, err := t.wikiPageService.GetPageBySlug(ctx, kbID, params.Slug)
	if err != nil && !errors.Is(err, repository.ErrWikiPageNotFound) {
		return &types.ToolResult{Success: false, Error: "Failed to check existing page: " + err.Error()}, nil
	}

	resolvedRefs := resolveSourceRefs(ctx, t.knowledgeService, params.SourceRefs)

	var action string
	if existingPage != nil {
		// Update
		existingPage.Title = params.Title
		existingPage.Summary = params.Summary
		existingPage.Content = params.Content
		existingPage.PageType = params.PageType
		existingPage.Aliases = params.Aliases

		if len(resolvedRefs) > 0 {
			existingPage.SourceRefs = resolvedRefs
		}

		_, err = t.wikiPageService.UpdatePage(ctx, existingPage)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to update page: " + err.Error()}, nil
		}
		action = "updated"
	} else {
		// Create
		newPage := &types.WikiPage{
			KnowledgeBaseID: kbID,
			Slug:            params.Slug,
			Title:           params.Title,
			Summary:         params.Summary,
			Content:         params.Content,
			PageType:        params.PageType,
			Aliases:         params.Aliases,
			SourceRefs:      resolvedRefs,
		}
		_, err = t.wikiPageService.CreatePage(ctx, newPage)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to create page: " + err.Error()}, nil
		}
		action = "created"
	}

	// Inject cross-links so other pages know about this new/updated entity
	t.wikiPageService.InjectCrossLinks(ctx, kbID, []string{params.Slug})

	// Rebuild the index page to reflect the new/updated summary
	_ = t.wikiPageService.RebuildIndexPage(ctx, kbID)

	output := fmt.Sprintf("Successfully %s page [[%s]].\n- Title: %s\n- Type: %s\n- Summary: %s\n- Content length: %d chars", action, params.Slug, params.Title, params.PageType, params.Summary, len(params.Content))
	if len(params.Aliases) > 0 {
		output += fmt.Sprintf("\n- Aliases: %s", strings.Join(params.Aliases, ", "))
	}
	if len(resolvedRefs) > 0 {
		output += fmt.Sprintf("\n- Source refs: %d document(s)", len(resolvedRefs))
	}

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"display_type": "wiki_write_page",
			"action":       action,
			"slug":         params.Slug,
			"title":        params.Title,
			"page_type":    params.PageType,
			"summary":      params.Summary,
		},
	}, nil
}
