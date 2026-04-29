package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiDeletePageTool struct {
	BaseTool
	wikiPageService interfaces.WikiPageService
	kbIDs           []string
}

// NewWikiDeletePageTool creates a new wiki_delete_page tool
func NewWikiDeletePageTool(wikiPageService interfaces.WikiPageService, kbIDs []string) types.Tool {
	return &wikiDeletePageTool{
		BaseTool: NewBaseTool(
			ToolWikiDeletePage,
			"Delete a Wiki page. Automatically cleans up incoming links on other pages to prevent dead links.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {
						"type": "string",
						"description": "The slug of the Wiki page to delete"
					}
				},
				"required": ["slug"]
			}`),
		),
		wikiPageService: wikiPageService,
		kbIDs:           kbIDs,
	}
}

func (t *wikiDeletePageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug string `json:"slug"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	kbID := t.kbIDs[0]

	if params.Slug == "" {
		return &types.ToolResult{Success: false, Error: "slug is required"}, nil
	}

	// Fetch page to get its incoming links before deleting
	existingPage, err := t.wikiPageService.GetPageBySlug(ctx, kbID, params.Slug)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to fetch page to delete: " + err.Error()}, nil
	}
	inLinks := make([]string, len(existingPage.InLinks))
	copy(inLinks, existingPage.InLinks)

	err = t.wikiPageService.DeletePage(ctx, kbID, params.Slug)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to delete page: " + err.Error()}, nil
	}

	// Clean up incoming links to prevent dead links
	updatedCount := 0
	var updatedSlugs []string
	for _, sourceSlug := range inLinks {
		sourcePage, err := t.wikiPageService.GetPageBySlug(ctx, kbID, sourceSlug)
		if err == nil {
			changed := false

			// Replace [[deleted-slug]] with readable name
			parts := strings.Split(params.Slug, "/")
			readableName := parts[len(parts)-1]
			readableName = strings.ReplaceAll(readableName, "-", " ")

			link1 := "[[" + params.Slug + "]]"
			if strings.Contains(sourcePage.Content, link1) {
				sourcePage.Content = strings.ReplaceAll(sourcePage.Content, link1, readableName)
				changed = true
			}

			// Replace [[deleted-slug|Text]] with just Text
			re := regexp.MustCompile(`\[\[` + regexp.QuoteMeta(params.Slug) + `\|([^\]]+)\]\]`)
			if re.MatchString(sourcePage.Content) {
				sourcePage.Content = re.ReplaceAllString(sourcePage.Content, "$1")
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

	outputMsg := fmt.Sprintf("Successfully deleted page [[%s]] and cleaned up %d incoming links.", params.Slug, updatedCount)
	if updatedCount > 0 {
		outputMsg += fmt.Sprintf("\n- Affected pages: %s", strings.Join(updatedSlugs, ", "))
	}

	return &types.ToolResult{
		Success: true,
		Output:  outputMsg,
		Data: map[string]interface{}{
			"display_type":    "wiki_delete_page",
			"slug":            params.Slug,
			"title":           existingPage.Title,
			"updated_count":   updatedCount,
			"affected_pages":  updatedSlugs,
		},
	}, nil
}
