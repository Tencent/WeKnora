package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiReplaceTextTool struct {
	BaseTool
	wikiPageService  interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	kbIDs            []string
	routes           *WikiRouteResolver
	searchTargets    types.SearchTargets
	scopeEnforced    bool
}

// NewWikiReplaceTextTool creates a new wiki_replace_text tool
func NewWikiReplaceTextTool(
	wikiPageService interfaces.WikiPageService,
	kbIDs []string,
	knowledgeService interfaces.KnowledgeService,
	routes ...*WikiRouteResolver,
) *wikiReplaceTextTool {
	return &wikiReplaceTextTool{
		BaseTool: NewBaseTool(
			ToolWikiReplaceText,
			"Replace specific exact text in a Wiki page. Ideal for minor corrections.",
			json.RawMessage(`{
				"type": "object",
				"properties": {
					"slug": {
						"type": "string",
						"description": "The slug of the Wiki page"
					},
					"old_text": {
						"type": "string",
						"description": "The exact text to find and replace"
					},
					"new_text": {
						"type": "string",
						"description": "The new text to insert"
					},
					"source_refs": {
						"type": "array",
						"items": {"type": "string"},
						"description": "An optional list of short dN source document IDs that justify this change. If provided, these will COMPLETELY REPLACE the existing source_refs of the page."
					}
				},
				"required": ["slug", "old_text", "new_text"]
			}`),
		),
		wikiPageService:  wikiPageService,
		knowledgeService: knowledgeService,
		kbIDs:            kbIDs,
		routes:           firstWikiRoute(routes),
	}
}

// WithSearchTargets enables the Agent authorization boundary for source_refs.
// An Agent turn with no search target must reject every source document.
func (t *wikiReplaceTextTool) WithSearchTargets(searchTargets types.SearchTargets) *wikiReplaceTextTool {
	t.searchTargets = searchTargets
	t.scopeEnforced = true
	return t
}

func (t *wikiReplaceTextTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Attribute every page write performed by this tool to the agent so
	// revision history distinguishes agent edits from pipeline/user ones.
	ctx = types.WithWikiEditSource(ctx, types.WikiEditSourceAgent)
	var params struct {
		Slug       string    `json:"slug"`
		OldText    string    `json:"old_text"`
		NewText    string    `json:"new_text"`
		SourceRefs *[]string `json:"source_refs"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	if params.OldText == "" {
		return &types.ToolResult{Success: false, Error: "old_text is required"}, nil
	}
	normalizedSlug, slugErr := normalizeAndValidateWikiSlug(params.Slug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	params.Slug = normalizedSlug

	// Get the existing page
	existingPage, _, err := resolveUniqueWikiPage(ctx, t.wikiPageService, params.Slug, t.kbIDs, t.routes)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to fetch page %s: %v", params.Slug, err)}, nil
	}

	if !strings.Contains(existingPage.Content, params.OldText) {
		return &types.ToolResult{Success: false, Error: "old_text not found in the current page content. Ensure you copy it exactly as it appears."}, nil
	}

	existingPage.Content = strings.Replace(existingPage.Content, params.OldText, params.NewText, 1)

	if params.SourceRefs != nil {
		if t.scopeEnforced {
			resolvedRefs, scopeErr := resolveAuthorizedSourceRefs(ctx, t.searchTargets, *params.SourceRefs, t.knowledgeService)
			if scopeErr != nil {
				return &types.ToolResult{Success: false, Error: "Invalid source_refs: " + scopeErr.Error()}, nil
			}
			existingPage.SourceRefs = resolvedRefs
		} else {
			existingPage.SourceRefs = resolveSourceRefs(ctx, t.knowledgeService, *params.SourceRefs)
		}
	}

	_, err = t.wikiPageService.UpdatePage(ctx, existingPage)
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to update page: " + err.Error()}, nil
	}

	oldPreview := truncateRunes(params.OldText, 80)
	newPreview := truncateRunes(params.NewText, 80)

	output := fmt.Sprintf("Successfully replaced text on page [[%s]].\n- Old: %s\n- New: %s", params.Slug, oldPreview, newPreview)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"display_type": "wiki_replace_text",
			"slug":         params.Slug,
			"title":        existingPage.Title,
			"old_text":     oldPreview,
			"new_text":     newPreview,
		},
	}, nil
}
