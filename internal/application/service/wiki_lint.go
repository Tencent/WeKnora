package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// WikiLintIssueType defines the type of lint issue
type WikiLintIssueType string

const (
	LintIssueOrphanPage      WikiLintIssueType = "orphan_page"
	LintIssueBrokenLink      WikiLintIssueType = "broken_link"
	LintIssueStaleRef        WikiLintIssueType = "stale_ref"
	LintIssueMissingCrossRef WikiLintIssueType = "missing_cross_ref"
	LintIssueEmptyContent    WikiLintIssueType = "empty_content"
	LintIssueDuplicateSlug   WikiLintIssueType = "duplicate_slug"
)

// WikiLintIssueSeverity defines the severity of a lint issue
type WikiLintIssueSeverity string

const (
	SeverityInfo    WikiLintIssueSeverity = "info"
	SeverityWarning WikiLintIssueSeverity = "warning"
	SeverityError   WikiLintIssueSeverity = "error"
)

// WikiLintIssue represents a single lint finding
type WikiLintIssue struct {
	Type     WikiLintIssueType     `json:"type"`
	Severity WikiLintIssueSeverity `json:"severity"`
	PageSlug string                `json:"page_slug"`
	// TargetSlug identifies the other page involved in the issue (e.g. the
	// broken link target, or the entity slug for a missing cross-ref). It is
	// the structured field used by AutoFix instead of parsing Description.
	TargetSlug  string `json:"target_slug,omitempty"`
	Description string `json:"description"`
	AutoFixable bool   `json:"auto_fixable"`
}

// WikiLintReport is the complete lint report for a wiki KB
type WikiLintReport struct {
	KnowledgeBaseID string           `json:"knowledge_base_id"`
	Issues          []WikiLintIssue  `json:"issues"`
	HealthScore     int              `json:"health_score"` // 0-100
	Stats           *types.WikiStats `json:"stats"`
	Summary         string           `json:"summary"`
}

// WikiLintService provides wiki health checking capabilities
type WikiLintService struct {
	wikiService      interfaces.WikiPageService
	kbService        interfaces.KnowledgeBaseService
	knowledgeService interfaces.KnowledgeService
}

// NewWikiLintService creates a new wiki lint service
func NewWikiLintService(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
) *WikiLintService {
	return &WikiLintService{
		wikiService:      wikiService,
		kbService:        kbService,
		knowledgeService: knowledgeService,
	}
}

// lintCursorBatch is the per-batch limit for the streaming page walk.
// Picked at 200 because wiki pages can carry multi-KB content blobs
// and 200 rows × ~20KB ≈ 4MB resident at a time, which is well within
// what we want to hold while running per-page checks.
const lintCursorBatch = 200

// RunLint performs a comprehensive health check on a wiki knowledge base.
//
// At 4w-document scale the legacy "load every page in one shot"
// approach was the dominant tail in this method (and intermittently
// caused OOM in production). We now walk the page set via
// ListPagesCursor in lintCursorBatch-sized windows, accumulating
// issues incrementally — memory stays bounded regardless of KB size.
//
// We also drop the GetGraph(Limit:0) call that the legacy path used
// to compute the live-slug set. ListAllSlugs is a one-column projection
// over the same predicate (kbID + status<>archived), so it gives the
// same answer at a fraction of the cost.
func (s *WikiLintService) RunLint(ctx context.Context, kbID string) (*WikiLintReport, error) {
	// Validate KB
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get KB: %w", err)
	}
	if !kb.IsWikiEnabled() {
		return nil, fmt.Errorf("KB %s is not a wiki type", kbID)
	}

	// Get stats
	stats, err := s.wikiService.GetStats(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}

	// Compute the live-slug set from the cheap one-column projection.
	// This replaces a full GetGraph call (which materialized every node
	// + edge) with a single Pluck("slug") query.
	liveSlugs, err := s.wikiService.ListAllSlugs(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("list all slugs: %w", err)
	}
	slugSet := make(map[string]bool, len(liveSlugs))
	for _, slug := range liveSlugs {
		slugSet[slug] = true
	}

	var issues []WikiLintIssue
	healthScore := 100
	knowledgeLive := make(map[string]bool) // kid -> exists; cached across pages

	// First-pass walk: orphan / broken-link / empty / stale-ref
	// detection. Each check is independent of order; we accumulate
	// issues per-batch and the cursor walk keeps memory bounded.
	//
	// We collect entity / concept titles in this pass too so the
	// missing-cross-ref check (which is intrinsically O(N×M) in
	// distinct entities × pages) doesn't need a second walk to find
	// candidates. The check itself runs in a second walk because it
	// needs the full entity-title set to compare against any page.
	entitySlugs := make(map[string]string) // slug -> title

	cursor := ""
	for {
		pages, next, err := s.wikiService.ListPagesCursor(ctx, kbID, cursor, lintCursorBatch)
		if err != nil {
			return nil, fmt.Errorf("list pages cursor: %w", err)
		}
		if len(pages) == 0 {
			break
		}
		for _, page := range pages {
			// Track entity/concept titles for the second pass.
			if page.PageType == types.WikiPageTypeEntity || page.PageType == types.WikiPageTypeConcept {
				entitySlugs[page.Slug] = page.Title
			}

			// Check 1: Orphan pages (no inbound links, excluding system pages).
			if page.PageType != types.WikiPageTypeIndex && page.PageType != types.WikiPageTypeLog {
				if len(page.InLinks) == 0 {
					issues = append(issues, WikiLintIssue{
						Type:        LintIssueOrphanPage,
						Severity:    SeverityWarning,
						PageSlug:    page.Slug,
						Description: fmt.Sprintf("Page '%s' has no inbound links — it's disconnected from the wiki", page.Title),
						AutoFixable: false,
					})
				}
			}

			// Check 2: Broken links — outlinks pointing at slugs that
			// don't exist in the live set.
			for _, outLink := range page.OutLinks {
				if !slugSet[outLink] {
					issues = append(issues, WikiLintIssue{
						Type:        LintIssueBrokenLink,
						Severity:    SeverityError,
						PageSlug:    page.Slug,
						TargetSlug:  outLink,
						Description: fmt.Sprintf("Page '%s' links to [[%s]] which does not exist", page.Title, outLink),
						AutoFixable: true,
					})
				}
			}

			// Check 3: Empty content.
			content := strings.TrimSpace(page.Content)
			if len(content) < 50 {
				issues = append(issues, WikiLintIssue{
					Type:        LintIssueEmptyContent,
					Severity:    SeverityWarning,
					PageSlug:    page.Slug,
					Description: fmt.Sprintf("Page '%s' has very little content (%d chars)", page.Title, len(content)),
					AutoFixable: true,
				})
			}

			// Check 4: Stale source refs — source_refs pointing at
			// soft-deleted knowledge. Cached knowledgeLive lookup keeps
			// per-kid checks O(1) after the first batch encounters
			// each id.
			if s.knowledgeService != nil &&
				page.PageType != types.WikiPageTypeIndex &&
				page.PageType != types.WikiPageTypeLog {
				for _, ref := range page.SourceRefs {
					kid := ref
					if i := strings.Index(ref, "|"); i > 0 {
						kid = ref[:i]
					}
					if kid == "" {
						continue
					}
					live, seen := knowledgeLive[kid]
					if !seen {
						kn, err := s.knowledgeService.GetKnowledgeByIDOnly(ctx, kid)
						live = err == nil && kn != nil
						knowledgeLive[kid] = live
					}
					if !live {
						issues = append(issues, WikiLintIssue{
							Type:        LintIssueStaleRef,
							Severity:    SeverityError,
							PageSlug:    page.Slug,
							TargetSlug:  kid,
							Description: fmt.Sprintf("Page '%s' references deleted knowledge %s", page.Title, kid),
							AutoFixable: true,
						})
					}
				}
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	// Second-pass walk: missing-cross-ref check. This needs the full
	// entitySlugs map (built in pass 1), so it has to be a separate
	// pass — but it's still streaming.
	cursor = ""
	for {
		pages, next, err := s.wikiService.ListPagesCursor(ctx, kbID, cursor, lintCursorBatch)
		if err != nil {
			return nil, fmt.Errorf("list pages cursor (pass 2): %w", err)
		}
		if len(pages) == 0 {
			break
		}
		for _, page := range pages {
			lowerContent := strings.ToLower(page.Content)
			outLinkSet := make(map[string]struct{}, len(page.OutLinks))
			for _, l := range page.OutLinks {
				outLinkSet[l] = struct{}{}
			}
			for slug, title := range entitySlugs {
				if slug == page.Slug || title == "" {
					continue
				}
				if !strings.Contains(lowerContent, strings.ToLower(title)) {
					continue
				}
				if _, linked := outLinkSet[slug]; linked {
					continue
				}
				issues = append(issues, WikiLintIssue{
					Type:        LintIssueMissingCrossRef,
					Severity:    SeverityInfo,
					PageSlug:    page.Slug,
					TargetSlug:  slug,
					Description: fmt.Sprintf("Page '%s' mentions '%s' but doesn't link to [[%s]]", page.Title, title, slug),
					AutoFixable: false,
				})
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	// Calculate health score
	if stats.TotalPages > 0 {
		// Penalize for orphans
		orphanPct := float64(stats.OrphanCount) / float64(stats.TotalPages) * 100
		if orphanPct > 50 {
			healthScore -= 25
		} else if orphanPct > 25 {
			healthScore -= 10
		}

		// Penalize for broken links
		brokenCount := 0
		for _, issue := range issues {
			if issue.Type == LintIssueBrokenLink {
				brokenCount++
			}
		}
		healthScore -= brokenCount * 5

		// Penalize for no links at all
		if stats.TotalLinks == 0 && stats.TotalPages > 2 {
			healthScore -= 15
		}

		// Penalize for empty pages
		emptyCount := 0
		for _, issue := range issues {
			if issue.Type == LintIssueEmptyContent {
				emptyCount++
			}
		}
		healthScore -= emptyCount * 3
	}

	if healthScore < 0 {
		healthScore = 0
	}

	// Generate summary
	var summary strings.Builder
	errorCount := 0
	warningCount := 0
	infoCount := 0
	for _, issue := range issues {
		switch issue.Severity {
		case SeverityError:
			errorCount++
		case SeverityWarning:
			warningCount++
		case SeverityInfo:
			infoCount++
		}
	}

	if len(issues) == 0 {
		summary.WriteString("Wiki is healthy! No issues found.")
	} else {
		fmt.Fprintf(&summary, "Found %d issues: %d errors, %d warnings, %d suggestions.",
			len(issues), errorCount, warningCount, infoCount)
	}

	report := &WikiLintReport{
		KnowledgeBaseID: kbID,
		Issues:          issues,
		HealthScore:     healthScore,
		Stats:           stats,
		Summary:         summary.String(),
	}

	logger.Infof(ctx, "wiki lint: KB %s — health score %d/100, %d issues", kbID, healthScore, len(issues))

	return report, nil
}

// AutoFix attempts to automatically fix fixable issues
func (s *WikiLintService) AutoFix(ctx context.Context, kbID string) (int, error) {
	report, err := s.RunLint(ctx, kbID)
	if err != nil {
		return 0, err
	}

	fixed := 0
	for _, issue := range report.Issues {
		if !issue.AutoFixable {
			continue
		}

		switch issue.Type {
		case LintIssueBrokenLink:
			// Replace [[broken-slug]] with plain text so the reference text is
			// preserved but no longer renders as a dangling wiki link.
			if issue.TargetSlug == "" {
				continue
			}
			page, err := s.wikiService.GetPageBySlug(ctx, kbID, issue.PageSlug)
			if err != nil {
				continue
			}
			target := issue.TargetSlug
			page.Content = strings.ReplaceAll(page.Content, "[["+target+"]]", target)
			if err := s.wikiService.UpdateAutoLinkedContent(ctx, page); err == nil {
				fixed++
			}

		case LintIssueEmptyContent:
			// Archive pages with very little content instead of deleting
			page, err := s.wikiService.GetPageBySlug(ctx, kbID, issue.PageSlug)
			if err != nil {
				continue
			}
			// Don't archive index or log pages
			if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
				continue
			}
			page.Status = types.WikiPageStatusArchived
			if _, err := s.wikiService.UpdatePage(ctx, page); err == nil {
				fixed++
			}

		case LintIssueStaleRef:
			// Strip source_refs that point at soft-deleted knowledge. If the
			// page has no other live sources, delete it outright — leaving
			// an orphan summary page is worse than removing it, because the
			// model would still link to it from other pages and the
			// wiki_read_source_doc drill-down would always fail.
			if issue.TargetSlug == "" {
				continue
			}
			page, err := s.wikiService.GetPageBySlug(ctx, kbID, issue.PageSlug)
			if err != nil || page == nil {
				continue
			}
			if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
				continue
			}
			remaining := removeSourceRef(page.SourceRefs, issue.TargetSlug)
			if len(remaining) == 0 {
				if err := s.wikiService.DeletePage(ctx, kbID, page.Slug); err == nil {
					fixed++
				}
			} else if len(remaining) != len(page.SourceRefs) {
				page.SourceRefs = remaining
				if err := s.wikiService.UpdatePageMeta(ctx, page); err == nil {
					fixed++
				}
			}
		}
	}

	// Rebuild links after fixes
	if fixed > 0 {
		_ = s.wikiService.RebuildLinks(ctx, kbID)
	}

	logger.Infof(ctx, "wiki auto-fix: KB %s — fixed %d issues", kbID, fixed)
	return fixed, nil
}
