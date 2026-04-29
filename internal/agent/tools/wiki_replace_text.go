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
}

// NewWikiReplaceTextTool creates a new wiki_replace_text tool
func NewWikiReplaceTextTool(wikiPageService interfaces.WikiPageService, kbIDs []string, knowledgeService interfaces.KnowledgeService) types.Tool {
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
						"description": "An optional list of source knowledge IDs (UUIDs only) that justify this change. If provided, these will COMPLETELY REPLACE the existing source_refs of the page."
					}
				},
				"required": ["slug", "old_text", "new_text"]
			}`),
		),
		wikiPageService:  wikiPageService,
		knowledgeService: knowledgeService,
		kbIDs:            kbIDs,
	}
}

func (t *wikiReplaceTextTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug       string   `json:"slug"`
		OldText    string   `json:"old_text"`
		NewText    string   `json:"new_text"`
		SourceRefs []string `json:"source_refs"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	kbID := t.kbIDs[0]

	if params.OldText == "" {
		return &types.ToolResult{Success: false, Error: "old_text is required"}, nil
	}

	// Get the existing page
	existingPage, err := t.wikiPageService.GetPageBySlug(ctx, kbID, params.Slug)
	if err != nil {
		return &types.ToolResult{Success: false, Error: fmt.Sprintf("Failed to fetch page %s: %v", params.Slug, err)}, nil
	}

	if !strings.Contains(existingPage.Content, params.OldText) {
		return &types.ToolResult{Success: false, Error: "old_text not found in the current page content. Ensure you copy it exactly as it appears."}, nil
	}

	existingPage.Content = strings.Replace(existingPage.Content, params.OldText, params.NewText, 1)

	if len(params.SourceRefs) > 0 {
		existingPage.SourceRefs = resolveSourceRefs(ctx, t.knowledgeService, params.SourceRefs)
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
