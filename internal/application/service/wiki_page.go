package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// wikiLinkRegex matches [[wiki-link]] syntax in markdown content
var wikiLinkRegex = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// wikiPageService implements the WikiPageService interface
type wikiPageService struct {
	repo        interfaces.WikiPageRepository
	chunkRepo   interfaces.ChunkRepository
	kbService   interfaces.KnowledgeBaseService
	redisClient *redis.Client
}

// NewWikiPageService creates a new wiki page service
func NewWikiPageService(
	repo interfaces.WikiPageRepository,
	chunkRepo interfaces.ChunkRepository,
	kbService interfaces.KnowledgeBaseService,
	redisClient *redis.Client,
) interfaces.WikiPageService {
	return &wikiPageService{
		repo:        repo,
		chunkRepo:   chunkRepo,
		kbService:   kbService,
		redisClient: redisClient,
	}
}

// CreatePage creates a new wiki page
func (s *wikiPageService) CreatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	if page.ID == "" {
		page.ID = uuid.New().String()
	}
	if page.Slug == "" {
		return nil, errors.New("wiki page slug is required")
	}
	if page.KnowledgeBaseID == "" {
		return nil, errors.New("knowledge_base_id is required")
	}
	if page.Status == "" {
		page.Status = types.WikiPageStatusPublished
	}
	if page.Version == 0 {
		page.Version = 1
	}

	// Parse outbound links from content
	page.OutLinks = s.parseOutLinks(page.Content)

	now := time.Now()
	page.CreatedAt = now
	page.UpdatedAt = now

	if err := s.repo.Create(ctx, page); err != nil {
		return nil, fmt.Errorf("create wiki page: %w", err)
	}

	// Update inbound links on target pages
	s.updateInLinks(ctx, page.KnowledgeBaseID, page.Slug, page.OutLinks)

	return page, nil
}

// UpdatePage updates an existing wiki page.
//
// Version bump policy: the `version` column is intended to track the user-
// visible content revision, not every row rewrite. We therefore bump it only
// when at least one of the user-facing fields actually changes — title,
// content, summary, page_type, or status. Bookkeeping-only writes (refreshing
// source_refs after re-ingest when the body is identical, rebuilding the index
// page with the same directory, cross-link injection that ends up replacing
// nothing, etc.) still persist through `UpdateMeta` but leave `version`
// untouched so consumers can treat a bump as a real edit signal.
func (s *wikiPageService) UpdatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	existing, err := s.repo.GetBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	if err != nil {
		return nil, fmt.Errorf("get existing page: %w", err)
	}

	oldOutLinks := existing.OutLinks

	// Snapshot user-visible fields BEFORE mutation so we can decide whether
	// this is a real content change or just bookkeeping.
	contentChanged := existing.Title != page.Title ||
		existing.Content != page.Content ||
		existing.Summary != page.Summary ||
		existing.PageType != page.PageType ||
		existing.Status != page.Status

	existing.Title = page.Title
	existing.Content = page.Content
	existing.Summary = page.Summary
	existing.PageType = page.PageType
	existing.SourceRefs = page.SourceRefs
	existing.ChunkRefs = page.ChunkRefs
	existing.PageMetadata = page.PageMetadata
	existing.Status = page.Status
	existing.UpdatedAt = time.Now()

	// Outbound links are a pure derivative of content, so they only shift
	// when content shifts. Re-parse unconditionally to stay consistent with
	// the stored body.
	existing.OutLinks = s.parseOutLinks(existing.Content)

	if contentChanged {
		if err := s.repo.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("update wiki page: %w", err)
		}
	} else {
		// No user-visible change — persist bookkeeping fields but preserve
		// the version so downstream consumers can rely on it.
		if err := s.repo.UpdateMeta(ctx, existing); err != nil {
			return nil, fmt.Errorf("update wiki page meta: %w", err)
		}
	}

	// Update inbound links: remove old, add new. If content didn't change,
	// oldOutLinks == existing.OutLinks and these calls are effectively no-ops.
	s.removeInLinks(ctx, existing.KnowledgeBaseID, existing.Slug, oldOutLinks)
	s.updateInLinks(ctx, existing.KnowledgeBaseID, existing.Slug, existing.OutLinks)

	return existing, nil
}

// UpdatePageMeta updates only metadata (status, source_refs) without version bump or link re-parse.
func (s *wikiPageService) UpdatePageMeta(ctx context.Context, page *types.WikiPage) error {
	page.UpdatedAt = time.Now()
	return s.repo.UpdateMeta(ctx, page)
}

// UpdateAutoLinkedContent persists content produced by machine-only link
// decorators (cross-link injection / dead-link cleanup) without bumping
// `version`. Out-links are re-parsed from the new body and bidirectional
// in-link references on target pages are refreshed so link navigation stays
// consistent — only the user-facing revision counter is preserved.
func (s *wikiPageService) UpdateAutoLinkedContent(ctx context.Context, page *types.WikiPage) error {
	existing, err := s.repo.GetBySlug(ctx, page.KnowledgeBaseID, page.Slug)
	if err != nil {
		return fmt.Errorf("get existing page: %w", err)
	}

	oldOutLinks := existing.OutLinks

	existing.Content = page.Content
	existing.OutLinks = s.parseOutLinks(existing.Content)
	existing.UpdatedAt = time.Now()

	if err := s.repo.UpdateAutoLinkedContent(ctx, existing); err != nil {
		return fmt.Errorf("update auto-linked content: %w", err)
	}

	s.removeInLinks(ctx, existing.KnowledgeBaseID, existing.Slug, oldOutLinks)
	s.updateInLinks(ctx, existing.KnowledgeBaseID, existing.Slug, existing.OutLinks)

	return nil
}

// GetPageBySlug retrieves a wiki page by its slug
func (s *wikiPageService) GetPageBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error) {
	return s.repo.GetBySlug(ctx, kbID, slug)
}

// GetPageByID retrieves a wiki page by its ID
func (s *wikiPageService) GetPageByID(ctx context.Context, id string) (*types.WikiPage, error) {
	return s.repo.GetByID(ctx, id)
}

// ListPages lists wiki pages with optional filtering and pagination
func (s *wikiPageService) ListPages(ctx context.Context, req *types.WikiPageListRequest) (*types.WikiPageListResponse, error) {
	pages, total, err := s.repo.List(ctx, req)
	if err != nil {
		return nil, err
	}

	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	page := req.Page
	if page < 1 {
		page = 1
	}
	totalPages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		totalPages++
	}

	return &types.WikiPageListResponse{
		Pages:      pages,
		Total:      total,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}, nil
}

// DeletePage soft-deletes a wiki page
func (s *wikiPageService) DeletePage(ctx context.Context, kbID string, slug string) error {
	page, err := s.repo.GetBySlug(ctx, kbID, slug)
	if err != nil {
		return err
	}

	// Remove inbound link references from pages this page links to
	s.removeInLinks(ctx, kbID, slug, page.OutLinks)

	// Delete the page
	if err := s.repo.Delete(ctx, kbID, slug); err != nil {
		return err
	}

	// Delete synced chunk
	s.deleteChunkForPage(ctx, page)

	return nil
}

// GetIndex returns the index page for a knowledge base
func (s *wikiPageService) GetIndex(ctx context.Context, kbID string) (*types.WikiPage, error) {
	page, err := s.repo.GetBySlug(ctx, kbID, "index")
	if err != nil {
		if errors.Is(err, repository.ErrWikiPageNotFound) {
			// Create default index page
			return s.createDefaultPage(ctx, kbID, "index", "Index", types.WikiPageTypeIndex,
				"# Wiki Index\n\nThis is the index page. It will be automatically updated as pages are added.\n")
		}
		return nil, err
	}
	return page, nil
}

// wikiIndexContentPageTypes enumerates the page types that make up a wiki's
// user-visible directory. System pages (index/log) are excluded; any
// LLM-created type we do not recognize surfaces under a generic "other"
// bucket.
var wikiIndexContentPageTypes = []string{
	types.WikiPageTypeSummary,
	types.WikiPageTypeEntity,
	types.WikiPageTypeConcept,
	types.WikiPageTypeSynthesis,
	types.WikiPageTypeComparison,
}

// GetIndexView builds the structured index response without ever
// materializing a multi-MB directory markdown string. Intro is read from
// the index wiki_page row (which now carries only intro text — see
// rebuildIndexPage). Each requested page_type is paginated independently
// with ListByTypeLight so reads stay O(page_size) rather than O(total
// pages in the KB).
//
// `pageTypes` narrows which groups to include; empty = all content types.
// `limit` is the per-group window size (defaults to 50, capped at 200).
// `cursor` is an opaque offset string; currently we use the stringified
// offset so clients can resume where they left off. Because different
// page_types paginate independently, `cursor` applies uniformly to every
// group — if the caller wants per-group cursors it should request one
// type at a time via `pageTypes`. That simplifies the wire format and
// matches the frontend's tabbed UX.
func (s *wikiPageService) GetIndexView(
	ctx context.Context,
	kbID string,
	pageTypes []string,
	limit int,
	cursor string,
) (*types.WikiIndexResponse, error) {
	indexPage, err := s.GetIndex(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("load index page: %w", err)
	}

	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := 0
	if cursor != "" {
		v, parseErr := strconv.Atoi(cursor)
		if parseErr != nil || v < 0 {
			return nil, fmt.Errorf("invalid cursor %q", cursor)
		}
		offset = v
	}

	// Default to every known content type when the caller passes no
	// filter. Any unknown request-time type is passed through verbatim so
	// future page types (declared in types/wiki_page.go) start showing
	// up in the index the moment the LLM starts creating them, without a
	// handler change.
	selected := pageTypes
	if len(selected) == 0 {
		selected = append([]string{}, wikiIndexContentPageTypes...)
	}

	groups := make([]types.WikiIndexGroup, 0, len(selected))
	for _, pt := range selected {
		entries, total, listErr := s.repo.ListByTypeLight(ctx, kbID, pt, limit, offset)
		if listErr != nil {
			return nil, fmt.Errorf("list %s pages: %w", pt, listErr)
		}
		if entries == nil {
			entries = []types.WikiIndexEntry{}
		}
		next := ""
		// Only emit a cursor when a full page was returned AND more rows
		// remain past `offset + limit`. A short page or one that exactly
		// consumed the remainder should signal end-of-feed.
		if len(entries) == limit && int64(offset+len(entries)) < total {
			next = strconv.Itoa(offset + limit)
		}
		groups = append(groups, types.WikiIndexGroup{
			Type:       pt,
			Total:      total,
			Items:      entries,
			NextCursor: next,
		})
	}

	// The intro used to be stored on indexPage.Summary while
	// indexPage.Content held intro + directory markdown. After the
	// directory was lifted out of wiki_pages the content column holds
	// only the intro. Fall back to Summary for KBs that haven't been
	// re-ingested since the change so the response is never blank.
	intro := indexPage.Content
	if strings.TrimSpace(intro) == "" {
		intro = indexPage.Summary
	}

	return &types.WikiIndexResponse{
		Intro:   intro,
		Version: indexPage.Version,
		Groups:  groups,
	}, nil
}

// GetLog returns the wiki_pages row for slug='log' if it exists.
//
// Log events are now stored in the dedicated `wiki_log_entries` table and
// paginated via wikiLogEntryService — the per-KB log is no longer a single
// TEXT column on a wiki_pages row (that model caused O(n^2) write
// amplification as logs grew). This method is retained for callers that
// still probe the legacy row (wiki_lint, knowledge delete, etc.), but it
// no longer auto-creates the placeholder page on miss; a missing row is a
// normal state and the helper returns `nil, nil`.
func (s *wikiPageService) GetLog(ctx context.Context, kbID string) (*types.WikiPage, error) {
	page, err := s.repo.GetBySlug(ctx, kbID, "log")
	if err != nil {
		if errors.Is(err, repository.ErrWikiPageNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return page, nil
}

// GetGraph returns a slice of the wiki link graph for visualization.
//
// Two modes are supported:
//
//   - WikiGraphModeOverview (default): returns the top `Limit` pages sorted
//     by link_count (in+out), plus every edge that connects two surviving
//     nodes. This is what the frontend fetches on the first graph open —
//     4万-page wikis would otherwise ship ~30MB of JSON and crash the
//     browser trying to render 100k SVG elements.
//
//   - WikiGraphModeEgo: returns the BFS neighborhood of `Center` up to
//     `Depth` undirected hops, capped at `Limit` total nodes. The
//     frontend uses this to drill down when the user clicks / searches a
//     node in the overview.
//
// `Types` is an optional page_type allow-list applied to both the candidate
// node set and (in ego mode) the frontier expansion. Leaving it empty means
// no type filter.
//
// `Limit <= 0` disables the cap entirely and is reserved for internal
// callers like the lint service that need to walk every page. The HTTP
// handler always clamps Limit into a safe range so external traffic can
// never opt out of truncation.
//
// Implementation note: pages are still fetched via repo.ListAll. At 4万
// pages that's ~10MB of rows + deserialization, which is already on the
// expensive side but still tractable and keeps the repository interface
// unchanged. Pushing the filter/top-N down into SQL is a follow-up step
// (cache layer + DB-side projection) — see CLAUDE.md plan.
func (s *wikiPageService) GetGraph(ctx context.Context, req *types.WikiGraphRequest) (*types.WikiGraphData, error) {
	if req == nil {
		return nil, errors.New("wiki graph request is required")
	}

	pages, err := s.repo.ListAll(ctx, req.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	return computeGraphSubset(pages, req)
}

// computeGraphSubset is the pure I/O-free core of GetGraph. It takes the
// full page list and a request description and returns the subgraph the
// caller asked for. Extracted from GetGraph so tests can exercise the
// mode/limit/type-filter behavior without plumbing a full repository mock.
func computeGraphSubset(pages []*types.WikiPage, req *types.WikiGraphRequest) (*types.WikiGraphData, error) {
	mode := req.Mode
	if mode == "" {
		mode = types.WikiGraphModeOverview
	}

	// Pre-compute link_count and the type allow-list used for candidate
	// filtering. We keep the full page list around so ego mode can still
	// traverse through neighbors whose type is in the allow-list.
	typeAllow := make(map[string]bool, len(req.Types))
	for _, t := range req.Types {
		if t != "" {
			typeAllow[t] = true
		}
	}
	hasTypeFilter := len(typeAllow) > 0

	pageBySlug := make(map[string]*types.WikiPage, len(pages))
	linkCount := make(map[string]int, len(pages))
	for _, p := range pages {
		pageBySlug[p.Slug] = p
		linkCount[p.Slug] = len(p.InLinks) + len(p.OutLinks)
	}

	// Select the node slug set for the requested slice.
	var selected map[string]struct{}
	switch mode {
	case types.WikiGraphModeEgo:
		if req.Center == "" {
			return nil, errors.New("ego graph requires a center slug")
		}
		if _, ok := pageBySlug[req.Center]; !ok {
			return nil, fmt.Errorf("ego center slug %q not found", req.Center)
		}
		depth := req.Depth
		if depth < 1 {
			depth = 1
		}
		selected = bfsEgoSlugs(pageBySlug, req.Center, depth, typeAllow, req.Limit)
	default:
		// overview: keep only type-allowed candidates, sort by link_count desc, cap.
		candidates := make([]*types.WikiPage, 0, len(pages))
		for _, p := range pages {
			if hasTypeFilter && !typeAllow[p.PageType] {
				continue
			}
			candidates = append(candidates, p)
		}
		sort.SliceStable(candidates, func(i, j int) bool {
			li := linkCount[candidates[i].Slug]
			lj := linkCount[candidates[j].Slug]
			if li != lj {
				return li > lj
			}
			// Stable tiebreaker keeps the API deterministic between calls.
			return candidates[i].Slug < candidates[j].Slug
		})
		if req.Limit > 0 && len(candidates) > req.Limit {
			candidates = candidates[:req.Limit]
		}
		selected = make(map[string]struct{}, len(candidates))
		for _, p := range candidates {
			selected[p.Slug] = struct{}{}
		}
	}

	// Build nodes from the selected set.
	nodes := make([]types.WikiGraphNode, 0, len(selected))
	for slug := range selected {
		p := pageBySlug[slug]
		nodes = append(nodes, types.WikiGraphNode{
			Slug:      p.Slug,
			Title:     p.Title,
			PageType:  p.PageType,
			LinkCount: linkCount[slug],
		})
	}
	// Deterministic node ordering — the map iteration above is random.
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].LinkCount != nodes[j].LinkCount {
			return nodes[i].LinkCount > nodes[j].LinkCount
		}
		return nodes[i].Slug < nodes[j].Slug
	})

	// Build edges, keeping only edges whose endpoints both survived selection.
	var edges []types.WikiGraphEdge
	for _, p := range pages {
		if _, ok := selected[p.Slug]; !ok {
			continue
		}
		for _, target := range p.OutLinks {
			if _, ok := selected[target]; !ok {
				continue
			}
			edges = append(edges, types.WikiGraphEdge{
				Source: p.Slug,
				Target: target,
			})
		}
	}

	// total is the count of candidate nodes before truncation — i.e. the
	// population the frontend would need to fetch if it asked for the
	// whole graph. For overview this respects the type filter; for ego
	// it is the total KB page count (the user still sees "X of Y" based
	// on the full wiki, not a filtered denominator).
	total := len(pages)
	if mode == types.WikiGraphModeOverview && hasTypeFilter {
		total = 0
		for _, p := range pages {
			if typeAllow[p.PageType] {
				total++
			}
		}
	}

	meta := types.WikiGraphMeta{
		Mode:      mode,
		Total:     total,
		Returned:  len(nodes),
		Truncated: len(nodes) < total,
	}
	if mode == types.WikiGraphModeEgo {
		meta.Center = req.Center
		meta.Depth = req.Depth
		if meta.Depth < 1 {
			meta.Depth = 1
		}
	}

	return &types.WikiGraphData{
		Nodes: nodes,
		Edges: edges,
		Meta:  meta,
	}, nil
}

// bfsEgoSlugs computes the undirected BFS neighborhood of `center` up to
// `depth` hops using both inbound and outbound links. Type-filtered pages
// are excluded from the result but are also NOT traversed through — so a
// filter that hides "index" pages will not leak the whole wiki via the
// index. The caller guarantees center exists in pageBySlug.
func bfsEgoSlugs(
	pageBySlug map[string]*types.WikiPage,
	center string,
	depth int,
	typeAllow map[string]bool,
	limit int,
) map[string]struct{} {
	hasTypeFilter := len(typeAllow) > 0
	centerPage, ok := pageBySlug[center]
	if !ok {
		return map[string]struct{}{}
	}
	// If the center itself fails the type filter we honor the filter and
	// return an empty set — the handler will surface Returned=0.
	if hasTypeFilter && !typeAllow[centerPage.PageType] {
		return map[string]struct{}{}
	}

	visited := map[string]struct{}{center: {}}
	frontier := []string{center}

	for hop := 0; hop < depth; hop++ {
		if limit > 0 && len(visited) >= limit {
			break
		}
		next := make([]string, 0, len(frontier))
		for _, slug := range frontier {
			p, ok := pageBySlug[slug]
			if !ok {
				continue
			}
			neighbors := make([]string, 0, len(p.OutLinks)+len(p.InLinks))
			neighbors = append(neighbors, p.OutLinks...)
			neighbors = append(neighbors, p.InLinks...)
			for _, nb := range neighbors {
				if _, seen := visited[nb]; seen {
					continue
				}
				np, exists := pageBySlug[nb]
				if !exists {
					continue
				}
				if hasTypeFilter && !typeAllow[np.PageType] {
					continue
				}
				visited[nb] = struct{}{}
				next = append(next, nb)
				if limit > 0 && len(visited) >= limit {
					break
				}
			}
			if limit > 0 && len(visited) >= limit {
				break
			}
		}
		frontier = next
		if len(frontier) == 0 {
			break
		}
	}

	return visited
}

// GetStats returns aggregate statistics about the wiki
func (s *wikiPageService) GetStats(ctx context.Context, kbID string) (*types.WikiStats, error) {
	counts, err := s.repo.CountByType(ctx, kbID)
	if err != nil {
		return nil, err
	}

	var total int64
	for _, c := range counts {
		total += c
	}

	orphans, err := s.repo.CountOrphans(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Count total links
	pages, err := s.repo.ListAll(ctx, kbID)
	if err != nil {
		return nil, err
	}
	var totalLinks int64
	for _, p := range pages {
		totalLinks += int64(len(p.OutLinks))
	}

	// Get recent updates (last 10)
	listReq := &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		Page:            1,
		PageSize:        10,
		SortBy:          "updated_at",
		SortOrder:       "desc",
	}
	recentPages, _, err := s.repo.List(ctx, listReq)
	if err != nil {
		return nil, err
	}

	var pendingTasks int64
	var pendingIssues int64
	var isActive bool
	if s.redisClient != nil {
		pendingTasks, _ = s.redisClient.LLen(ctx, "wiki:pending:"+kbID).Result()
		activeFlag, _ := s.redisClient.Exists(ctx, "wiki:active:"+kbID).Result()
		isActive = activeFlag > 0
	}

	issues, _ := s.ListIssues(ctx, kbID, "", "pending")
	pendingIssues = int64(len(issues))

	return &types.WikiStats{
		TotalPages:    total,
		PagesByType:   counts,
		TotalLinks:    totalLinks,
		OrphanCount:   orphans,
		RecentUpdates: recentPages,
		PendingTasks:  pendingTasks,
		PendingIssues: pendingIssues,
		IsActive:      isActive,
	}, nil
}

// RebuildLinks re-parses all pages and rebuilds bidirectional link references
func (s *wikiPageService) RebuildLinks(ctx context.Context, kbID string) error {
	pages, err := s.repo.ListAll(ctx, kbID)
	if err != nil {
		return err
	}

	// Build slug-to-page map
	pageMap := make(map[string]*types.WikiPage)
	for _, p := range pages {
		pageMap[p.Slug] = p
	}

	// Clear all inbound links first
	for _, p := range pages {
		p.InLinks = types.StringArray{}
	}

	// Re-parse outbound links and rebuild inbound links
	for _, p := range pages {
		p.OutLinks = s.parseOutLinks(p.Content)
		for _, target := range p.OutLinks {
			if tp, exists := pageMap[target]; exists {
				tp.InLinks = append(tp.InLinks, p.Slug)
			}
		}
	}

	// Save all pages (link rebuild is metadata-only, no version bump)
	for _, p := range pages {
		p.UpdatedAt = time.Now()
		if err := s.repo.UpdateMeta(ctx, p); err != nil {
			logger.Warnf(ctx, "wiki: failed to update links for page %s: %v", p.Slug, err)
		}
	}

	return nil
}

// ListAllPages retrieves all wiki pages without pagination.
func (s *wikiPageService) ListAllPages(ctx context.Context, kbID string) ([]*types.WikiPage, error) {
	return s.repo.ListAll(ctx, kbID)
}

// ListByType returns every wiki page of a given type for a KB. Exposed so
// callers like intro regeneration can load only the page type they need
// (summaries) instead of paying for the full ListAll scan.
func (s *wikiPageService) ListByType(ctx context.Context, kbID string, pageType string) ([]*types.WikiPage, error) {
	return s.repo.ListByType(ctx, kbID, pageType)
}

// ListPagesBySourceRef exposes the repository's source-ref lookup so higher
// layers (delete flow, retract reconciliation) can re-query the current wiki
// state without depending on a stale caller-captured slug list.
func (s *wikiPageService) ListPagesBySourceRef(ctx context.Context, kbID string, knowledgeID string) ([]*types.WikiPage, error) {
	return s.repo.ListBySourceRef(ctx, kbID, knowledgeID)
}

// SearchPages performs full-text search over wiki pages
func (s *wikiPageService) SearchPages(ctx context.Context, kbID string, query string, limit int) ([]*types.WikiPage, error) {
	return s.repo.Search(ctx, kbID, query, limit)
}

// --- Internal helpers ---

// parseOutLinks extracts [[wiki-link]] slugs from markdown content
func (s *wikiPageService) parseOutLinks(content string) types.StringArray {
	matches := wikiLinkRegex.FindAllStringSubmatch(content, -1)
	seen := make(map[string]bool)
	var links types.StringArray

	for _, match := range matches {
		if len(match) > 1 {
			slug := strings.TrimSpace(match[1])
			// Handle [[slug|display name]] format — slug is the first part
			if parts := strings.SplitN(slug, "|", 2); len(parts) == 2 {
				slug = strings.TrimSpace(parts[0])
			}
			slug = normalizeSlug(slug)
			if slug != "" && !seen[slug] {
				seen[slug] = true
				links = append(links, slug)
			}
		}
	}
	return links
}

// normalizeSlug normalizes a wiki link slug
func normalizeSlug(slug string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	slug = strings.ReplaceAll(slug, " ", "-")
	return slug
}

// updateInLinks adds the source slug to the in_links of target pages
func (s *wikiPageService) updateInLinks(ctx context.Context, kbID string, sourceSlug string, targets types.StringArray) {
	for _, targetSlug := range targets {
		targetPage, err := s.repo.GetBySlug(ctx, kbID, targetSlug)
		if err != nil {
			continue // target page may not exist yet
		}
		if !containsString(targetPage.InLinks, sourceSlug) {
			targetPage.InLinks = append(targetPage.InLinks, sourceSlug)
			targetPage.UpdatedAt = time.Now()
			if err := s.repo.UpdateMeta(ctx, targetPage); err != nil {
				logger.Warnf(ctx, "wiki: failed to update in_links for %s: %v", targetSlug, err)
			}
		}
	}
}

// removeInLinks removes the source slug from the in_links of target pages
func (s *wikiPageService) removeInLinks(ctx context.Context, kbID string, sourceSlug string, targets types.StringArray) {
	for _, targetSlug := range targets {
		targetPage, err := s.repo.GetBySlug(ctx, kbID, targetSlug)
		if err != nil {
			continue
		}
		newInLinks := removeString(targetPage.InLinks, sourceSlug)
		if len(newInLinks) != len(targetPage.InLinks) {
			targetPage.InLinks = newInLinks
			targetPage.UpdatedAt = time.Now()
			if err := s.repo.UpdateMeta(ctx, targetPage); err != nil {
				logger.Warnf(ctx, "wiki: failed to update in_links for %s: %v", targetSlug, err)
			}
		}
	}
}

// deleteChunkForPage removes the synced chunk for a wiki page
func (s *wikiPageService) deleteChunkForPage(ctx context.Context, page *types.WikiPage) {
	chunkID := "wp-" + page.ID
	if err := s.chunkRepo.DeleteChunk(ctx, page.TenantID, chunkID); err != nil {
		logger.Warnf(ctx, "wiki: failed to delete chunk for page %s: %v", page.Slug, err)
	}
}

// createDefaultPage creates a default system page (index, log)
func (s *wikiPageService) createDefaultPage(ctx context.Context, kbID string, slug string, title string, pageType string, content string) (*types.WikiPage, error) {
	// Get KB to get tenant ID
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get knowledge base: %w", err)
	}

	page := &types.WikiPage{
		ID:              uuid.New().String(),
		TenantID:        kb.TenantID,
		KnowledgeBaseID: kbID,
		Slug:            slug,
		Title:           title,
		PageType:        pageType,
		Status:          types.WikiPageStatusPublished,
		Content:         content,
		Summary:         title,
		Version:         1,
	}

	if err := s.repo.Create(ctx, page); err != nil {
		return nil, fmt.Errorf("create default %s page: %w", slug, err)
	}
	return page, nil
}

// containsString checks if a string slice contains a given string
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// removeString removes a string from a slice
func removeString(slice []string, s string) types.StringArray {
	result := make(types.StringArray, 0, len(slice))
	for _, v := range slice {
		if v != s {
			result = append(result, v)
		}
	}
	return result
}

// CreateIssue logs a new issue for a wiki page
func (s *wikiPageService) CreateIssue(ctx context.Context, issue *types.WikiPageIssue) (*types.WikiPageIssue, error) {
	if issue.ID == "" {
		issue.ID = uuid.New().String()
	}
	if err := s.repo.CreateIssue(ctx, issue); err != nil {
		return nil, fmt.Errorf("create wiki page issue: %w", err)
	}
	return issue, nil
}

// ListIssues retrieves issues for a knowledge base
func (s *wikiPageService) ListIssues(ctx context.Context, kbID string, slug string, status string) ([]*types.WikiPageIssue, error) {
	return s.repo.ListIssues(ctx, kbID, slug, status)
}

// UpdateIssueStatus updates an issue's status
func (s *wikiPageService) UpdateIssueStatus(ctx context.Context, issueID string, status string) error {
	return s.repo.UpdateIssueStatus(ctx, issueID, status)
}

// InjectCrossLinks scans affected pages and injects [[wiki-links]] for mentions
// of other wiki page titles in the content. Pure text replacement, no LLM call.
// Shares the linkifyContent helper with the ingest pipeline so both paths honor
// the same code-block / existing-link / word-boundary rules.
func (s *wikiPageService) InjectCrossLinks(ctx context.Context, kbID string, affectedSlugs []string) {
	allPages, err := s.ListAllPages(ctx, kbID)
	if err != nil || len(allPages) < 2 {
		return
	}

	refs := collectLinkRefs(allPages)
	if len(refs) == 0 {
		return
	}

	affectedSet := make(map[string]bool, len(affectedSlugs))
	for _, slug := range affectedSlugs {
		affectedSet[slug] = true
	}

	var updated int
	for _, p := range allPages {
		if !affectedSet[p.Slug] {
			continue
		}
		if p.PageType == types.WikiPageTypeIndex || p.PageType == types.WikiPageTypeLog {
			continue
		}

		newContent, changed := linkifyContent(p.Content, refs, p.Slug)
		if !changed {
			continue
		}
		p.Content = newContent
		if err := s.UpdateAutoLinkedContent(ctx, p); err != nil {
			logger.Warnf(ctx, "wiki: cross-link injection failed for %s: %v", p.Slug, err)
			continue
		}
		updated++
	}

	if updated > 0 {
		logger.Infof(ctx, "wiki: injected cross-links in %d pages", updated)
	}
}

// RebuildIndexPage was historically called by agent write/rename tools to
// refresh the index page's directory listing after a page mutation.
//
// The directory is no longer persisted in wiki_pages.content — it is
// assembled on demand by GetIndexView from the lightweight ListByTypeLight
// projection, so individual page writes don't need to redo O(N) string
// concatenation and rewrite a multi-MB TEXT column anymore. Keeping the
// method name lets existing agent tool call sites (wiki_write_page,
// wiki_rename_page) compile unchanged; the body is now intentionally a
// no-op.
//
// The intro that still lives on the index row is managed separately by
// the ingest pipeline (see wikiIngestService.rebuildIndexPage) on batch
// completion, which is where we actually have the LLM + change description
// context needed to rewrite it.
func (s *wikiPageService) RebuildIndexPage(ctx context.Context, kbID string) error {
	_ = ctx
	_ = kbID
	return nil
}
