package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// wikiIndexAgentTopK is the per-type cap applied when synthesizing the
// index overview for wiki_read_page('index'). The agent uses the overview
// to get a sense of the wiki's shape; any deeper exploration should go
// through wiki_search. Keeping the cap small bounds the content envelope
// served to the LLM, which is the main reason this synthesis exists.
const wikiIndexAgentTopK = 20

// renderIndexOverviewForAgent formats a WikiIndexResponse as a compact
// markdown block suitable for embedding inside the <content> tag that
// wiki_read_page returns. We deliberately keep the structure almost
// identical to the legacy "intro + ## Type (N)\n[[slug]] — summary"
// markdown so existing agent prompts (prompts_wiki.go) that reason
// about index layouts do not need retraining.
func renderIndexOverviewForAgent(resp *types.WikiIndexResponse) string {
	var sb strings.Builder
	// Intro may still carry a legacy inline directory on KBs that
	// haven't been re-ingested since the index refactor — clip
	// everything from the first "\n## " heading onwards so the model
	// doesn't see the old directory alongside the live top-K below.
	intro := strings.TrimSpace(resp.Intro)
	if idx := strings.Index(intro, "\n## "); idx >= 0 {
		intro = strings.TrimSpace(intro[:idx])
	}
	if intro != "" {
		sb.WriteString(intro)
		sb.WriteString("\n")
	}

	typeLabels := map[string]string{
		types.WikiPageTypeSummary:    "Summary",
		types.WikiPageTypeEntity:     "Entity",
		types.WikiPageTypeConcept:    "Concept",
		types.WikiPageTypeSynthesis:  "Synthesis",
		types.WikiPageTypeComparison: "Comparison",
	}

	nonEmpty := 0
	for _, g := range resp.Groups {
		if g.Total == 0 {
			continue
		}
		label := typeLabels[g.Type]
		if label == "" {
			label = g.Type
		}
		if int64(len(g.Items)) < g.Total {
			fmt.Fprintf(&sb, "\n## %s (%d total, showing top %d)\n\n", label, g.Total, len(g.Items))
		} else {
			fmt.Fprintf(&sb, "\n## %s (%d)\n\n", label, g.Total)
		}
		for _, item := range g.Items {
			// Emit [[slug|title]] so the LLM sees the human-readable
			// name next to the slug it needs for downstream tool calls
			// (wiki_read_page, wiki_search by title, etc.). Falling back
			// to the slug when a page has no title keeps the wiki-link
			// syntactically valid either way.
			display := item.Title
			if display == "" {
				display = item.Slug
			}
			if item.Summary != "" {
				fmt.Fprintf(&sb, "[[%s|%s]] — %s\n", item.Slug, display, item.Summary)
			} else {
				fmt.Fprintf(&sb, "[[%s|%s]]\n", item.Slug, display)
			}
		}
		nonEmpty++
	}

	if nonEmpty == 0 {
		sb.WriteString("\n*No wiki pages yet. Upload documents to get started.*\n")
	} else {
		sb.WriteString("\n_To explore more pages under any category, use wiki_search with a query, or read a specific slug directly._\n")
	}
	return sb.String()
}

// ---- wiki_read_page ----

// WikiScope describes the effective retrieval scope for a single wiki
// knowledge base within an agent session.
//
//   - KnowledgeBaseID: the wiki KB to search.
//   - KnowledgeIDs: OPTIONAL whitelist of source knowledge (document) IDs.
//     When non-empty, a wiki page is only surfaced if its SourceRefs
//     intersect this set. Used to honour user-level @mentions that pin the
//     search to specific documents inside a KB.
//   - TagIDs: OPTIONAL whitelist of document tags. When non-empty, a wiki page
//     is only surfaced if at least one of its SourceRefs belongs to any tag.
type WikiScope struct {
	KnowledgeBaseID string
	KnowledgeIDs    []string
	TagIDs          []string
}

// NewWikiScopesFromKBIDs is a convenience constructor for callers that only
// carry plain KB IDs and don't need per-document filtering (e.g. legacy tests).
func NewWikiScopesFromKBIDs(kbIDs []string) []WikiScope {
	// Deduplicate defensively: resolveUniqueWikiPage counts one hit per scope
	// as a distinct owner, so a duplicated KB ID would misreport a unique slug
	// as ambiguous.
	deduped := dedupNonEmptyStrings(kbIDs)
	scopes := make([]WikiScope, 0, len(deduped))
	for _, id := range deduped {
		scopes = append(scopes, WikiScope{KnowledgeBaseID: id})
	}
	return scopes
}

// NewWikiScopesFromSearchTargets merges all targets for a Wiki KB into one
// scope. SearchTargets are alternatives selected by the user, so document and
// tag constraints are combined as a union; an unrestricted whole-KB target
// supersedes narrower targets for that KB.
func NewWikiScopesFromSearchTargets(searchTargets types.SearchTargets, wikiKBIDs []string) []WikiScope {
	allowed := make(map[string]struct{}, len(wikiKBIDs))
	for _, kbID := range dedupNonEmptyStrings(wikiKBIDs) {
		allowed[kbID] = struct{}{}
	}
	type accumulatedScope struct {
		WikiScope
		unrestricted bool
	}
	byKB := make(map[string]*accumulatedScope, len(allowed))
	for _, target := range searchTargets {
		if target == nil {
			continue
		}
		if _, ok := allowed[target.KnowledgeBaseID]; !ok {
			continue
		}
		targetKnowledgeIDs, targetTagIDs := searchTargetScope(target)
		wholeKB := searchTargetIsWholeKB(target)
		if !wholeKB && len(targetKnowledgeIDs) == 0 && len(targetTagIDs) == 0 {
			// A malformed empty document target must not silently become
			// whole-KB authorization.
			continue
		}
		scope := byKB[target.KnowledgeBaseID]
		if scope == nil {
			scope = &accumulatedScope{WikiScope: WikiScope{KnowledgeBaseID: target.KnowledgeBaseID}}
			byKB[target.KnowledgeBaseID] = scope
		}
		if wholeKB {
			scope.unrestricted = true
			continue
		}
		scope.KnowledgeIDs = append(scope.KnowledgeIDs, targetKnowledgeIDs...)
		scope.TagIDs = append(scope.TagIDs, targetTagIDs...)
	}

	scopes := make([]WikiScope, 0, len(allowed))
	for _, kbID := range dedupNonEmptyStrings(wikiKBIDs) {
		scope := byKB[kbID]
		if scope == nil {
			continue
		}
		if scope.unrestricted {
			scopes = append(scopes, WikiScope{KnowledgeBaseID: kbID})
			continue
		}
		scope.KnowledgeIDs = dedupNonEmptyStrings(scope.KnowledgeIDs)
		scope.TagIDs = dedupNonEmptyStrings(scope.TagIDs)
		scopes = append(scopes, scope.WikiScope)
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

// isStructuralPage reports whether a page is the wiki-level index rather than
// a content page tied to specific source documents. The index is never
// filtered by knowledge_ids scope because it describes wiki topology.
func isStructuralPage(page *types.WikiPage) bool {
	if page == nil {
		return false
	}
	return page.PageType == types.WikiPageTypeIndex
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

// pageIntersectsKnowledgeIDs reports whether at least one of the page's source
// documents is in the allowed whitelist. Callers must already have established
// that the page has attributable provenance; pagePassesWikiScope owns the
// fail-closed handling of structural and uncited pages.
func pageIntersectsKnowledgeIDs(page *types.WikiPage, allowed map[string]bool) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, kid := range extractSourceKnowledgeIDs(page) {
		if allowed[kid] {
			return true
		}
	}
	return false
}

func pagePassesWikiScope(
	ctx context.Context,
	page *types.WikiPage,
	scope WikiScope,
	fetchTags knowledgeTagsFetcher,
) (bool, error) {
	allowed, hasKnowledgeFilter := scopeKnowledgeFilter(scope)
	tagIDs := dedupNonEmptyStrings(scope.TagIDs)
	if !hasKnowledgeFilter && len(tagIDs) == 0 {
		return true, nil
	}
	// Under a document/tag-constrained scope, every surfaced page must prove
	// provenance. Structural pages and uncited pages describe the whole Wiki
	// and cannot be safely attributed to the selected subset; wiki_search is
	// still available for scoped discovery.
	if isStructuralPage(page) {
		return false, nil
	}
	sourceKnowledgeIDs := extractSourceKnowledgeIDs(page)
	if len(sourceKnowledgeIDs) == 0 {
		return false, nil
	}
	if hasKnowledgeFilter && pageIntersectsKnowledgeIDs(page, allowed) {
		return true, nil
	}
	if len(tagIDs) == 0 {
		return false, nil
	}
	matches, err := knowledgeIDsMatchingAnyTag(ctx, sourceKnowledgeIDs, tagIDs, fetchTags)
	if err != nil {
		return false, err
	}
	return len(matches) > 0, nil
}

const (
	// wikiMaxLinkSummaries bounds how many neighbour summaries are inlined per
	// link section, and wikiLinkSummaryMaxRunes bounds each one. A hub page can
	// carry dozens of links, and inlining every full summary used to spend more
	// of the output budget than the page bodies the caller asked for.
	wikiMaxLinkSummaries    = 20
	wikiLinkSummaryMaxRunes = 150

	// wikiMinPageBody is the smallest body slice worth rendering. When the
	// budget cannot give every resolved page at least this much, trailing pages
	// are named in <omitted_pages> instead of being cut mid-tag: a half-rendered
	// <wiki_page> is unparseable for the UI and silently misleading to the model.
	wikiMinPageBody = 400

	// wikiBudgetReserve holds back room for the <errors> and <omitted_pages>
	// trailers appended after the pages are rendered.
	wikiBudgetReserve = 600
)

// pendingWikiPage is a resolved page whose neighbour summaries, sources and
// body have been gathered but whose rendered size is not yet decided.
type pendingWikiPage struct {
	page     *types.WikiPage
	kbID     string
	outLinks []string
	inLinks  []string
	sources  []string
	body     string
}

func (p pendingWikiPage) render(body string) string {
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
		p.kbID,
		p.page.Slug, p.page.Title, p.page.PageType,
		strings.Join(p.page.Aliases, ", "),
		strings.Join(p.outLinks, ", "),
		strings.Join(p.inLinks, ", "),
		strings.Join(p.sources, "\n"),
		p.page.Summary,
		body,
	)
}

// renderWikiPagesWithinBudget renders a batch of pages so that the result fits
// the tool output ceiling with every page still well-formed. Pages are trimmed
// by fair-sharing the body budget, and only when even a minimal body no longer
// fits are trailing pages dropped — by name, so the model knows to re-read them.
//
// It returns the joined output plus the slugs whose body was trimmed and the
// slugs that were not rendered at all.
func renderWikiPagesWithinBudget(pages []pendingWikiPage, budget int) (string, []string, []string) {
	if len(pages) == 0 {
		return "", nil, nil
	}

	const separator = "\n\n"
	separatorCost := utf8.RuneCountInString(separator)

	rendered := make([]string, len(pages))
	bodySizes := make([]int, len(pages))
	overheads := make([]int, len(pages))
	total := separatorCost * (len(pages) - 1)
	for i, p := range pages {
		rendered[i] = p.render(p.body)
		size := utf8.RuneCountInString(rendered[i])
		bodySizes[i] = utf8.RuneCountInString(p.body)
		overheads[i] = size - bodySizes[i]
		total += size
	}

	usable := budget - wikiBudgetReserve
	if usable <= 0 || total <= usable {
		return strings.Join(rendered, separator), nil, nil
	}

	fixedCost := func(keep int) int {
		cost := separatorCost * (keep - 1)
		for i := 0; i < keep; i++ {
			cost += overheads[i]
		}
		return cost
	}

	keep := len(pages)
	for keep > 1 && fixedCost(keep)+keep*wikiMinPageBody > usable {
		keep--
	}
	var omitted []string
	for _, p := range pages[keep:] {
		omitted = append(omitted, p.page.Slug)
	}

	caps := splitBudgetFairly(usable-fixedCost(keep), bodySizes[:keep])
	outputs := make([]string, keep)
	var truncated []string
	for i := 0; i < keep; i++ {
		if caps[i] >= bodySizes[i] {
			outputs[i] = rendered[i]
			continue
		}
		body := "(body omitted: output budget exhausted)"
		if caps[i] > 0 {
			body = TruncateToolOutput(pages[i].body, caps[i])
		}
		outputs[i] = pages[i].render(body)
		truncated = append(truncated, pages[i].page.Slug)
	}
	return strings.Join(outputs, separator), truncated, omitted
}

type wikiReadPageTool struct {
	BaseTool
	wikiService      interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	scopes           []WikiScope
	routes           *WikiRouteResolver
	seenLinks        map[string]bool
	mu               sync.Mutex
}

func NewWikiReadPageTool(
	wikiService interfaces.WikiPageService,
	knowledgeService interfaces.KnowledgeService,
	scopes []WikiScope,
	routes *WikiRouteResolver,
) types.Tool {
	if routes == nil {
		routes = NewWikiRouteResolver()
	}
	return &wikiReadPageTool{
		BaseTool: NewBaseTool(
			ToolWikiReadPage,
			`Read one or more wiki pages by their slugs. Returns the full markdown content, metadata, and links.
Use this to read specific wiki pages when you know their slug (e.g. "entity/acme-corp", "concept/rag").
Knowledge-base routing is automatic. Known link/search provenance is preferred; otherwise every wiki knowledge base in scope is checked and ambiguous matches are returned.`,
			json.RawMessage(`{
  "type": "object",
  "properties": {
    "slugs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "List of wiki page slugs to read (e.g. ['entity/acme-corp', 'index'])"
    }
  },
  "required": ["slugs"]
}`),
		),
		wikiService:      wikiService,
		knowledgeService: knowledgeService,
		scopes:           scopes,
		routes:           routes,
		seenLinks:        make(map[string]bool),
	}
}

// seenLinkKey builds a dedupe key scoped to a knowledge base so that identical
// slugs from different KBs are not collapsed into a single "already seen" entry.
func seenLinkKey(kbID, slug string) string {
	return kbID + "\x00" + slug
}

func (t *wikiReadPageTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Slug  any `json:"slug"`
		Slugs any `json:"slugs"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}

	var slugsToFetch []string
	slugsToFetch = append(slugsToFetch, parseStringOrArray(params.Slugs)...)
	slugsToFetch = append(slugsToFetch, parseStringOrArray(params.Slug)...)
	// A slug repeated inside `slugs`, or echoed in both `slug` and `slugs`,
	// would otherwise be rendered twice and spend budget a distinct page needs.
	slugsToFetch = dedupNonEmptyStrings(slugsToFetch)

	if len(slugsToFetch) == 0 {
		return &types.ToolResult{Success: false, Error: "Missing 'slugs' parameter"}, nil
	}

	var pending []pendingWikiPage
	var errs []string
	// Per-slug list of KB IDs where the slug was found. A slug may exist in
	// multiple KBs when the agent has several wiki KBs in scope.
	foundKBs := make(map[string][]string)

	formatLinks := func(slugs []string, kbID string) []string {
		var descs []string
		inlined := 0
		for _, s := range slugs {
			if s == "" {
				continue
			}
			// Past the cap the slug alone is enough: the model can always read
			// a neighbour explicitly, whereas an unbounded link section costs
			// one query per entry and crowds out the bodies actually requested.
			if inlined >= wikiMaxLinkSummaries {
				descs = append(descs, fmt.Sprintf("[[%s]]", s))
				continue
			}

			key := seenLinkKey(kbID, s)
			t.mu.Lock()
			seen := t.seenLinks[key]
			t.mu.Unlock()
			if seen {
				// We already injected the summary for this link in this session (within the same KB)
				descs = append(descs, fmt.Sprintf("[[%s]] (summary omitted, already seen)", s))
				continue
			}

			linkPage, err := t.wikiService.GetPageBySlug(ctx, kbID, s)
			if err != nil || linkPage == nil || linkPage.Summary == "" {
				descs = append(descs, fmt.Sprintf("[[%s]]", s))
				continue
			}
			descs = append(descs, fmt.Sprintf("[[%s]] (%s)", s, truncateRunes(linkPage.Summary, wikiLinkSummaryMaxRunes)))
			inlined++
			t.mu.Lock()
			t.seenLinks[key] = true
			t.mu.Unlock()
		}
		if len(descs) == 0 {
			return []string{"(none)"}
		}
		return descs
	}

	// resolvePage gathers everything needed to render a page except its final
	// size. Rendering is deferred until every requested slug has been resolved
	// so the output budget can be shared across the whole batch.
	resolvePage := func(page *types.WikiPage, kbID string) pendingWikiPage {
		resolved := pendingWikiPage{
			page:     page,
			kbID:     kbID,
			outLinks: formatLinks(page.OutLinks, kbID),
			inLinks:  formatLinks(page.InLinks, kbID),
			body:     page.Content,
		}

		for _, ref := range page.SourceRefs {
			// SourceRefs might be "knowledgeID" or "knowledgeID|Title"
			kid := ref
			title := ""
			if pipeIdx := strings.Index(ref, "|"); pipeIdx > 0 {
				kid = ref[:pipeIdx]
				title = ref[pipeIdx+1:]
			}
			if title != "" {
				resolved.sources = append(resolved.sources, fmt.Sprintf(`<source knowledge_id="%s">%s</source>`, kid, title))
			} else {
				resolved.sources = append(resolved.sources, fmt.Sprintf(`<source knowledge_id="%s"/>`, kid))
			}
		}

		// Index page special-case. wiki_pages.content used to hold
		// "intro + full directory" markdown; on big KBs that was a
		// multi-MB blob the LLM had no hope of reading end-to-end.
		// After the index refactor, content holds only the intro, and
		// the directory is assembled on demand. Surface a bounded
		// top-K overview so the agent still sees the wiki's shape
		// without overflowing its context window — the explicit hint
		// steers the model to wiki_search for deeper exploration.
		if page.PageType == types.WikiPageTypeIndex {
			if overview, err := t.wikiService.GetIndexView(ctx, kbID, nil, wikiIndexAgentTopK, ""); err == nil && overview != nil {
				resolved.body = renderIndexOverviewForAgent(overview)
				for _, group := range overview.Groups {
					for _, item := range group.Items {
						t.routes.remember(item.Slug, kbID)
					}
				}
			}
		}

		return resolved
	}

	// Track slugs that were found in the raw lookup but filtered out by a
	// knowledge_ids whitelist, so we can surface a clearer error.
	filteredOut := make(map[string][]string) // slug -> list of KB IDs where filtered
	lookupFailed := make(map[string]bool)
	var fetchTags knowledgeTagsFetcher
	if t.knowledgeService != nil {
		fetchTags = t.knowledgeService.GetKnowledgeTags
	}
	for _, slug := range slugsToFetch {
		var hits []struct {
			page *types.WikiPage
			kbID string
		}
		cachedScopes := t.routes.scopesForSlug(slug, t.scopes)
		// Provenance is an ordering hint, never an authority boundary. Always
		// inspect every legal scope so a cached hit cannot hide a duplicate slug
		// in another Wiki KB and make the model act on an apparently unique page.
		effectiveScopes := append(
			append([]WikiScope(nil), cachedScopes...),
			scopesOutsideKBs(t.scopes, cachedScopes)...,
		)

		lookupScopes := func(scopes []WikiScope) {
			for _, sc := range scopes {
				kbID := sc.KnowledgeBaseID
				if kbID == "" {
					continue
				}
				page, err := t.wikiService.GetPageBySlug(ctx, kbID, slug)
				if err != nil {
					if !errors.Is(err, repository.ErrWikiPageNotFound) {
						lookupFailed[slug] = true
						errs = append(errs, fmt.Sprintf("Failed to read Wiki page '%s' in KB %s: %v", slug, kbID, err))
					}
					continue
				}
				if page == nil {
					continue
				}
				if page.KnowledgeBaseID != "" && page.KnowledgeBaseID != kbID {
					lookupFailed[slug] = true
					errs = append(errs, fmt.Sprintf(
						"Wiki page '%s' returned KB %s while resolving allowed KB %s",
						slug, page.KnowledgeBaseID, kbID,
					))
					continue
				}
				actualKBID := kbID

				// Apply server-enforced source-document / tag scope (silent; never
				// exposed to the model as a tool argument).
				passesScope, scopeErr := pagePassesWikiScope(ctx, page, sc, fetchTags)
				if scopeErr != nil {
					errs = append(errs, fmt.Sprintf("Failed to validate wiki scope for '%s' in KB %s: %v", slug, actualKBID, scopeErr))
					continue
				}
				if !passesScope {
					filteredOut[slug] = append(filteredOut[slug], actualKBID)
					continue
				}

				hits = append(hits, struct {
					page *types.WikiPage
					kbID string
				}{page, actualKBID})
				t.routes.rememberPage(page, actualKBID)
				foundKBs[slug] = append(foundKBs[slug], actualKBID)
				// Also register the page's neighbours so that when the model
				// echoes a link like `[[summary/xyz]]` from this page's body,
				// the frontend can resolve it to the same KB without guessing.
				registerLinkedSlugs(foundKBs, page, actualKBID)
				t.mu.Lock()
				t.seenLinks[seenLinkKey(actualKBID, slug)] = true
				t.mu.Unlock()
			}
		}

		lookupScopes(effectiveScopes)

		if len(hits) == 0 {
			if kbs := filteredOut[slug]; len(kbs) > 0 {
				errs = append(errs, fmt.Sprintf(
					"Wiki page '%s' exists in %v but none of its source documents are within the scope pinned by the user",
					slug, kbs,
				))
			} else if !lookupFailed[slug] {
				errs = append(errs, fmt.Sprintf("Wiki page '%s' not found", slug))
			}
			continue
		}

		// When routing remains ambiguous, emit every matched page so the model
		// can use or compare them explicitly.
		for _, h := range hits {
			pending = append(pending, resolvePage(h.page, h.kbID))
		}
	}

	if len(pending) == 0 {
		return &types.ToolResult{Success: false, Error: strings.Join(errs, "; ")}, nil
	}

	finalOutput, truncatedSlugs, omittedSlugs := renderWikiPagesWithinBudget(pending, OutputBudget(ctx))
	if len(omittedSlugs) > 0 {
		finalOutput += fmt.Sprintf(
			"\n\n<omitted_pages reason=\"output budget exceeded\">\n%s\n</omitted_pages>"+
				"\n<hint>These pages were resolved but not rendered. "+
				"Call wiki_read_page again with fewer slugs to read them.</hint>",
			strings.Join(omittedSlugs, "\n"),
		)
	}
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
			"truncated_slugs": truncatedSlugs,
			"omitted_slugs":   omittedSlugs,
		},
	}, nil
}

// ---- wiki_search ----

type wikiSearchTool struct {
	BaseTool
	wikiService      interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	scopes           []WikiScope
	routes           *WikiRouteResolver
	seenSlugs        map[string]bool
	mu               sync.Mutex
}

func NewWikiSearchTool(
	wikiService interfaces.WikiPageService,
	knowledgeService interfaces.KnowledgeService,
	scopes []WikiScope,
	routes *WikiRouteResolver,
) types.Tool {
	if routes == nil {
		routes = NewWikiRouteResolver()
	}
	return &wikiSearchTool{
		BaseTool: NewBaseTool(
			ToolWikiSearch,
			`Search wiki pages using case-insensitive regular expressions.
STRONGLY PREFER using regex to search for multiple concepts at once rather than simple plain text queries.
Returns matching pages with titles, slugs, and summaries (each tagged with its short bN knowledge_base_id).
Examples:
- Alternation (RECOMMENDED): "stardust|skyvault" (matches either word)
- Multiple terms (RECOMMENDED): "psionic.*engine" (matches both words in order)
- Prefix matching: "^entity/.*" (finds all entities)
- Plain text: "engine" (matches anywhere in title/content/slug/summary)
Use the portable syntax shared by supported databases: literals, character ranges, grouping, alternation,
anchors, and quantifiers. Do not use engine-specific escapes such as \d, \s, \w, \b or constructs beginning with "(?".
IMPORTANT — JSON escaping: every backslash in a regex MUST be written as \\ inside the JSON tool arguments.
For example, to search for literal "C++" write "C\\+\\+", NOT "C\+\+".
Plain "\+" is an invalid JSON escape and will fail to parse.
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
      "description": "Optional: restrict search to a single short bN knowledge base ID in scope."
    }
  },
  "required": ["queries"]
}`),
		),
		wikiService:      wikiService,
		knowledgeService: knowledgeService,
		scopes:           scopes,
		routes:           routes,
		seenSlugs:        make(map[string]bool),
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
	for _, query := range queriesToRun {
		if _, err := compilePortableCaseInsensitiveRegex(query); err != nil {
			return &types.ToolResult{Success: false, Error: err.Error()}, err
		}
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
			return &types.ToolResult{
				Success: false,
				Error:   "knowledge_base_id is not within the current wiki scope",
			}, nil
		}
		effectiveScopes = filtered
	}

	var allOutputs []string
	var searchErrors []string
	successfulSearchCalls := 0
	// Per-slug list of KB IDs that produced a match. Multiple KBs may share a
	// slug when the agent has several wiki KBs in scope, so we keep the full list.
	foundKBs := make(map[string][]string)

	type searchHit struct {
		page *types.WikiPage
		kbID string
	}
	var fetchTags knowledgeTagsFetcher
	if t.knowledgeService != nil {
		fetchTags = t.knowledgeService.GetKnowledgeTags
	}

	for _, query := range queriesToRun {
		var allHits []searchHit
		filteredCount := 0
		for _, sc := range effectiveScopes {
			kbID := sc.KnowledgeBaseID
			if kbID == "" {
				continue
			}
			pages, err := t.wikiService.SearchPages(ctx, kbID, query, params.Limit)
			if err != nil {
				searchErrors = append(searchErrors, fmt.Sprintf("Wiki search %q failed in KB %s: %v", query, kbID, err))
				continue
			}
			successfulSearchCalls++
			for _, p := range pages {
				if p == nil {
					continue
				}
				passesScope, scopeErr := pagePassesWikiScope(ctx, p, sc, fetchTags)
				if scopeErr != nil {
					searchErrors = append(searchErrors, fmt.Sprintf(
						"Failed to validate Wiki search result %q in KB %s: %v", p.Slug, kbID, scopeErr,
					))
					continue
				}
				if !passesScope {
					filteredCount++
					continue
				}
				if p.KnowledgeBaseID != "" && p.KnowledgeBaseID != kbID {
					searchErrors = append(searchErrors, fmt.Sprintf(
						"Wiki search result %q returned KB %s while resolving allowed KB %s",
						p.Slug, p.KnowledgeBaseID, kbID,
					))
					continue
				}
				actualKBID := kbID
				allHits = append(allHits, searchHit{page: p, kbID: actualKBID})
				t.routes.rememberPage(p, actualKBID)
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

	if successfulSearchCalls == 0 && len(searchErrors) > 0 {
		return &types.ToolResult{Success: false, Error: strings.Join(searchErrors, "; ")}, nil
	}
	output := strings.Join(allOutputs, "\n\n")
	if len(searchErrors) > 0 {
		output += "\n\n<errors>\n" + strings.Join(searchErrors, "\n") + "\n</errors>"
	}
	return &types.ToolResult{
		Success: true,
		Output:  output,
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
	re, err := compilePortableCaseInsensitiveRegex(query)
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
