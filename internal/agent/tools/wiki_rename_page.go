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
}

// NewWikiRenamePageTool creates a new wiki_rename_page tool
func NewWikiRenamePageTool(wikiPageService interfaces.WikiPageService, kbIDs []string) types.Tool {
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
	kbID := t.kbIDs[0]

	if params.NewSlug == "" {
		return &types.ToolResult{Success: false, Error: "new_slug is required"}, nil
	}
	if params.NewSlug == params.Slug {
		return &types.ToolResult{Success: false, Error: "new_slug must be different from old slug"}, nil
	}

	// Get existing page
	existingPage, err := t.wikiPageService.GetPageBySlug(ctx, kbID, params.Slug)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Page %s not found. Cannot rename a non-existent page.", params.Slug)}, nil
	}

	inLinks := make([]string, len(existingPage.InLinks))
	copy(inLinks, existingPage.InLinks)

	// Create new page with new slug but same content
	newPage := &types.WikiPage{
		KnowledgeBaseID: kbID,
		Slug:            params.NewSlug,
		Title:           existingPage.Title,
		Summary:         existingPage.Summary,
		Content:         existingPage.Content,
		PageType:        existingPage.PageType,
		Aliases:         existingPage.Aliases,
	}
	_, err = t.wikiPageService.CreatePage(ctx, newPage)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to create renamed page: " + err.Error()}, nil
	}

	// Update incoming links in other pages
	updatedCount := 0
	var updatedSlugs []string
	for _, sourceSlug := range inLinks {
		sourcePage, err := t.wikiPageService.GetPageBySlug(ctx, kbID, sourceSlug)
		if err == nil {
			changed := false
			
			// Replace [[old-slug]] with [[new-slug]]
			link1 := "[[" + params.Slug + "]]"
			newLink1 := "[[" + params.NewSlug + "]]"
			if strings.Contains(sourcePage.Content, link1) {
				sourcePage.Content = strings.ReplaceAll(sourcePage.Content, link1, newLink1)
				changed = true
			}
			
			// Replace [[old-slug|text]] with [[new-slug|text]]
			link2 := "[[" + params.Slug + "|"
			newLink2 := "[[" + params.NewSlug + "|"
			if strings.Contains(sourcePage.Content, link2) {
				sourcePage.Content = strings.ReplaceAll(sourcePage.Content, link2, newLink2)
				changed = true
			}

			if changed {
				_, updateErr := t.wikiPageService.UpdatePage(ctx, sourcePage)
				if updateErr == nil {
					updatedCount++
					updatedSlugs = append(updatedSlugs, sourceSlug)
				}
			}
		}
	}

	// Delete old page
	err = t.wikiPageService.DeletePage(ctx, kbID, params.Slug)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Successfully created new page and updated links, but failed to delete old page: " + err.Error()}, nil
	}

	// Inject cross-links so other pages know about this new slug
	t.wikiPageService.InjectCrossLinks(ctx, kbID, []string{params.NewSlug})

	// Rebuild the index page to reflect the new/updated summary
	_ = t.wikiPageService.RebuildIndexPage(ctx, kbID)

	outputMsg := fmt.Sprintf("Successfully renamed page [[%s]] → [[%s]] and updated %d incoming links.", params.Slug, params.NewSlug, updatedCount)
	if updatedCount > 0 {
		outputMsg += fmt.Sprintf("\n- Affected pages: %s", strings.Join(updatedSlugs, ", "))
	}

	return &types.ToolResult{
		Success: true,
		Output:  outputMsg,
		Data: map[string]interface{}{
			"display_type":    "wiki_rename_page",
			"old_slug":        params.Slug,
			"new_slug":        params.NewSlug,
			"title":           existingPage.Title,
			"updated_count":   updatedCount,
			"affected_pages":  updatedSlugs,
		},
	}, nil
}
