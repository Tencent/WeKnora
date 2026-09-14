package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiRenamePageTool struct {
	BaseTool
	wikiPageService interfaces.WikiPageService
	kbIDs           []string
	routes          *WikiRouteResolver
}

// NewWikiRenamePageTool creates a new wiki_rename_page tool
func NewWikiRenamePageTool(
	wikiPageService interfaces.WikiPageService,
	kbIDs []string,
	routes ...*WikiRouteResolver,
) types.Tool {
	return &wikiRenamePageTool{
		BaseTool: NewBaseTool(
			ToolWikiRenamePage,
			"Rename a Wiki page's slug. Automatically cascades the new slug to all pages that linked to the old one.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {
						"type": "string",
						"description": "The current slug of the Wiki page"
					},
					"new_slug": {
						"type": "string",
						"description": "The new slug for the page"
					}
				},
				"required": ["slug", "new_slug"]
			}`),
		),
		wikiPageService: wikiPageService,
		kbIDs:           kbIDs,
		routes:          firstWikiRoute(routes),
	}
}

func (t *wikiRenamePageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug    string `json:"slug"`
		NewSlug string `json:"new_slug"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	if params.NewSlug == "" {
		return &types.ToolResult{Success: false, Error: "new_slug is required"}, nil
	}
	normalizedSlug, slugErr := normalizeAndValidateWikiSlug(params.Slug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	params.Slug = normalizedSlug
	normalizedNewSlug, slugErr := normalizeAndValidateWikiSlug(params.NewSlug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	params.NewSlug = normalizedNewSlug
	if params.NewSlug == params.Slug {
		return &types.ToolResult{Success: false, Error: "new_slug must be different from old slug"}, nil
	}
	renamer, ok := t.wikiPageService.(interfaces.WikiPageRenamer)
	if !ok {
		return &types.ToolResult{Success: false, Error: interfaces.ErrWikiRenameUnsupported.Error()}, nil
	}

	// Get existing page
	existingPage, kbID, err := resolveUniqueWikiPage(ctx, t.wikiPageService, params.Slug, t.kbIDs, t.routes)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to resolve page to rename: " + err.Error()}, nil
	}

	renamed, err := renamer.RenamePage(ctx, interfaces.WikiPageRenameRequest{
		KnowledgeBaseID: kbID,
		PageID:          existingPage.ID,
		OldSlug:         params.Slug,
		NewSlug:         params.NewSlug,
	})
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to rename wiki page: " + err.Error()}, nil
	}
	t.routes.forget(params.Slug, kbID)
	t.routes.remember(params.NewSlug, kbID)

	updatedSlugs := make([]string, 0, len(renamed.AffectedPages))
	for _, page := range renamed.AffectedPages {
		if page.ID != renamed.Page.ID {
			updatedSlugs = append(updatedSlugs, page.Slug)
		}
	}
	updatedCount := len(updatedSlugs)
	outputMsg := fmt.Sprintf(
		"Successfully renamed page [[%s]] to [[%s]], preserving its ID and history, "+
			"and updated %d related pages.",
		params.Slug,
		params.NewSlug,
		updatedCount,
	)
	if updatedCount > 0 {
		outputMsg += fmt.Sprintf("\n- Affected pages: %s", strings.Join(updatedSlugs, ", "))
	}
	return &types.ToolResult{
		Success: true,
		Output:  outputMsg,
		Data: map[string]interface{}{
			"display_type":   "wiki_rename_page",
			"page_id":        renamed.Page.ID,
			"old_slug":       params.Slug,
			"new_slug":       params.NewSlug,
			"title":          renamed.Page.Title,
			"updated_count":  updatedCount,
			"affected_pages": updatedSlugs,
		},
	}, nil
}
