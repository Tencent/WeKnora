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
	// Attribute every page write performed by this tool to the agent so
	// revision history distinguishes agent edits from pipeline/user ones.
	ctx = types.WithWikiEditSource(ctx, types.WikiEditSourceAgent)
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

	// Get existing page
	existingPage, kbID, err := resolveUniqueWikiPage(ctx, t.wikiPageService, params.Slug, t.kbIDs, t.routes)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to resolve page to rename: " + err.Error()}, nil
	}

	inLinks := make([]string, len(existingPage.InLinks))
	copy(inLinks, existingPage.InLinks)

	// Move the stable target first. Incoming page updates can then resolve
	// new_slug immediately and preserve the target's in_links metadata.
	if _, err = t.wikiPageService.RenamePage(ctx, kbID, params.Slug, params.NewSlug); err != nil {
		return &types.ToolResult{
			Success: false,
			Error:   "Rename aborted because the page slug could not be changed: " + err.Error(),
		}, nil
	}

	changes, updatedSlugs, rewriteErr := applyIncomingWikiContentRewrite(
		ctx, t.wikiPageService, kbID, inLinks,
		func(content string) (string, bool) {
			updated := strings.ReplaceAll(
				content, "[["+params.Slug+"]]", "[["+params.NewSlug+"]]",
			)
			updated = strings.ReplaceAll(
				updated, "[["+params.Slug+"|", "[["+params.NewSlug+"|",
			)
			return updated, updated != content
		},
	)
	if rewriteErr != nil {
		var renameRollbackErr error
		if _, err := t.wikiPageService.RenamePage(ctx, kbID, params.NewSlug, params.Slug); err != nil {
			renameRollbackErr = err
		}
		rollbackErr := rollbackWikiContentChanges(ctx, t.wikiPageService, changes)
		return &types.ToolResult{
			Success: false,
			Error: "Rename aborted while updating incoming links: " +
				joinWikiMutationErrors(rewriteErr, renameRollbackErr, rollbackErr),
		}, nil
	}
	updatedCount := len(updatedSlugs)
	t.routes.forget(params.Slug, kbID)
	t.routes.remember(params.NewSlug, kbID)

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
			"display_type":   "wiki_rename_page",
			"old_slug":       params.Slug,
			"new_slug":       params.NewSlug,
			"title":          existingPage.Title,
			"updated_count":  updatedCount,
			"affected_pages": updatedSlugs,
		},
	}, nil
}
