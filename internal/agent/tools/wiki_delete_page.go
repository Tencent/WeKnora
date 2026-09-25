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
	routes          *WikiRouteResolver
}

// NewWikiDeletePageTool creates a new wiki_delete_page tool
func NewWikiDeletePageTool(
	wikiPageService interfaces.WikiPageService,
	kbIDs []string,
	routes ...*WikiRouteResolver,
) types.Tool {
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
		routes:          firstWikiRoute(routes),
	}
}

func (t *wikiDeletePageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Attribute every page write performed by this tool to the agent so
	// revision history distinguishes agent edits from pipeline/user ones.
	ctx = types.WithWikiEditSource(ctx, types.WikiEditSourceAgent)
	var params struct {
		Slug string `json:"slug"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	if params.Slug == "" {
		return &types.ToolResult{Success: false, Error: "slug is required"}, nil
	}
	normalizedSlug, slugErr := normalizeAndValidateWikiSlug(params.Slug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	params.Slug = normalizedSlug

	// Fetch page to get its incoming links before deleting
	existingPage, kbID, err := resolveUniqueWikiPage(ctx, t.wikiPageService, params.Slug, t.kbIDs, t.routes)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to fetch page to delete: " + err.Error()}, nil
	}
	inLinks := make([]string, len(existingPage.InLinks))
	copy(inLinks, existingPage.InLinks)

	parts := strings.Split(params.Slug, "/")
	readableName := strings.ReplaceAll(parts[len(parts)-1], "-", " ")
	pipeLinkRE := regexp.MustCompile(`\[\[` + regexp.QuoteMeta(params.Slug) + `\|([^\]]+)\]\]`)
	changes, updatedSlugs, rewriteErr := applyIncomingWikiContentRewrite(
		ctx, t.wikiPageService, kbID, inLinks,
		func(content string) (string, bool) {
			updated := strings.ReplaceAll(content, "[["+params.Slug+"]]", readableName)
			updated = pipeLinkRE.ReplaceAllString(updated, "$1")
			return updated, updated != content
		},
	)
	if rewriteErr != nil {
		rollbackErr := rollbackWikiContentChanges(ctx, t.wikiPageService, changes)
		return &types.ToolResult{
			Success: false,
			Error: "Delete aborted while cleaning incoming links: " +
				joinWikiMutationErrors(rewriteErr, rollbackErr),
		}, nil
	}

	err = t.wikiPageService.DeletePage(ctx, kbID, params.Slug)
	if err != nil {
		rollbackErr := rollbackWikiContentChanges(ctx, t.wikiPageService, changes)
		return &types.ToolResult{
			Success: false,
			Error: "Delete aborted because the page could not be removed: " +
				joinWikiMutationErrors(err, rollbackErr),
		}, nil
	}
	t.routes.forget(params.Slug, kbID)
	updatedCount := len(updatedSlugs)

	outputMsg := fmt.Sprintf("Successfully deleted page [[%s]] and cleaned up %d incoming links.", params.Slug, updatedCount)
	if updatedCount > 0 {
		outputMsg += fmt.Sprintf("\n- Affected pages: %s", strings.Join(updatedSlugs, ", "))
	}

	return &types.ToolResult{
		Success: true,
		Output:  outputMsg,
		Data: map[string]interface{}{
			"display_type":   "wiki_delete_page",
			"slug":           params.Slug,
			"title":          existingPage.Title,
			"updated_count":  updatedCount,
			"affected_pages": updatedSlugs,
		},
	}, nil
}
