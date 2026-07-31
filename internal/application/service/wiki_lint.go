package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
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
	PageID      string `json:"page_id,omitempty"`
	PageVersion int    `json:"page_version,omitempty"`
	Description string `json:"description"`
	AutoFixable bool   `json:"auto_fixable"`
	RepairMode  string `json:"repair_mode"`
	Fingerprint string `json:"fingerprint"`
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
	repo             interfaces.WikiPageRepository
}

// NewWikiLintService creates a new wiki lint service
func NewWikiLintService(
	wikiService interfaces.WikiPageService,
	kbService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
	repo interfaces.WikiPageRepository,
) *WikiLintService {
	return &WikiLintService{
		wikiService:      wikiService,
		kbService:        kbService,
		knowledgeService: knowledgeService,
		repo:             repo,
	}
}

// lintCursorBatch is the per-batch limit for the streaming page walk.
// Picked at 200 because wiki pages can carry multi-KB content blobs
// and 200 rows × ~20KB ≈ 4MB resident at a time, which is well within
// what we want to hold while running per-page checks.
const lintCursorBatch = 200

type wikiLintTitlePattern struct {
	Slug       string
	Title      string
	RuneLength int
}

type wikiLintTitleNode struct {
	next    map[rune]int
	fail    int
	outputs []int
}

// wikiLintTitleMatcher is a compact Aho-Corasick matcher. It replaces the
// old per-page × per-entity strings.Contains loop with one linear scan per
// page while preserving the same case-insensitive substring semantics.
type wikiLintTitleMatcher struct {
	nodes    []wikiLintTitleNode
	patterns []wikiLintTitlePattern
}

func newWikiLintTitleMatcher(entitySlugs map[string]string) *wikiLintTitleMatcher {
	m := &wikiLintTitleMatcher{nodes: []wikiLintTitleNode{{next: make(map[rune]int)}}}
	for slug, title := range entitySlugs {
		normalized := []rune(strings.ToLower(strings.TrimSpace(title)))
		if len(normalized) == 0 {
			continue
		}
		patternIndex := len(m.patterns)
		m.patterns = append(m.patterns, wikiLintTitlePattern{Slug: slug, Title: title, RuneLength: len(normalized)})
		state := 0
		for _, ch := range normalized {
			next, ok := m.nodes[state].next[ch]
			if !ok {
				next = len(m.nodes)
				m.nodes[state].next[ch] = next
				m.nodes = append(m.nodes, wikiLintTitleNode{next: make(map[rune]int)})
			}
			state = next
		}
		m.nodes[state].outputs = append(m.nodes[state].outputs, patternIndex)
	}
	queue := make([]int, 0, len(m.nodes))
	for _, child := range m.nodes[0].next {
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		state := queue[head]
		for ch, child := range m.nodes[state].next {
			queue = append(queue, child)
			fallback := m.nodes[state].fail
			for fallback != 0 {
				if next, ok := m.nodes[fallback].next[ch]; ok {
					fallback = next
					break
				}
				fallback = m.nodes[fallback].fail
			}
			if fallback == 0 {
				if next, ok := m.nodes[0].next[ch]; ok && next != child {
					fallback = next
				}
			}
			m.nodes[child].fail = fallback
			m.nodes[child].outputs = append(m.nodes[child].outputs, m.nodes[fallback].outputs...)
		}
	}
	return m
}

func (m *wikiLintTitleMatcher) Find(content string) []wikiLintTitlePattern {
	if m == nil || len(m.patterns) == 0 || content == "" {
		return nil
	}
	state := 0
	seen := make(map[int]struct{})
	contentRunes := []rune(strings.ToLower(content))
	for position, ch := range contentRunes {
		for state != 0 {
			if _, ok := m.nodes[state].next[ch]; ok {
				break
			}
			state = m.nodes[state].fail
		}
		if next, ok := m.nodes[state].next[ch]; ok {
			state = next
		}
		for _, patternIndex := range m.nodes[state].outputs {
			pattern := m.patterns[patternIndex]
			start := position - pattern.RuneLength + 1
			// ASCII entity names need token boundaries so "AI" does not
			// create a second finding inside "OpenAI". CJK adjacency remains
			// valid because those languages do not require spaces around names.
			if start > 0 && isASCIIAlphaNumeric(contentRunes[start-1]) {
				continue
			}
			if position+1 < len(contentRunes) && isASCIIAlphaNumeric(contentRunes[position+1]) {
				continue
			}
			seen[patternIndex] = struct{}{}
		}
	}
	matches := make([]wikiLintTitlePattern, 0, len(seen))
	for i, pattern := range m.patterns {
		if _, ok := seen[i]; ok {
			matches = append(matches, pattern)
		}
	}
	return matches
}

func isASCIIAlphaNumeric(ch rune) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

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
	// missing-cross-ref matcher can be built once. The check itself runs
	// in a second walk because it
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
			if page.PageType != types.WikiPageTypeIndex {
				if len(page.InLinks) == 0 {
					issues = append(issues, WikiLintIssue{
						Type:        LintIssueOrphanPage,
						Severity:    SeverityWarning,
						PageSlug:    page.Slug,
						Description: fmt.Sprintf("Page '%s' has no inbound links — it's disconnected from the wiki", page.Title),
						AutoFixable: false,
						PageID:      page.ID, PageVersion: page.Version, RepairMode: types.WikiIssueRepairManual,
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
						PageID:      page.ID, PageVersion: page.Version, RepairMode: types.WikiIssueRepairDeterministic,
					})
				}
			}

			// Check 3: Empty content.
			content := strings.TrimSpace(page.Content)
			contentLength := utf8.RuneCountInString(content)
			if contentLength < 50 {
				issues = append(issues, WikiLintIssue{
					Type:        LintIssueEmptyContent,
					Severity:    SeverityWarning,
					PageSlug:    page.Slug,
					Description: fmt.Sprintf("Page '%s' has very little content (%d chars)", page.Title, contentLength),
					AutoFixable: false,
					PageID:      page.ID, PageVersion: page.Version, RepairMode: types.WikiIssueRepairAgent,
				})
			}

			// Check 4: Stale source refs — source_refs pointing at
			// soft-deleted knowledge. Cached knowledgeLive lookup keeps
			// per-kid checks O(1) after the first batch encounters
			// each id.
			if s.knowledgeService != nil && page.PageType != types.WikiPageTypeIndex {
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
						if err != nil && !errors.Is(err, repository.ErrKnowledgeNotFound) {
							return nil, fmt.Errorf("check source knowledge %s: %w", kid, err)
						}
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
							AutoFixable: false,
							PageID:      page.ID, PageVersion: page.Version, RepairMode: types.WikiIssueRepairAgent,
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

	// Second-pass walk: missing-cross-ref check. The matcher is built from
	// the full entity title set and scans each page in O(content + matches),
	// avoiding the previous O(pages × entities) nested contains loop.
	titleMatcher := newWikiLintTitleMatcher(entitySlugs)
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
			outLinkSet := make(map[string]struct{}, len(page.OutLinks))
			for _, l := range page.OutLinks {
				outLinkSet[l] = struct{}{}
			}
			for _, match := range titleMatcher.Find(page.Content) {
				if match.Slug == page.Slug {
					continue
				}
				if _, linked := outLinkSet[match.Slug]; linked {
					continue
				}
				issues = append(issues, WikiLintIssue{
					Type:       LintIssueMissingCrossRef,
					Severity:   SeverityInfo,
					PageSlug:   page.Slug,
					TargetSlug: match.Slug,
					Description: fmt.Sprintf(
						"Page '%s' mentions '%s' but doesn't link to [[%s]]",
						page.Title, match.Title, match.Slug,
					),
					AutoFixable: false,
					PageID:      page.ID, PageVersion: page.Version, RepairMode: types.WikiIssueRepairAgent,
				})
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}

	// Calculate health score
	for i := range issues {
		issues[i].Fingerprint = wikiIssueFingerprint(
			kbID, issues[i].PageID, issues[i].PageSlug, string(issues[i].Type), issues[i].TargetSlug,
		)
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

const wikiLintRuleVersion = "2026-07-v1"

// WikiLintTaskPayload identifies one durable lint run queued for execution.
type WikiLintTaskPayload struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	RunID           string `json:"run_id"`
}

// StartRun creates a queued lint run while enforcing one active run per KB.
func (s *WikiLintService) StartRun(
	ctx context.Context, tenantID uint64, kbID string,
) (*types.WikiLintRun, error) {
	run := &types.WikiLintRun{
		ID: uuid.New().String(), TenantID: tenantID, KnowledgeBaseID: kbID,
		Status: "queued", RuleVersion: wikiLintRuleVersion,
	}
	if err := s.repo.CreateLintRun(ctx, run); err != nil {
		return nil, err
	}
	return run, nil
}

// GetRun returns a KB-scoped lint run.
func (s *WikiLintService) GetRun(ctx context.Context, kbID, runID string) (*types.WikiLintRun, error) {
	return s.repo.GetLintRun(ctx, kbID, runID)
}

// GetLatestRun returns the most recently created lint run for a KB.
func (s *WikiLintService) GetLatestRun(ctx context.Context, kbID string) (*types.WikiLintRun, error) {
	return s.repo.GetLatestLintRun(ctx, kbID)
}

// FailRun records an enqueue or execution failure on a durable lint run.
func (s *WikiLintService) FailRun(ctx context.Context, kbID, runID, message string) error {
	run, err := s.repo.GetLintRun(ctx, kbID, runID)
	if err != nil {
		return err
	}
	now := time.Now()
	run.Status, run.ErrorMessage, run.FinishedAt = "failed", message, &now
	return s.repo.UpdateLintRun(ctx, run)
}

// RepairPersistedIssue executes only deterministic, typed repairs. Everything
// else stays bound to the Wiki Fixer session created by the repair endpoint.
func (s *WikiLintService) RepairPersistedIssue(
	ctx context.Context, issue *types.WikiPageIssue, attempt *types.WikiRepairAttempt,
) error {
	if issue == nil || attempt == nil {
		return errors.New("issue and repair attempt are required")
	}
	if issue.IssueType != string(LintIssueBrokenLink) {
		return fmt.Errorf("issue type %s does not have a deterministic repair", issue.IssueType)
	}
	page, err := s.wikiService.GetPageBySlug(ctx, issue.KnowledgeBaseID, issue.Slug)
	if err != nil {
		return err
	}
	repaired, changed, err := s.wikiService.RepairContentLinks(
		ctx, issue.KnowledgeBaseID, page.Slug, page.Content,
	)
	if err != nil {
		return err
	}
	if !changed {
		return errors.New("no unique high-confidence replacement exists for the broken link")
	}
	page.Content = repaired
	if _, err := s.wikiService.UpdatePage(types.WithWikiEditSource(ctx, types.WikiEditSourcePipeline), page); err != nil {
		return err
	}
	return s.wikiService.UpdateIssueStatus(
		ctx, issue.KnowledgeBaseID, issue.ID, types.WikiIssueStatusResolved,
		"Rewrote the broken link to its unique high-confidence live target and verified the target is no longer dangling.",
	)
}

// Handle decodes and executes an asynchronous Wiki lint task.
func (s *WikiLintService) Handle(ctx context.Context, task *asynq.Task) error {
	var payload WikiLintTaskPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("decode wiki lint task: %w", err)
	}
	return s.ProcessRun(ctx, payload)
}

func lintSeverityString(severity WikiLintIssueSeverity) string {
	switch severity {
	case SeverityError:
		return "high"
	case SeverityInfo:
		return "low"
	default:
		return "warning"
	}
}

// ProcessRun scans, persists, and reconciles findings for one complete run.
func (s *WikiLintService) ProcessRun(ctx context.Context, payload WikiLintTaskPayload) (runErr error) {
	run, err := s.repo.GetLintRun(ctx, payload.KnowledgeBaseID, payload.RunID)
	if err != nil {
		return err
	}
	now := time.Now()
	run.Status, run.Progress, run.StartedAt = "running", 5, &now
	if err := s.repo.UpdateLintRun(ctx, run); err != nil {
		return err
	}
	defer func() {
		if runErr == nil {
			return
		}
		finished := time.Now()
		run.Status, run.ErrorMessage, run.FinishedAt = "failed", runErr.Error(), &finished
		_ = s.repo.UpdateLintRun(context.WithoutCancel(ctx), run)
	}()

	report, err := s.RunLint(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return err
	}
	run.Progress = 80
	_ = s.repo.UpdateLintRun(ctx, run)
	seenAt := time.Now()
	for _, finding := range report.Issues {
		evidence, _ := json.Marshal(map[string]interface{}{
			"target_slug":  finding.TargetSlug,
			"rule_version": wikiLintRuleVersion,
		})
		issue := &types.WikiPageIssue{
			ID: uuid.New().String(), TenantID: payload.TenantID, KnowledgeBaseID: payload.KnowledgeBaseID,
			PageID: finding.PageID, Slug: finding.PageSlug, IssueType: string(finding.Type),
			Severity: lintSeverityString(finding.Severity), Source: types.WikiIssueSourceLint,
			Fingerprint: finding.Fingerprint, Description: finding.Description, Evidence: types.JSON(evidence),
			RepairMode: finding.RepairMode, DetectedPageVersion: finding.PageVersion,
			LastSeenRunID: run.ID, LastSeenAt: seenAt, OccurrenceCount: 1,
			Status: types.WikiIssueStatusOpen, ReportedBy: "wiki-lint",
		}
		if err := s.repo.UpsertLintIssue(ctx, issue); err != nil {
			return fmt.Errorf("persist lint finding: %w", err)
		}
	}
	// Reconcile absence only after every detector and every upsert succeeded.
	if err := s.repo.ResolveMissingLintIssues(ctx, payload.KnowledgeBaseID, run.ID, seenAt); err != nil {
		return fmt.Errorf("reconcile lint findings: %w", err)
	}
	finished := time.Now()
	run.Status, run.Progress, run.FindingCount, run.FinishedAt = "completed", 100, len(report.Issues), &finished
	run.ErrorMessage = ""
	return s.repo.UpdateLintRun(ctx, run)
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
			// Only apply the existing high-confidence rewrite helper. A link with
			// no unique live target remains an issue instead of being destructively
			// flattened into plain text.
			if issue.TargetSlug == "" {
				continue
			}
			page, err := s.wikiService.GetPageBySlug(ctx, kbID, issue.PageSlug)
			if err != nil {
				continue
			}
			repaired, changed, err := s.wikiService.RepairContentLinks(ctx, kbID, page.Slug, page.Content)
			if err == nil && changed {
				page.Content = repaired
				pipelineCtx := types.WithWikiEditSource(ctx, types.WikiEditSourcePipeline)
				if _, err := s.wikiService.UpdatePage(pipelineCtx, page); err == nil {
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
