package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ---- wiki_read_page ----

// WikiScope describes the effective retrieval scope for a single wiki
// knowledge base within an agent session.
//
//   - KnowledgeBaseID: the wiki KB to search.
//   - KnowledgeIDs: OPTIONAL whitelist of source knowledge (document) IDs.
//     When non-empty, a wiki page is only surfaced if its SourceRefs
//     intersect this set. Used to honour user-level @mentions that pin the
//     search to specific documents inside a KB.
type WikiScope struct {
	KnowledgeBaseID string
	KnowledgeIDs    []string
}

// NewWikiScopesFromKBIDs is a convenience constructor for callers that only
// carry plain KB IDs and don't need per-document filtering (e.g. legacy tests).
func NewWikiScopesFromKBIDs(kbIDs []string) []WikiScope {
	scopes := make([]WikiScope, 0, len(kbIDs))
	for _, id := range kbIDs {
		if id == "" {
			continue
		}
		scopes = append(scopes, WikiScope{KnowledgeBaseID: id})
	}
	return scopes
}

// scopeKnowledgeFilter returns the server-enforced knowledge-ID whitelist for
// a KB, derived purely from the agent scope (never from tool arguments).
// The model does not see this filter; it is applied silently by Execute.
//
// Returns (filterSet, hasFilter). When hasFilter is false, no filtering is
// applied to pages from this KB.
func scopeKnowledgeFilter(scope WikiScope) (map[string]bool, bool) {
	if len(scope.KnowledgeIDs) == 0 {
		return nil, false
	}
	set := make(map[string]bool, len(scope.KnowledgeIDs))
	for _, id := range scope.KnowledgeIDs {
		if id != "" {
			set[id] = true
		}
	}
	if len(set) == 0 {
		return nil, false
	}
	return set, true
}

// extractSourceKnowledgeIDs parses SourceRefs ("uuid" or "uuid|title") and
// returns the bare knowledge IDs.
func extractSourceKnowledgeIDs(page *types.WikiPage) []string {
	if page == nil || len(page.SourceRefs) == 0 {
		return nil
	}
	ids := make([]string, 0, len(page.SourceRefs))
	for _, ref := range page.SourceRefs {
		kid := ref
		if pipeIdx := strings.Index(ref, "|"); pipeIdx > 0 {
			kid = ref[:pipeIdx]
		}
		if kid != "" {
			ids = append(ids, kid)
		}
	}
	return ids
}

// isStructuralPage reports whether a page is a wiki-level structural/meta
// page (index, log) rather than a content page tied to specific source
// documents. Structural pages are never filtered by knowledge_ids scope —
// they describe wiki topology (TOC, operation log) and must remain reachable
// even when the user has pinned specific documents.
func isStructuralPage(page *types.WikiPage) bool {
	if page == nil {
		return false
	}
	switch page.PageType {
	case types.WikiPageTypeIndex, types.WikiPageTypeLog:
		return true
	}
	return false
}

// registerLinkedSlugs records the KB that owns this page for every slug the
// page links to (outbound) or is linked from (inbound). Wiki links are
// KB-local — a `[[slug]]` inside page X in KB A always refers to another
// page in KB A — so sharing the KB mapping with neighbours is safe and lets
// the frontend resolve a slug the model echoed from a page body (e.g. an
// `index` page's table of contents) without an extra round trip.
func registerLinkedSlugs(foundKBs map[string][]string, page *types.WikiPage, kbID string) {
	if page == nil || kbID == "" {
		return
	}
	add := func(slug string) {
		if slug == "" {
			return
		}
		for _, existing := range foundKBs[slug] {
			if existing == kbID {
				return
			}
		}
		foundKBs[slug] = append(foundKBs[slug], kbID)
	}
	for _, s := range page.OutLinks {
		add(s)
	}
	for _, s := range page.InLinks {
		add(s)
	}
}

// pageIntersectsKnowledgeIDs reports whether the page should pass the
// knowledge-ID scope filter.
//
//   - Empty allowed set = "no filter" → always true.
//   - Structural pages (index/log) are always surfaced so the model can still
//     navigate wiki topology under a pinned-doc scope.
//   - Pages with no SourceRefs at all are conservatively allowed through: the
//     filter is meant to narrow document-derived content, not hide metadata
//     pages that happen to have empty refs.
//   - Otherwise, at least one of the page's SourceRefs must be in allowed.
func pageIntersectsKnowledgeIDs(page *types.WikiPage, allowed map[string]bool) bool {
	if len(allowed) == 0 {
		return true
	}
	if isStructuralPage(page) {
		return true
	}
	ids := extractSourceKnowledgeIDs(page)
	if len(ids) == 0 {
		return true
	}
	for _, kid := range ids {
		if allowed[kid] {
			return true
		}
	}
	return false
}

type wikiReadPageTool struct {
	BaseTool
	wikiService interfaces.WikiPageService
	scopes      []WikiScope
	seenLinks   map[string]bool
	mu          sync.Mutex
}

func NewWikiReadPageTool(wikiService interfaces.WikiPageService, scopes []WikiScope) types.Tool {
	return &wikiReadPageTool{
		BaseTool: NewBaseTool(
			ToolWikiReadPage,
			`Read one or more wiki pages by their slugs. Returns the full markdown content, metadata, and links.
Use this to read specific wiki pages when you know their slug (e.g. "entity/acme-corp", "concept/rag").
When the same slug exists in multiple knowledge bases, all matching pages are returned (each tagged with its knowledge_base_id). Pass "knowledge_base_id" to limit to a specific KB.`,
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "slugs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "List of wiki page slugs to read (e.g. ['entity/acme-corp', 'index'])"
    },
    "knowledge_base_id": {
      "type": "string",
      "description": "Optional: specific knowledge base ID. If omitted, reads the slug from every wiki KB in scope (all matches returned)."
    }
  },
  "required": ["slugs"]
}`),
		),
		wikiService: wikiService,
		scopes:      scopes,
		seenLinks:   make(map[string]bool),
	}
}

// seenLinkKey builds a dedupe key scoped to a knowledge base so that identical
// slugs from different KBs are not collapsed into a single "already seen" entry.
func seenLinkKey(kbID, slug string) string {
	return kbID + "\x00" + slug
}

func (t *wikiReadPageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug            any    `json:"slug"`
		Slugs           any    `json:"slugs"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	var slugsToFetch []string
	slugsToFetch = append(slugsToFetch, parseStringOrArray(params.Slugs)...)
	slugsToFetch = append(slugsToFetch, parseStringOrArray(params.Slug)...)

	if len(slugsToFetch) == 0 {
		return &types.ToolResult{Success: false, Error: "Missing 'slugs' parameter"}, nil
	}

	// Build effective scope list. If the caller pinned a knowledge_base_id,
	// limit to that KB while preserving any scope-level knowledge_ids filter
	// (the filter itself is never exposed as a tool argument — it comes from
	// the server-side scope so the model doesn't have to reason about it).
	effectiveScopes := t.scopes
	if params.KnowledgeBaseID != "" {
		filtered := make([]WikiScope, 0, 1)
		for _, sc := range t.scopes {
			if sc.KnowledgeBaseID == params.KnowledgeBaseID {
				filtered = append(filtered, sc)
				break
			}
		}
		if len(filtered) == 0 {
			// Not in the agent's scope list — still allow direct addressing
			// but without any pin (scopes were the source of the pin).
			filtered = append(filtered, WikiScope{KnowledgeBaseID: params.KnowledgeBaseID})
		}
		effectiveScopes = filtered
	}

	var outputs []string
	var errs []string
	// Per-slug list of KB IDs where the slug was found. A slug may exist in
	// multiple KBs when the agent has several wiki KBs in scope.
	foundKBs := make(map[string][]string)

	formatLinks := func(slugs []string, kbID string) []string {
		var descs []string
		for _, s := range slugs {
			key := seenLinkKey(kbID, s)
			t.mu.Lock()
			seen := t.seenLinks[key]
			t.seenLinks[key] = true
			t.mu.Unlock()

			if seen {
				// We already injected the summary for this link in this session (within the same KB)
				descs = append(descs, fmt.Sprintf("[[%s]] (summary omitted, already seen)", s))
			} else {
				if linkPage, err := t.wikiService.GetPageBySlug(ctx, kbID, s); err == nil && linkPage != nil {
					descs = append(descs, fmt.Sprintf("[[%s]] (%s)", s, linkPage.Summary))
				} else {
					descs = append(descs, fmt.Sprintf("[[%s]]", s))
				}
			}
		}
		if len(descs) == 0 {
			return []string{"(none)"}
		}
		return descs
	}

	renderPage := func(page *types.WikiPage, kbID string) string {
		outLinksDesc := formatLinks(page.OutLinks, kbID)
		inLinksDesc := formatLinks(page.InLinks, kbID)

		// Render source refs
		var sourcesDesc []string
		if len(page.SourceRefs) > 0 {
			for _, ref := range page.SourceRefs {
				// SourceRefs might be "knowledgeID" or "knowledgeID|Title"
				kid := ref
				title := ""
				if pipeIdx := strings.Index(ref, "|"); pipeIdx > 0 {
					kid = ref[:pipeIdx]
					title = ref[pipeIdx+1:]
				}
				if title != "" {
					sourcesDesc = append(sourcesDesc, fmt.Sprintf(`<source knowledge_id="%s">%s</source>`, kid, title))
				} else {
					sourcesDesc = append(sourcesDesc, fmt.Sprintf(`<source knowledge_id="%s"/>`, kid))
				}
			}
		}

		return fmt.Sprintf(`<wiki_page>
<metadata>
<knowledge_base_id>%s</knowledge_base_id>
<link>[[%s|%s]]</link>
<type>%s</type>
<aliases>%s</aliases>
</metadata>
<relationships>
<links_to>%s</links_to>
<linked_from>%s</linked_from>
</relationships>
<sources>
%s
</sources>
<summary>
%s
</summary>
<content>
%s
</content>
</wiki_page>`,
			kbID,
			page.Slug, page.Title, page.PageType,
			strings.Join(page.Aliases, ", "),
			strings.Join(outLinksDesc, ", "),
			strings.Join(inLinksDesc, ", "),
			strings.Join(sourcesDesc, "\n"),
			page.Summary,
			page.Content,
		)
	}

	// Track slugs that were found in the raw lookup but filtered out by a
	// knowledge_ids whitelist, so we can surface a clearer error.
	filteredOut := make(map[string][]string) // slug -> list of KB IDs where filtered
	for _, slug := range slugsToFetch {
		var hits []struct {
			page *types.WikiPage
			kbID string
		}
		for _, sc := range effectiveScopes {
			kbID := sc.KnowledgeBaseID
			if kbID == "" {
				continue
			}
			page, err := t.wikiService.GetPageBySlug(ctx, kbID, slug)
			if err != nil || page == nil {
				continue
			}
			actualKBID := kbID
			if page.KnowledgeBaseID != "" {
				actualKBID = page.KnowledgeBaseID
			}

			// Apply server-enforced knowledge-ID scope (silent; never exposed
			// to the model as a tool argument).
			if allowed, has := scopeKnowledgeFilter(sc); has {
				if !pageIntersectsKnowledgeIDs(page, allowed) {
					filteredOut[slug] = append(filteredOut[slug], actualKBID)
					continue
				}
			}

			hits = append(hits, struct {
				page *types.WikiPage
				kbID string
			}{page, actualKBID})
			foundKBs[slug] = append(foundKBs[slug], actualKBID)
			// Also register the page's neighbours so that when the model
			// echoes a link like `[[summary/xyz]]` from this page's body,
			// the frontend can resolve it to the same KB without guessing.
			registerLinkedSlugs(foundKBs, page, actualKBID)
			t.mu.Lock()
			t.seenLinks[seenLinkKey(actualKBID, slug)] = true
			t.mu.Unlock()
		}

		if len(hits) == 0 {
			if kbs := filteredOut[slug]; len(kbs) > 0 {
				errs = append(errs, fmt.Sprintf(
					"Wiki page '%s' exists in %v but none of its source documents are within the scope pinned by the user",
					slug, kbs,
				))
			} else {
				errs = append(errs, fmt.Sprintf("Wiki page '%s' not found", slug))
			}
			continue
		}

		// When the same slug exists in multiple KBs (and the caller did not
		// specify a knowledge_base_id), emit all pages so the model can pick
		// the right one or compare them explicitly.
		for _, h := range hits {
			outputs = append(outputs, renderPage(h.page, h.kbID))
		}
	}

	if len(outputs) == 0 {
		return &types.ToolResult{Success: false, Error: strings.Join(errs, "; ")}, nil
	}

	finalOutput := strings.Join(outputs, "\n\n")
	if len(errs) > 0 {
		finalOutput += fmt.Sprintf("\n\n<errors>\n%s\n</errors>", strings.Join(errs, "\n"))
	}

	// Surface ambiguous slugs so the caller (and logs) can see when a slug
	// resolved to more than one KB.
	ambiguous := make(map[string][]string)
	for slug, kbs := range foundKBs {
		if len(kbs) > 1 {
			ambiguous[slug] = kbs
		}
	}

	return &types.ToolResult{
		Success: true,
		Output:  finalOutput,
		Data: map[string]interface{}{
			"found_kbs":       foundKBs,
			"ambiguous_slugs": ambiguous,
		},
	}, nil
}

// ---- wiki_search ----

type wikiSearchTool struct {
	BaseTool
	wikiService interfaces.WikiPageService
	scopes      []WikiScope
	seenSlugs   map[string]bool
	mu          sync.Mutex
}

func NewWikiSearchTool(wikiService interfaces.WikiPageService, scopes []WikiScope) types.Tool {
	return &wikiSearchTool{
		BaseTool: NewBaseTool(
			ToolWikiSearch,
			`Search wiki pages using PostgreSQL POSIX regular expressions (~* operator, case-insensitive).
STRONGLY PREFER using regex to search for multiple concepts at once rather than simple plain text queries.
Returns matching pages with titles, slugs, and summaries (each tagged with its knowledge_base_id).
Examples:
- Alternation (RECOMMENDED): "stardust|skyvault" (matches either word)
- Multiple terms (RECOMMENDED): "psionic.*engine" (matches both words in order)
- Prefix matching: "^entity/.*" (finds all entities)
- Plain text: "engine" (matches anywhere in title/content/slug/summary)
IMPORTANT — JSON escaping: every backslash in a regex MUST be written as \\ inside the JSON tool arguments (e.g. to search for literal "C++" write "C\\+\\+", NOT "C\+\+"; for "\d+" write "\\d+"). Plain "\+" / "\d" etc. are invalid JSON escapes and will fail to parse.
Use this to find relevant wiki pages when you don't know the exact slug.`,
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "queries": {
      "type": "array",
      "items": { "type": "string" },
      "description": "List of regex search queries to run"
    },
    "limit": {
      "type": "integer",
      "description": "Max results to return per query (default 10)"
    },
    "knowledge_base_id": {
      "type": "string",
      "description": "Optional: restrict search to a single knowledge base ID in scope."
    }
  },
  "required": ["queries"]
}`),
		),
		wikiService: wikiService,
		scopes:      scopes,
		seenSlugs:   make(map[string]bool),
	}
}

func (t *wikiSearchTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Query           any    `json:"query"`
		Queries         any    `json:"queries"`
		Limit           int    `json:"limit"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	var queriesToRun []string
	queriesToRun = append(queriesToRun, parseStringOrArray(params.Queries)...)
	queriesToRun = append(queriesToRun, parseStringOrArray(params.Query)...)

	if len(queriesToRun) == 0 {
		return &types.ToolResult{Success: false, Error: "Missing 'queries' parameter"}, nil
	}

	if params.Limit <= 0 {
		params.Limit = 10
	}

	// Restrict scopes by knowledge_base_id arg if provided.
	effectiveScopes := t.scopes
	if params.KnowledgeBaseID != "" {
		filtered := make([]WikiScope, 0, 1)
		for _, sc := range t.scopes {
			if sc.KnowledgeBaseID == params.KnowledgeBaseID {
				filtered = append(filtered, sc)
				break
			}
		}
		if len(filtered) == 0 {
			filtered = append(filtered, WikiScope{KnowledgeBaseID: params.KnowledgeBaseID})
		}
		effectiveScopes = filtered
	}

	var allOutputs []string
	// Per-slug list of KB IDs that produced a match. Multiple KBs may share a
	// slug when the agent has several wiki KBs in scope, so we keep the full list.
	foundKBs := make(map[string][]string)

	type searchHit struct {
		page *types.WikiPage
		kbID string
	}

	for _, query := range queriesToRun {
		var allHits []searchHit
		filteredCount := 0
		for _, sc := range effectiveScopes {
			kbID := sc.KnowledgeBaseID
			if kbID == "" {
				continue
			}
			allowed, hasFilter := scopeKnowledgeFilter(sc)

			pages, err := t.wikiService.SearchPages(ctx, kbID, query, params.Limit)
			if err != nil {
				continue
			}
			for _, p := range pages {
				if p == nil {
					continue
				}
				if hasFilter && !pageIntersectsKnowledgeIDs(p, allowed) {
					filteredCount++
					continue
				}
				actualKBID := kbID
				if p.KnowledgeBaseID != "" {
					actualKBID = p.KnowledgeBaseID
				}
				allHits = append(allHits, searchHit{page: p, kbID: actualKBID})
				foundKBs[p.Slug] = append(foundKBs[p.Slug], actualKBID)
				// Register neighbour slugs so links surfaced from this
				// page's body can be routed to the same KB by the frontend.
				registerLinkedSlugs(foundKBs, p, actualKBID)
			}
		}
		_ = filteredCount // reserved for future debug surface

		if len(allHits) == 0 {
			allOutputs = append(allOutputs, fmt.Sprintf("<search_results count=\"0\" query=\"%s\" />", query))
			continue
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "<search_results count=\"%d\" query=\"%s\">\n", len(allHits), query)
		for _, h := range allHits {
			p := h.page
			key := seenLinkKey(h.kbID, p.Slug)
			t.mu.Lock()
			seen := t.seenSlugs[key]
			t.seenSlugs[key] = true
			t.mu.Unlock()

			snippet := extractSnippet(p.Content, query)
			snippetTag := ""
			if snippet != "" {
				snippetTag = fmt.Sprintf("\n<match_snippet>%s</match_snippet>", snippet)
			}

			aliasesTag := ""
			if len(p.Aliases) > 0 {
				aliasesTag = fmt.Sprintf("\n<aliases>%s</aliases>", strings.Join(p.Aliases, ", "))
			}

			summary := p.Summary
			if seen {
				summary = "(summary omitted, already seen in previous search)"
			}
			fmt.Fprintf(&sb,
				"<page>\n<knowledge_base_id>%s</knowledge_base_id>\n<link>[[%s|%s]]</link>\n<type>%s</type>%s\n<summary>%s</summary>%s\n</page>\n",
				h.kbID, p.Slug, p.Title, p.PageType, aliasesTag, summary, snippetTag,
			)
		}
		sb.WriteString("</search_results>")
		allOutputs = append(allOutputs, sb.String())
	}

	return &types.ToolResult{
		Success: true,
		Output:  strings.Join(allOutputs, "\n\n"),
		Data: map[string]interface{}{
			"found_kbs": foundKBs,
		},
	}, nil
}

// --- Helper ---

func truncateForSummary(content string, maxLen int) string {
	// Take first paragraph or first maxLen chars
	lines := strings.SplitN(content, "\n\n", 2)
	summary := strings.TrimSpace(lines[0])
	summary = strings.TrimPrefix(summary, "# ")
	summary = strings.TrimPrefix(summary, "## ")
	runes := []rune(summary)
	if len(runes) > maxLen {
		return string(runes[:maxLen]) + "..."
	}
	return summary
}

func parseStringOrArray(val any) []string {
	if val == nil {
		return nil
	}
	switch v := val.(type) {
	case string:
		if v != "" {
			return []string{v}
		}
	case []interface{}:
		var res []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				res = append(res, s)
			}
		}
		return res
	}
	return nil
}

// resolveSourceRefs enriches plain knowledge UUIDs to "uuid|title" format.
// Refs already in "uuid|title" format are left unchanged.
func resolveSourceRefs(ctx context.Context, knowledgeService interfaces.KnowledgeService, refs []string) []string {
	if len(refs) == 0 || knowledgeService == nil {
		return refs
	}
	resolved := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.Contains(ref, "|") {
			resolved = append(resolved, ref)
			continue
		}
		kn, err := knowledgeService.GetKnowledgeByIDOnly(ctx, ref)
		if err != nil || kn == nil {
			resolved = append(resolved, ref)
			continue
		}
		title := kn.Title
		if title == "" {
			title = kn.FileName
		}
		if title != "" {
			resolved = append(resolved, ref+"|"+title)
		} else {
			resolved = append(resolved, ref)
		}
	}
	return resolved
}

func extractSnippet(content string, query string) string {
	if content == "" || query == "" {
		return ""
	}
	re, err := regexp.Compile("(?i)" + query)
	if err != nil {
		return ""
	}
	loc := re.FindStringIndex(content)
	if loc == nil {
		return ""
	}

	matchStr := content[loc[0]:loc[1]]
	before := content[:loc[0]]
	after := content[loc[1]:]

	beforeRunes := []rune(before)
	if len(beforeRunes) > 60 {
		beforeRunes = beforeRunes[len(beforeRunes)-60:]
	}

	afterRunes := []rune(after)
	if len(afterRunes) > 60 {
		afterRunes = afterRunes[:60]
	}

	matchRunes := []rune(matchStr)
	if len(matchRunes) > 100 {
		matchRunes = append(matchRunes[:100], []rune("...")...)
	}

	snippet := string(beforeRunes) + string(matchRunes) + string(afterRunes)
	snippet = strings.ReplaceAll(snippet, "\n", " ")
	for strings.Contains(snippet, "  ") {
		snippet = strings.ReplaceAll(snippet, "  ", " ")
	}

	return "... " + strings.TrimSpace(snippet) + " ..."
}

func truncateRunes(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
