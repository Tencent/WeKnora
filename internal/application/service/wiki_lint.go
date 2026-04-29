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
	LintIssueOrphanPage     WikiLintIssueType = "orphan_page"
	LintIssueBrokenLink     WikiLintIssueType = "broken_link"
	LintIssueStaleRef       WikiLintIssueType = "stale_ref"
	LintIssueMissingCrossRef WikiLintIssueType = "missing_cross_ref"
	LintIssueEmptyContent   WikiLintIssueType = "empty_content"
	LintIssueDuplicateSlug  WikiLintIssueType = "duplicate_slug"
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

// RunLint performs a comprehensive health check on a wiki knowledge base
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

	// Get graph for link analysis
	graph, err := s.wikiService.GetGraph(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get graph: %w", err)
	}

	// Get all pages for detailed analysis
	resp, err := s.wikiService.ListPages(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: kbID,
		PageSize:        500,
	})
	if err != nil {
		return nil, fmt.Errorf("list pages: %w", err)
	}

	var issues []WikiLintIssue
	healthScore := 100

	// Build slug set for link validation
	slugSet := make(map[string]bool)
	for _, node := range graph.Nodes {
		slugSet[node.Slug] = true
	}

	// Check 1: Orphan pages (no inbound links, excluding index/log)
	for _, page := range resp.Pages {
		if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
			continue
		}
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

	// Check 2: Broken links
	for _, page := range resp.Pages {
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
	}

	// Check 3: Empty content
	for _, page := range resp.Pages {
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
	}

	// Check 4: Stale source refs — source_refs pointing at soft-deleted
	// knowledge. This is the primary self-heal for the ingest/delete race
	// condition: if a wiki_ingest task managed to slip past the in-flight
	// guards and wrote a page for a knowledge that has since been deleted,
	// the page becomes a dead-end (wiki_read_source_doc returns "knowledge
	// not found"). Flag it so AutoFix can strip the ref, and delete the
	// page if no live refs remain.
	if s.knowledgeService != nil {
		knowledgeLive := make(map[string]bool) // kid -> exists
		for _, page := range resp.Pages {
			// Skip wiki-intrinsic pages: index/log never carry SourceRefs
			// anyway, and accidentally flagging them would risk AutoFix
			// deleting system pages.
			if page.PageType == types.WikiPageTypeIndex || page.PageType == types.WikiPageTypeLog {
				continue
			}
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

	// Check 5: Missing cross-references (entities mentioned in content but not linked)
	entitySlugs := make(map[string]string) // slug -> title
	for _, page := range resp.Pages {
		if page.PageType == types.WikiPageTypeEntity || page.PageType == types.WikiPageTypeConcept {
			entitySlugs[page.Slug] = page.Title
		}
	}
	for _, page := range resp.Pages {
		for slug, title := range entitySlugs {
			if slug == page.Slug {
				continue
			}
			// Check if title is mentioned in content but not linked
			if strings.Contains(strings.ToLower(page.Content), strings.ToLower(title)) {
				linked := false
				for _, l := range page.OutLinks {
					if l == slug {
						linked = true
						break
					}
				}
				if !linked {
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
		}
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
