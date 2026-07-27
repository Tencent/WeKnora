package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type wikiWritePageTool struct {
	BaseTool
	wikiPageService  interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	kbIDs            []string
	routes           *WikiRouteResolver
	searchTargets    types.SearchTargets
	scopeEnforced    bool
}

// NewWikiWritePageTool creates a new wiki_write_page tool
func NewWikiWritePageTool(
	wikiPageService interfaces.WikiPageService,
	kbIDs []string,
	knowledgeService interfaces.KnowledgeService,
	routes ...*WikiRouteResolver,
) *wikiWritePageTool {
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
						"description": "A list of aliases for the page (optional). If provided, these will COMPLETELY REPLACE the existing aliases of the page."
					},
					"source_refs": {
						"type": "array",
						"items": {"type": "string"},
						"description": "A list of short dN source document IDs that contributed to this page. If provided, these will COMPLETELY REPLACE the existing source_refs of the page."
					}
				},
				"required": ["slug", "title", "summary", "content", "page_type"]
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
func (t *wikiWritePageTool) WithSearchTargets(searchTargets types.SearchTargets) *wikiWritePageTool {
	t.searchTargets = searchTargets
	t.scopeEnforced = true
	return t
}

func (t *wikiWritePageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	// Attribute every page write performed by this tool to the agent so
	// revision history distinguishes agent edits from pipeline/user ones.
	ctx = types.WithWikiEditSource(ctx, types.WikiEditSourceAgent)
	var params struct {
		Slug       string    `json:"slug"`
		Title      string    `json:"title"`
		Summary    string    `json:"summary"`
		Content    string    `json:"content"`
		PageType   string    `json:"page_type"`
		Aliases    *[]string `json:"aliases"`
		SourceRefs *[]string `json:"source_refs"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to parse arguments: " + err.Error()}, nil
	}

	if len(t.kbIDs) == 0 {
		return &types.ToolResult{Success: false, Error: "No knowledge bases available for editing"}, nil
	}
	if params.Title == "" || params.PageType == "" || params.Content == "" || params.Summary == "" {
		return &types.ToolResult{Success: false, Error: "title, summary, content, and page_type are required for write action"}, nil
	}

	// Validate + normalize the slug up front. The model routinely emits
	// malformed slugs (stray characters, mangled UUIDs); persisting them
	// verbatim creates unreachable pages and dead cross-links.
	normalizedSlug, slugErr := normalizeAndValidateWikiSlug(params.Slug)
	if slugErr != nil {
		return &types.ToolResult{Success: false, Error: slugErr.Error()}, nil
	}
	params.Slug = normalizedSlug

	// Resolve and authorize provenance before choosing a creation target. In a
	// multi-Wiki Agent, source documents provide a server-owned KB hint and
	// remove the need for a model-visible knowledge_base_id argument.
	var resolvedRefs []string
	var err error
	if params.SourceRefs != nil {
		if t.scopeEnforced {
			resolvedRefs, err = resolveAuthorizedSourceRefs(ctx, t.searchTargets, *params.SourceRefs, t.knowledgeService)
			if err != nil {
				return &types.ToolResult{Success: false, Error: "Invalid source_refs: " + err.Error()}, nil
			}
		} else {
			resolvedRefs = resolveSourceRefs(ctx, t.knowledgeService, *params.SourceRefs)
		}
	}
	// Resolve existing pages across every legal Wiki KB. Cached provenance only
	// influences lookup order; ambiguous slugs are never silently written to
	// the first KB. New pages require one unambiguous creation target.
	existingPage, kbID, err := resolveUniqueWikiPage(ctx, t.wikiPageService, params.Slug, t.kbIDs, t.routes)
	if errors.Is(err, errWikiPageNotFoundInScope) {
		existingPage = nil
		sourceKBHints, sourceErr := wikiKnowledgeBasesForSourceRefs(
			ctx, resolvedRefs, t.knowledgeService, t.kbIDs,
		)
		if sourceErr != nil {
			return &types.ToolResult{Success: false, Error: "Failed to resolve source_refs routing: " + sourceErr.Error()}, nil
		}
		kbID, err = resolveWikiCreateKB(params.Slug, t.kbIDs, t.routes, sourceKBHints...)
	}
	if err != nil {
		return &types.ToolResult{Success: false, Error: "Failed to resolve wiki target: " + err.Error()}, nil
	}

	// Summary pages are system-owned: they are generated deterministically
	// from a source document and keyed by its knowledge ID
	// (summary/<knowledgeID>). Letting the agent CREATE one with an
	// arbitrary/hand-typed slug is precisely how mangled-UUID ghost summary
	// existing summary page, but never fabricating a new one.
	if existingPage == nil &&
		(isSummaryNamespace(params.Slug) || strings.EqualFold(params.PageType, types.WikiPageTypeSummary)) {
		return &types.ToolResult{
			Success: false,
			Error: "summary pages are generated automatically from source documents and cannot be created manually. " +
				"Use page_type 'synthesis'/'comparison'/'entity'/'concept' for authored pages, " +
				"or target an existing summary page to update it.",
		}, nil
	}

	// Auto-repair dead [[slug]] references in the body before persisting.
	// This rewrites LLM-mangled links (most importantly UUID-based summary
	// slugs) back to their real target when a confident match exists, and
	// leaves everything else untouched. Best-effort — never block the write.
	if repaired, changed, rerr := t.wikiPageService.RepairContentLinks(ctx, kbID, params.Slug, params.Content); rerr == nil && changed {
		params.Content = repaired
	}

	var action string
	if existingPage != nil {
		// Update
		existingPage.Title = params.Title
		existingPage.Summary = params.Summary
		existingPage.Content = params.Content
		existingPage.PageType = params.PageType
		if params.Aliases != nil {
			existingPage.Aliases = *params.Aliases
		}

		if params.SourceRefs != nil {
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
			SourceRefs:      resolvedRefs,
		}
		if params.Aliases != nil {
			newPage.Aliases = *params.Aliases
		}
		_, err = t.wikiPageService.CreatePage(ctx, newPage)
		if err != nil {
			return &types.ToolResult{Success: false, Error: "Failed to create page: " + err.Error()}, nil
		}
		action = "created"
	}
	t.routes.remember(params.Slug, kbID)

	// Inject cross-links so other pages know about this new/updated entity
	t.wikiPageService.InjectCrossLinks(ctx, kbID, []string{params.Slug})

	// Rebuild the index page to reflect the new/updated summary
	_ = t.wikiPageService.RebuildIndexPage(ctx, kbID)

	output := fmt.Sprintf("Successfully %s page [[%s]].\n- Title: %s\n- Type: %s\n- Summary: %s\n- Content length: %d chars", action, params.Slug, params.Title, params.PageType, params.Summary, len(params.Content))
	if params.Aliases != nil && len(*params.Aliases) > 0 {
		output += fmt.Sprintf("\n- Aliases: %s", strings.Join(*params.Aliases, ", "))
	}
	if params.SourceRefs != nil {
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

// normalizeAndValidateWikiSlug lowercases + trims a model-supplied slug and
// rejects malformed ones. A valid slug contains only lowercase ASCII letters,
// digits, '-', '/', or CJK characters, has no leading/trailing/duplicate '/',
// and is non-empty. Keeping this strict stops the agent from persisting
// unreachable pages built from stray characters or garbled identifiers.
func normalizeAndValidateWikiSlug(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.ReplaceAll(s, " ", "-")
	if s == "" {
		return "", errors.New("slug is required and must be non-empty")
	}
	if strings.Contains(s, "//") || strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/") {
		return "", fmt.Errorf("invalid slug %q: '/' separators are malformed", raw)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '/':
		case r >= 0x4E00 && r <= 0x9FFF: // keep CJK characters
		default:
			return "", fmt.Errorf(
				"invalid slug %q: character %q is not allowed (use lowercase letters, digits, '-', '/', or CJK)",
				raw, string(r),
			)
		}
	}
	return s, nil
}

// isSummaryNamespace reports whether a slug lives in the system-owned summary
// namespace (summary/…).
func isSummaryNamespace(slug string) bool {
	return strings.HasPrefix(slug, types.WikiPageTypeSummary+"/")
}
