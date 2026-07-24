package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	wikiNavigateDefaultLimit = 8
	wikiNavigateMaxLimit     = 12
	wikiNavigateSearchPool   = 32
	wikiNavigateIndexLimit   = 200
	wikiNavigateNextHopLimit = 12
	wikiNavigateIndexDefault = 6
	wikiNavigateIndexFocused = 24
	wikiNavigateIndexOther   = 3
)

const (
	wikiNavigateQueryModeLiteral = "literal"
	wikiNavigateQueryModeRegex   = "regex"
)

type wikiNavigateTool struct {
	BaseTool
	wikiService      interfaces.WikiPageService
	knowledgeService interfaces.KnowledgeService
	scopes           []WikiScope
}

type wikiNavigationCandidate struct {
	page      *types.WikiPage
	kbID      string
	slug      string
	title     string
	pageType  string
	summary   string
	score     int
	reasons   []string
	snippet   string
	fromIndex bool
	outLinks  []string
	inLinks   []string
}

type wikiNavigationIndexOverview struct {
	kbID   string
	groups []types.WikiIndexGroup
}

func NewWikiNavigateTool(
	wikiService interfaces.WikiPageService,
	knowledgeService interfaces.KnowledgeService,
	scopes []WikiScope,
) types.Tool {
	return &wikiNavigateTool{
		BaseTool: NewBaseTool(
			ToolWikiNavigate,
			`Navigate a wiki like an Obsidian vault: inspect the KB index, categories, wikilinks, and page graph to find candidate Wiki links.
Use this as the first step when you do not know the exact slug. It always returns a lightweight index_overview before recommendations.
After this tool returns, call wiki_read_page on the most relevant recommended slugs, then follow links_to / linked_from as needed.`,
			json.RawMessage(`{
  "type": "object",
  "required": ["query"],
  "properties": {
    "query": {
      "type": "string",
      "description": "Natural keyword, title, alias, product name, symptom, or concept to navigate from."
    },
    "query_mode": {
      "type": "string",
      "enum": ["literal", "regex"],
      "description": "Optional search interpretation for query. Default literal safely escapes regex metacharacters. Use regex only for explicit alternation, prefixes, ordered terms, or exact code/model patterns."
    },
    "limit": {
      "type": "integer",
      "description": "Max recommended pages to return (default 8, max 12)."
    },
    "max_hops": {
      "type": "integer",
      "description": "Reserved for graph expansion depth. MVP returns direct next hops only."
    },
    "knowledge_base_id": {
      "type": "string",
      "description": "Optional: restrict navigation to a single knowledge base ID in scope."
    },
    "page_types": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional one-or-more KB-local page types to drill into or softly prioritize. Omit page_types to keep an all-category view. The index_overview category totals are never restricted by this parameter."
    }
  }
}`),
		),
		wikiService:      wikiService,
		knowledgeService: knowledgeService,
		scopes:           scopes,
	}
}

func (t *wikiNavigateTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	var params struct {
		Query           string `json:"query"`
		QueryMode       string `json:"query_mode"`
		Limit           int    `json:"limit"`
		MaxHops         int    `json:"max_hops"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
		PageTypes       any    `json:"page_types"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &types.ToolResult{Success: false, Error: "Invalid parameters: " + err.Error()}, nil
	}
	params.Query = strings.TrimSpace(params.Query)
	if params.Query == "" {
		return &types.ToolResult{Success: false, Error: "Missing query"}, nil
	}
	queryMode, err := normalizeWikiNavigateQueryMode(params.QueryMode)
	if err != nil {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	if params.Limit <= 0 {
		params.Limit = wikiNavigateDefaultLimit
	}
	if params.Limit > wikiNavigateMaxLimit {
		params.Limit = wikiNavigateMaxLimit
	}

	effectiveScopes := filterWikiScopesByKB(t.scopes, params.KnowledgeBaseID)
	candidates := make(map[string]*wikiNavigationCandidate)
	foundKBs := make(map[string][]string)
	overviews := t.collectIndexOverviews(ctx, effectiveScopes, foundKBs)
	pageTypes, warnings := resolveWikiNavigateFocusPageTypes(parseWikiNavigatePageTypes(params.PageTypes), overviews)
	searchQuery := wikiNavigateSearchQuery(params.Query, queryMode)

	var fetchTags knowledgeTagsFetcher
	if t.knowledgeService != nil {
		fetchTags = t.knowledgeService.GetKnowledgeTags
	}
	for _, scope := range effectiveScopes {
		kbID := scope.KnowledgeBaseID
		if kbID == "" {
			continue
		}
		for _, overview := range overviews {
			if overview.kbID == kbID {
				collectIndexCandidatesFromOverview(overview, pageTypes, params.Query, candidates)
				break
			}
		}
		t.collectSearchCandidates(ctx, scope, fetchTags, params.Query, searchQuery, candidates, foundKBs)
	}

	ranked := rankedWikiNavigationCandidates(candidates)
	if len(ranked) > params.Limit {
		ranked = ranked[:params.Limit]
	}
	for _, c := range ranked {
		t.ensureCandidatePage(ctx, c)
		if c.page != nil {
			registerLinkedSlugs(foundKBs, c.page, c.kbID)
		}
		addFoundKB(foundKBs, c.slug, c.kbID)
	}

	nextHops := t.collectNextHops(ctx, ranked, params.Query, foundKBs)
	output := renderWikiNavigationResults(params.Query, overviews, pageTypes, warnings, ranked, nextHops)

	return &types.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]any{
			"found_kbs": foundKBs,
			"warnings":  warnings,
		},
	}, nil
}

func (t *wikiNavigateTool) collectIndexOverviews(ctx context.Context, scopes []WikiScope, foundKBs map[string][]string) []wikiNavigationIndexOverview {
	overviews := make([]wikiNavigationIndexOverview, 0, len(scopes))
	var fetchTags knowledgeTagsFetcher
	if t.knowledgeService != nil {
		fetchTags = t.knowledgeService.GetKnowledgeTags
	}
	for _, scope := range scopes {
		kbID := scope.KnowledgeBaseID
		if kbID == "" {
			continue
		}
		resp, err := t.wikiService.GetIndexView(ctx, kbID, nil, wikiNavigateIndexLimit, "")
		if err != nil || resp == nil {
			continue
		}
		groups := t.filterIndexGroupsByScope(ctx, scope, resp.Groups, fetchTags)
		for _, group := range groups {
			for _, item := range group.Items {
				addFoundKB(foundKBs, item.Slug, kbID)
			}
		}
		overviews = append(overviews, wikiNavigationIndexOverview{
			kbID:   kbID,
			groups: groups,
		})
	}
	return overviews
}

func (t *wikiNavigateTool) filterIndexGroupsByScope(ctx context.Context, scope WikiScope, groups []types.WikiIndexGroup, fetchTags knowledgeTagsFetcher) []types.WikiIndexGroup {
	hasKnowledgeFilter := len(scope.KnowledgeIDs) > 0
	hasTagFilter := len(scope.TagIDs) > 0
	if !hasKnowledgeFilter && !hasTagFilter {
		return groups
	}

	filteredGroups := make([]types.WikiIndexGroup, 0, len(groups))
	for _, group := range groups {
		filtered := group
		filtered.Items = nil
		for _, item := range group.Items {
			page, err := t.wikiService.GetPageBySlug(ctx, scope.KnowledgeBaseID, item.Slug)
			if err != nil || page == nil {
				continue
			}
			passesScope, scopeErr := pagePassesWikiScope(ctx, page, scope, fetchTags)
			if scopeErr != nil || !passesScope {
				continue
			}
			filtered.Items = append(filtered.Items, item)
		}
		filtered.Total = int64(len(filtered.Items))
		filtered.NextCursor = ""
		filteredGroups = append(filteredGroups, filtered)
	}
	return filteredGroups
}

func collectIndexCandidatesFromOverview(overview wikiNavigationIndexOverview, focusTypes []string, query string, out map[string]*wikiNavigationCandidate) {
	for _, group := range overview.groups {
		for _, item := range group.Items {
			score, reasons := scoreIndexEntry(item, query)
			if score <= 0 {
				continue
			}
			if wikiPageTypeFocused(group.Type, focusTypes) {
				score += 6
				reasons = append(reasons, "page_type_focus")
			}
			key := seenLinkKey(overview.kbID, item.Slug)
			c := mergeNavigationCandidate(out, key, &wikiNavigationCandidate{
				kbID:      overview.kbID,
				slug:      item.Slug,
				title:     item.Title,
				pageType:  group.Type,
				summary:   item.Summary,
				score:     score,
				reasons:   reasons,
				fromIndex: true,
			})
			c.fromIndex = true
		}
	}
}

func wikiPageTypeFocused(pageType string, focusTypes []string) bool {
	for _, focusType := range focusTypes {
		if pageType == focusType {
			return true
		}
	}
	return false
}

func (t *wikiNavigateTool) collectSearchCandidates(ctx context.Context, scope WikiScope, fetchTags knowledgeTagsFetcher, query string, searchQuery string, out map[string]*wikiNavigationCandidate, foundKBs map[string][]string) {
	kbID := scope.KnowledgeBaseID
	pages, err := t.wikiService.SearchPages(ctx, kbID, searchQuery, wikiNavigateSearchPool)
	if err != nil {
		return
	}
	for _, page := range pages {
		if page == nil {
			continue
		}
		passesScope, scopeErr := pagePassesWikiScope(ctx, page, scope, fetchTags)
		if scopeErr != nil || !passesScope {
			continue
		}
		actualKBID := kbID
		if page.KnowledgeBaseID != "" {
			actualKBID = page.KnowledgeBaseID
		}
		key := seenLinkKey(actualKBID, page.Slug)
		score, reasons := scorePageCandidate(page, query)
		if score <= 0 {
			score = 8
			reasons = appendUniqueStrings(reasons, "search_hit")
		}
		snippet := extractSnippet(page.Content, searchQuery)
		c := mergeNavigationCandidate(out, key, &wikiNavigationCandidate{
			page:     page,
			kbID:     actualKBID,
			slug:     page.Slug,
			title:    page.Title,
			pageType: page.PageType,
			summary:  page.Summary,
			score:    score,
			reasons:  reasons,
			snippet:  snippet,
			outLinks: []string(page.OutLinks),
			inLinks:  []string(page.InLinks),
		})
		if c.page == nil {
			c.page = page
		}
		registerLinkedSlugs(foundKBs, page, actualKBID)
		addFoundKB(foundKBs, page.Slug, actualKBID)
	}
}

func filterWikiScopesByKB(scopes []WikiScope, kbID string) []WikiScope {
	if kbID == "" {
		return scopes
	}
	for _, scope := range scopes {
		if scope.KnowledgeBaseID == kbID {
			return []WikiScope{scope}
		}
	}
	return []WikiScope{{KnowledgeBaseID: kbID}}
}

func normalizeWikiNavigateQueryMode(mode string) (string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", wikiNavigateQueryModeLiteral:
		return wikiNavigateQueryModeLiteral, nil
	case wikiNavigateQueryModeRegex:
		return wikiNavigateQueryModeRegex, nil
	default:
		return "", fmt.Errorf("unsupported query_mode: %s", mode)
	}
}

func wikiNavigateSearchQuery(query string, mode string) string {
	if mode == wikiNavigateQueryModeRegex {
		return query
	}
	return regexp.QuoteMeta(query)
}

func parseWikiNavigatePageTypes(val any) []string {
	return dedupNonEmptyStrings(parseStringOrArray(val))
}

func resolveWikiNavigateFocusPageTypes(rawPageTypes []string, overviews []wikiNavigationIndexOverview) ([]string, []string) {
	if len(rawPageTypes) == 0 {
		return nil, nil
	}
	available := make(map[string]struct{})
	for _, overview := range overviews {
		for _, group := range overview.groups {
			pageType := strings.TrimSpace(group.Type)
			if pageType != "" {
				available[pageType] = struct{}{}
			}
		}
	}

	var focusTypes []string
	var unsupported []string
	for _, pageType := range rawPageTypes {
		if _, ok := available[pageType]; ok {
			focusTypes = append(focusTypes, pageType)
			continue
		}
		unsupported = append(unsupported, pageType)
	}
	if len(unsupported) == 0 {
		return focusTypes, nil
	}
	return focusTypes, []string{fmt.Sprintf("Ignored page_types not present in scoped wiki KBs: %s", strings.Join(unsupported, ", "))}
}

func scoreIndexEntry(item types.WikiIndexEntry, query string) (int, []string) {
	q := normalizeNavigateText(query)
	title := normalizeNavigateText(item.Title)
	slug := normalizeNavigateText(item.Slug)
	summary := normalizeNavigateText(item.Summary)
	score := 0
	var reasons []string
	if title == q {
		score += 100
		reasons = append(reasons, "title_exact")
	} else if strings.Contains(title, q) {
		score += 82
		reasons = append(reasons, "title_contains")
	}
	if strings.Contains(slug, q) {
		score += 68
		reasons = append(reasons, "slug_contains")
	}
	if strings.Contains(summary, q) {
		score += 50
		reasons = append(reasons, "index_summary")
	}
	return score, reasons
}

func scorePageCandidate(page *types.WikiPage, query string) (int, []string) {
	if page == nil {
		return 0, nil
	}
	q := normalizeNavigateText(query)
	title := normalizeNavigateText(page.Title)
	slug := normalizeNavigateText(page.Slug)
	summary := normalizeNavigateText(page.Summary)
	content := normalizeNavigateText(page.Content)
	score := 0
	var reasons []string
	if title == q {
		score += 100
		reasons = append(reasons, "title_exact")
	} else if strings.Contains(title, q) {
		score += 85
		reasons = append(reasons, "title_contains")
	}
	for _, alias := range page.Aliases {
		aliasNorm := normalizeNavigateText(alias)
		if aliasNorm == q {
			score += 95
			reasons = append(reasons, "alias_exact")
			break
		}
		if strings.Contains(aliasNorm, q) {
			score += 65
			reasons = append(reasons, "alias_contains")
			break
		}
	}
	if strings.Contains(slug, q) {
		score += 70
		reasons = append(reasons, "slug_contains")
	}
	if strings.Contains(summary, q) {
		score += 55
		reasons = append(reasons, "summary_contains")
	}
	if strings.Contains(content, q) {
		score += 35
		reasons = append(reasons, "content_contains")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "search_hit")
	}
	return score, reasons
}

func mergeNavigationCandidate(out map[string]*wikiNavigationCandidate, key string, next *wikiNavigationCandidate) *wikiNavigationCandidate {
	current := out[key]
	if current == nil {
		out[key] = next
		return next
	}
	current.score += next.score
	current.reasons = appendUniqueStrings(current.reasons, next.reasons...)
	if current.page == nil {
		current.page = next.page
	}
	if current.title == "" {
		current.title = next.title
	}
	if current.pageType == "" {
		current.pageType = next.pageType
	}
	if current.summary == "" {
		current.summary = next.summary
	}
	if current.snippet == "" {
		current.snippet = next.snippet
	}
	if len(current.outLinks) == 0 {
		current.outLinks = next.outLinks
	}
	if len(current.inLinks) == 0 {
		current.inLinks = next.inLinks
	}
	current.fromIndex = current.fromIndex || next.fromIndex
	return current
}

func rankedWikiNavigationCandidates(candidates map[string]*wikiNavigationCandidate) []*wikiNavigationCandidate {
	ranked := make([]*wikiNavigationCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c != nil && c.score > 0 && c.slug != "" {
			ranked = append(ranked, c)
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].slug < ranked[j].slug
	})
	return ranked
}

func (t *wikiNavigateTool) ensureCandidatePage(ctx context.Context, c *wikiNavigationCandidate) {
	if c == nil || c.page != nil || c.kbID == "" || c.slug == "" {
		return
	}
	page, err := t.wikiService.GetPageBySlug(ctx, c.kbID, c.slug)
	if err != nil || page == nil {
		return
	}
	c.page = page
	c.title = firstNonEmpty(c.title, page.Title)
	c.pageType = firstNonEmpty(c.pageType, page.PageType)
	c.summary = firstNonEmpty(c.summary, page.Summary)
	c.outLinks = []string(page.OutLinks)
	c.inLinks = []string(page.InLinks)
}

func (t *wikiNavigateTool) collectNextHops(ctx context.Context, ranked []*wikiNavigationCandidate, query string, foundKBs map[string][]string) []*wikiNavigationCandidate {
	seen := make(map[string]bool, len(ranked))
	for _, c := range ranked {
		seen[seenLinkKey(c.kbID, c.slug)] = true
	}
	next := make(map[string]*wikiNavigationCandidate)
	for _, c := range ranked {
		for _, slug := range appendUniqueStrings(nil, c.outLinks...) {
			t.addNextHop(ctx, c.kbID, slug, "links_to:"+c.slug, query, seen, next, foundKBs)
		}
		for _, slug := range appendUniqueStrings(nil, c.inLinks...) {
			t.addNextHop(ctx, c.kbID, slug, "linked_from:"+c.slug, query, seen, next, foundKBs)
		}
	}
	rankedNext := rankedWikiNavigationCandidates(next)
	if len(rankedNext) > wikiNavigateNextHopLimit {
		rankedNext = rankedNext[:wikiNavigateNextHopLimit]
	}
	return rankedNext
}

func (t *wikiNavigateTool) addNextHop(ctx context.Context, kbID, slug, reason, query string, seen map[string]bool, out map[string]*wikiNavigationCandidate, foundKBs map[string][]string) {
	if kbID == "" || slug == "" {
		return
	}
	key := seenLinkKey(kbID, slug)
	if seen[key] {
		return
	}
	if _, ok := out[key]; ok {
		return
	}
	page, err := t.wikiService.GetPageBySlug(ctx, kbID, slug)
	if err != nil || page == nil {
		return
	}
	score, reasons := scorePageCandidate(page, query)
	if score <= 0 {
		score = 10
	}
	reasons = appendUniqueStrings([]string{reason}, reasons...)
	out[key] = &wikiNavigationCandidate{
		page:     page,
		kbID:     kbID,
		slug:     page.Slug,
		title:    page.Title,
		pageType: page.PageType,
		summary:  page.Summary,
		score:    score,
		reasons:  reasons,
		outLinks: []string(page.OutLinks),
		inLinks:  []string(page.InLinks),
	}
	registerLinkedSlugs(foundKBs, page, kbID)
	addFoundKB(foundKBs, page.Slug, kbID)
}

func renderWikiNavigationResults(query string, overviews []wikiNavigationIndexOverview, focusTypes []string, warnings []string, ranked []*wikiNavigationCandidate, nextHops []*wikiNavigationCandidate) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "<navigation_results count=\"%d\" query=\"%s\">\n", len(ranked), xmlEscape(query))
	renderWikiNavigationIndexOverview(&sb, overviews, focusTypes)
	renderWikiNavigationWarnings(&sb, warnings)
	if len(ranked) == 0 {
		sb.WriteString("<recommendation>No direct candidate wiki pages found. Inspect index_overview categories and wikilinks, then call wiki_read_page on the most relevant slug(s) or retry wiki_navigate with a wikilink title from the overview.</recommendation>\n")
		sb.WriteString("</navigation_results>")
		return sb.String()
	}
	sb.WriteString("<recommended_reads>\n")
	for _, c := range ranked {
		renderWikiNavigationPage(&sb, c, "page")
	}
	sb.WriteString("</recommended_reads>\n")
	if len(nextHops) > 0 {
		sb.WriteString("<next_hops>\n")
		for _, c := range nextHops {
			renderWikiNavigationPage(&sb, c, "page")
		}
		sb.WriteString("</next_hops>\n")
	}
	sb.WriteString("<instruction>First inspect index_overview categories and wikilinks, then call wiki_read_page on the most relevant recommended or overview slugs before answering. If a read page points to a stronger related slug, continue with wiki_read_page on that next hop.</instruction>\n")
	sb.WriteString("</navigation_results>")
	return sb.String()
}

func renderWikiNavigationWarnings(sb *strings.Builder, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	sb.WriteString("<warnings>\n")
	for _, warning := range warnings {
		fmt.Fprintf(sb, "<warning>%s</warning>\n", xmlEscape(warning))
	}
	sb.WriteString("</warnings>\n")
}

func renderWikiNavigationIndexOverview(sb *strings.Builder, overviews []wikiNavigationIndexOverview, focusTypes []string) {
	sb.WriteString("<index_overview>\n")
	for _, overview := range overviews {
		fmt.Fprintf(sb, "<knowledge_base id=\"%s\">\n", xmlEscape(overview.kbID))
		sb.WriteString("<categories>\n")
		for _, group := range overview.groups {
			if group.Total == 0 && len(group.Items) == 0 {
				continue
			}
			shown := indexOverviewShownCount(group, focusTypes)
			fmt.Fprintf(sb, "<category type=\"%s\" total=\"%d\" shown=\"%d\" />\n", xmlEscape(group.Type), group.Total, shown)
		}
		sb.WriteString("</categories>\n")
		sb.WriteString("<index_links>\n")
		for _, group := range overview.groups {
			if group.Total == 0 && len(group.Items) == 0 {
				continue
			}
			limit := indexOverviewPageLimit(group.Type, focusTypes)
			items := group.Items
			if len(items) > limit {
				items = items[:limit]
			}
			if len(items) == 0 {
				continue
			}
			fmt.Fprintf(sb, "<category type=\"%s\" total=\"%d\" shown=\"%d\">\n", xmlEscape(group.Type), group.Total, len(items))
			for _, item := range items {
				title := item.Title
				if title == "" {
					title = item.Slug
				}
				sb.WriteString("<page>\n")
				fmt.Fprintf(sb, "<link>[[%s|%s]]</link>\n", xmlEscape(item.Slug), xmlEscape(title))
				if item.Summary != "" {
					fmt.Fprintf(sb, "<summary>%s</summary>\n", xmlEscape(truncateForSummary(item.Summary, 220)))
				}
				sb.WriteString("</page>\n")
			}
			if group.NextCursor != "" {
				fmt.Fprintf(sb, "<more cursor=\"%s\">This category has more wiki links than shown.</more>\n", xmlEscape(group.NextCursor))
			} else if int64(len(items)) < group.Total {
				sb.WriteString("<more>Call wiki_navigate again with this page_type after choosing the category from index_overview.</more>\n")
			}
			sb.WriteString("</category>\n")
		}
		sb.WriteString("</index_links>\n")
		sb.WriteString("</knowledge_base>\n")
	}
	sb.WriteString("</index_overview>\n")
}

func indexOverviewShownCount(group types.WikiIndexGroup, focusTypes []string) int {
	limit := indexOverviewPageLimit(group.Type, focusTypes)
	if len(group.Items) < limit {
		return len(group.Items)
	}
	return limit
}

func indexOverviewPageLimit(pageType string, focusTypes []string) int {
	if len(focusTypes) == 0 {
		return wikiNavigateIndexDefault
	}
	if wikiPageTypeFocused(pageType, focusTypes) {
		return wikiNavigateIndexFocused
	}
	return wikiNavigateIndexOther
}

func renderWikiNavigationPage(sb *strings.Builder, c *wikiNavigationCandidate, element string) {
	if c == nil {
		return
	}
	title := c.title
	if title == "" {
		title = c.slug
	}
	fmt.Fprintf(sb, "<%s score=\"%d\" reasons=\"%s\">\n", element, c.score, xmlEscape(strings.Join(c.reasons, ",")))
	fmt.Fprintf(sb, "<knowledge_base_id>%s</knowledge_base_id>\n", xmlEscape(c.kbID))
	fmt.Fprintf(sb, "<link>[[%s|%s]]</link>\n", xmlEscape(c.slug), xmlEscape(title))
	if c.pageType != "" {
		fmt.Fprintf(sb, "<type>%s</type>\n", xmlEscape(c.pageType))
	}
	if c.summary != "" {
		fmt.Fprintf(sb, "<summary>%s</summary>\n", xmlEscape(truncateForSummary(c.summary, 260)))
	}
	if c.snippet != "" {
		fmt.Fprintf(sb, "<match_snippet>%s</match_snippet>\n", xmlEscape(c.snippet))
	}
	renderSlugList(sb, "links_to", c.outLinks, 8)
	renderSlugList(sb, "linked_from", c.inLinks, 8)
	fmt.Fprintf(sb, "</%s>\n", element)
}

func renderSlugList(sb *strings.Builder, tag string, slugs []string, limit int) {
	slugs = appendUniqueStrings(nil, slugs...)
	if len(slugs) == 0 {
		return
	}
	if len(slugs) > limit {
		slugs = slugs[:limit]
	}
	links := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		links = append(links, "[["+xmlEscape(slug)+"]]")
	}
	fmt.Fprintf(sb, "<%s>%s</%s>\n", tag, strings.Join(links, ", "), tag)
}

func normalizeNavigateText(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func appendUniqueStrings(base []string, values ...string) []string {
	seen := make(map[string]struct{}, len(base)+len(values))
	for _, value := range base {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}

func addFoundKB(foundKBs map[string][]string, slug string, kbID string) {
	if slug == "" || kbID == "" {
		return
	}
	for _, existing := range foundKBs[slug] {
		if existing == kbID {
			return
		}
	}
	foundKBs[slug] = append(foundKBs[slug], kbID)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
