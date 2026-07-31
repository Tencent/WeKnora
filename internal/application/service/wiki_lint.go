package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// WikiLintIssue represents a single lint finding. Instances are built through
// wikiLintRule.finding so a finding's severity and repair mode always match the
// rule that knows how to verify it — see wiki_lint_rules.go.
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

// WikiLintReport is the lint report for a wiki KB.
//
// Issues carries at most wikiLintReportMaxIssues entries so the synchronous
// endpoint never has to serialize an unbounded finding set; TotalIssues and
// Truncated describe the full picture the walk actually saw.
type WikiLintReport struct {
	KnowledgeBaseID string           `json:"knowledge_base_id"`
	Issues          []WikiLintIssue  `json:"issues"`
	TotalIssues     int              `json:"total_issues"`
	Truncated       bool             `json:"truncated"`
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

const (
	// wikiLintReportMaxIssues caps what the synchronous report materializes.
	// The walk still counts every finding, so health score and summary stay
	// exact — we just refuse to build a multi-hundred-thousand element JSON
	// array for a human-facing overview.
	wikiLintReportMaxIssues = 500

	// wikiCrossRefPerPageLimit caps the advisory cross-reference suggestions a
	// single page may contribute. Without it one long page that name-drops
	// hundreds of entities drowns out every other page's suggestions.
	wikiCrossRefPerPageLimit = 5

	// wikiLintUpsertBatch is the persistence window for a durable run. Batching
	// turns N single-row round-trips into N/200, which is what makes a
	// full-KB scan viable at 4w-page scale.
	wikiLintUpsertBatch = 200
)

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
	// Patterns are registered in slug order so a pattern's index — and therefore
	// the order Find reports hits in — is a property of the input set rather than
	// of map iteration. Callers that keep only the first N hits depend on this:
	// a shifting selection would give the same page different findings on every
	// run, and each run would close the previous run's suggestions.
	slugs := make([]string, 0, len(entitySlugs))
	for slug := range entitySlugs {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	for _, slug := range slugs {
		title := entitySlugs[slug]
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
	if len(seen) == 0 {
		return nil
	}
	// Collect from the hits, not from every registered pattern: a full pattern
	// sweep per page would reintroduce the O(pages × entities) cost that the
	// automaton exists to remove. Sorting keeps the output order stable so a
	// finding's identity does not depend on map iteration order.
	indexes := make([]int, 0, len(seen))
	for i := range seen {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	matches := make([]wikiLintTitlePattern, 0, len(indexes))
	for _, i := range indexes {
		matches = append(matches, m.patterns[i])
	}
	return matches
}

func isASCIIAlphaNumeric(ch rune) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

// wikiLintScan is the aggregate view of one complete walk. The counters are
// exact even when the caller materializes only a subset of the findings, so
// health score and summary never depend on how much a consumer chose to keep.
type wikiLintScan struct {
	Total      int
	ByType     map[WikiLintIssueType]int
	BySeverity map[WikiLintIssueSeverity]int
	Stats      *types.WikiStats
}

// scanWiki walks a wiki KB once and hands every finding to emit.
//
// Both the page walk and the finding stream are bounded here: pages arrive in
// lintCursorBatch windows, and findings are never accumulated. The caller
// decides what to retain — the synchronous report keeps a capped slice, a
// durable run batches straight into the problem centre — so peak memory stops
// scaling with the number of defects a KB happens to have. That matters most
// for the advisory cross-reference rule, which fires once per (page, mentioned
// entity) pair.
//
// The live-slug set comes from ListAllSlugs, a one-column projection over the
// same predicate a full GetGraph would use (kbID + status<>archived), so it
// answers "does this link target exist" at a fraction of the cost.
//
// progress, when non-nil, receives a coarse 0-100 estimate as the two passes
// advance. It is a UI hint, not a guaranteed cadence.
func (s *WikiLintService) scanWiki(
	ctx context.Context,
	kbID string,
	emit func(WikiLintIssue) error,
	progress func(percent int),
) (*wikiLintScan, error) {
	kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get KB: %w", err)
	}
	if !kb.IsWikiEnabled() {
		return nil, fmt.Errorf("KB %s is not a wiki type", kbID)
	}

	stats, err := s.wikiService.GetStats(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}

	liveSlugs, err := s.wikiService.ListAllSlugs(ctx, kbID)
	if err != nil {
		return nil, fmt.Errorf("list all slugs: %w", err)
	}
	slugSet := make(map[string]bool, len(liveSlugs))
	for _, slug := range liveSlugs {
		slugSet[slug] = true
	}

	scan := &wikiLintScan{
		ByType:     make(map[WikiLintIssueType]int),
		BySeverity: make(map[WikiLintIssueSeverity]int),
		Stats:      stats,
	}
	// The fingerprint is the finding's durable identity, so it is stamped at
	// the single point every finding passes through.
	emitFinding := func(finding WikiLintIssue) error {
		finding.Fingerprint = wikiIssueFingerprint(
			kbID, finding.PageID, finding.PageSlug, string(finding.Type), finding.TargetSlug,
		)
		scan.Total++
		scan.ByType[finding.Type]++
		scan.BySeverity[finding.Severity]++
		return emit(finding)
	}

	reporter := newWikiLintProgress(stats.TotalPages, progress)
	knowledgeLive := make(map[string]bool) // kid -> exists; cached across pages

	// First pass: orphan / broken-link / empty / stale-ref detection. Every
	// check is order-independent. Entity and concept titles are collected here
	// so the cross-reference matcher can be built once; that check needs the
	// complete title set before it can look at any page, hence the second walk.
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
			if page.PageType == types.WikiPageTypeEntity || page.PageType == types.WikiPageTypeConcept {
				entitySlugs[page.Slug] = page.Title
			}
			if err := s.scanPageDefects(ctx, page, slugSet, knowledgeLive, emitFinding); err != nil {
				return nil, err
			}
		}
		reporter.advance(len(pages))
		if next == "" {
			break
		}
		cursor = next
	}

	// Second pass: advisory cross-reference suggestions. The automaton scans
	// each page in O(content + hits) instead of the O(pages × entities) nested
	// contains loop it replaced.
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
			if err := scanPageCrossRefs(page, titleMatcher, emitFinding); err != nil {
				return nil, err
			}
		}
		reporter.advance(len(pages))
		if next == "" {
			break
		}
		cursor = next
	}

	return scan, nil
}

// scanPageDefects runs the per-page defect rules. knowledgeLive is a shared
// cache so a knowledge id referenced by many pages costs one lookup overall.
func (s *WikiLintService) scanPageDefects(
	ctx context.Context,
	page *types.WikiPage,
	slugSet map[string]bool,
	knowledgeLive map[string]bool,
	emit func(WikiLintIssue) error,
) error {
	if page.PageType != types.WikiPageTypeIndex && len(page.InLinks) == 0 {
		if err := emit(wikiRuleOrphanPage.finding(page, "", fmt.Sprintf(
			"Page '%s' has no inbound links — it's disconnected from the wiki", page.Title,
		))); err != nil {
			return err
		}
	}

	for _, outLink := range page.OutLinks {
		if slugSet[outLink] {
			continue
		}
		if err := emit(wikiRuleBrokenLink.finding(page, outLink, fmt.Sprintf(
			"Page '%s' links to [[%s]] which does not exist", page.Title, outLink,
		))); err != nil {
			return err
		}
	}

	if runes := wikiContentRunes(page.Content); runes < wikiMinContentRunes {
		if err := emit(wikiRuleEmptyContent.finding(page, "", fmt.Sprintf(
			"Page '%s' has very little content (%d chars)", page.Title, runes,
		))); err != nil {
			return err
		}
	}

	if s.knowledgeService == nil || page.PageType == types.WikiPageTypeIndex {
		return nil
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
			if err != nil && !errors.Is(err, repository.ErrKnowledgeNotFound) {
				return fmt.Errorf("check source knowledge %s: %w", kid, err)
			}
			live = err == nil && kn != nil
			knowledgeLive[kid] = live
		}
		if live {
			continue
		}
		if err := emit(wikiRuleStaleRef.finding(page, kid, fmt.Sprintf(
			"Page '%s' references deleted knowledge %s", page.Title, kid,
		))); err != nil {
			return err
		}
	}
	return nil
}

// scanPageCrossRefs emits at most wikiCrossRefPerPageLimit suggestions for one
// page. Matches arrive in a stable order, so the same page yields the same
// suggestions on every run rather than a shifting sample.
func scanPageCrossRefs(
	page *types.WikiPage,
	matcher *wikiLintTitleMatcher,
	emit func(WikiLintIssue) error,
) error {
	outLinkSet := make(map[string]struct{}, len(page.OutLinks))
	for _, link := range page.OutLinks {
		outLinkSet[link] = struct{}{}
	}
	emitted := 0
	for _, match := range matcher.Find(page.Content) {
		if emitted >= wikiCrossRefPerPageLimit {
			break
		}
		if match.Slug == page.Slug {
			continue
		}
		if _, linked := outLinkSet[match.Slug]; linked {
			continue
		}
		if err := emit(wikiRuleMissingCrossRef.finding(page, match.Slug, fmt.Sprintf(
			"Page '%s' mentions '%s' but doesn't link to [[%s]]", page.Title, match.Title, match.Slug,
		))); err != nil {
			return err
		}
		emitted++
	}
	return nil
}

// RunLint performs a comprehensive health check on a wiki knowledge base and
// returns a human-facing report. Findings beyond wikiLintReportMaxIssues are
// counted but not materialized; Truncated tells the caller that happened.
func (s *WikiLintService) RunLint(ctx context.Context, kbID string) (*WikiLintReport, error) {
	issues := make([]WikiLintIssue, 0, wikiLintReportMaxIssues)
	scan, err := s.scanWiki(ctx, kbID, func(finding WikiLintIssue) error {
		if len(issues) < wikiLintReportMaxIssues {
			issues = append(issues, finding)
		}
		return nil
	}, nil)
	if err != nil {
		return nil, err
	}

	report := &WikiLintReport{
		KnowledgeBaseID: kbID,
		Issues:          issues,
		TotalIssues:     scan.Total,
		Truncated:       scan.Total > len(issues),
		HealthScore:     wikiLintHealthScore(scan),
		Stats:           scan.Stats,
		Summary:         wikiLintSummary(scan),
	}

	logger.Infof(ctx, "wiki lint: KB %s — health score %d/100, %d issues",
		kbID, report.HealthScore, scan.Total)

	return report, nil
}

// wikiLintHealthScore turns the scan counters into a 0-100 score. Penalties are
// unchanged from the original inline computation; only the inputs moved from a
// materialized issue slice to the exact per-type counters.
func wikiLintHealthScore(scan *wikiLintScan) int {
	score := 100
	stats := scan.Stats
	if stats == nil || stats.TotalPages == 0 {
		return score
	}

	orphanPct := float64(stats.OrphanCount) / float64(stats.TotalPages) * 100
	if orphanPct > 50 {
		score -= 25
	} else if orphanPct > 25 {
		score -= 10
	}

	score -= scan.ByType[LintIssueBrokenLink] * 5
	if stats.TotalLinks == 0 && stats.TotalPages > 2 {
		score -= 15
	}
	score -= scan.ByType[LintIssueEmptyContent] * 3

	if score < 0 {
		return 0
	}
	return score
}

// wikiLintSummary renders the one-line verdict shown above the report.
func wikiLintSummary(scan *wikiLintScan) string {
	if scan.Total == 0 {
		return "Wiki is healthy! No issues found."
	}
	return fmt.Sprintf("Found %d issues: %d errors, %d warnings, %d suggestions.",
		scan.Total,
		scan.BySeverity[SeverityError],
		scan.BySeverity[SeverityWarning],
		scan.BySeverity[SeverityInfo],
	)
}

// wikiLintProgress converts "pages walked" into the coarse percentage a lint
// run publishes. Two passes over totalPages make up the scan, and the band is
// deliberately narrow (5-95) so the caller keeps 0 for "queued" and 100 for
// "committed and reconciled".
type wikiLintProgress struct {
	totalUnits int64
	done       int64
	last       int
	report     func(percent int)
}

func newWikiLintProgress(totalPages int64, report func(percent int)) *wikiLintProgress {
	return &wikiLintProgress{totalUnits: totalPages * 2, last: -1, report: report}
}

// advance records walked pages and publishes the percentage when it moved by a
// visible step, so a large KB does not issue one progress write per batch.
func (p *wikiLintProgress) advance(pages int) {
	if p == nil || p.report == nil || p.totalUnits <= 0 {
		return
	}
	p.done += int64(pages)
	percent := 5 + int(float64(p.done)/float64(p.totalUnits)*90)
	if percent > 95 {
		percent = 95
	}
	if percent-p.last < 5 {
		return
	}
	p.last = percent
	p.report(percent)
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
	rule, ok := wikiLintRuleFor(issue.IssueType)
	if !ok || rule.RepairMode != types.WikiIssueRepairDeterministic {
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
//
// Findings stream into wikiLintUpsertBatch-sized writes rather than being
// collected first, so a KB with many defects costs the same memory as a healthy
// one. Only durable rules are persisted — advisory suggestions belong to the
// synchronous report, not the problem centre.
//
// Reconciliation runs last and only on the success path: closing issues by
// absence is sound only after every detector and every write has landed, and
// because the scan is never truncated, absence really does mean absence.
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

	seenAt := time.Now()
	persisted := 0
	batch := make([]*types.WikiPageIssue, 0, wikiLintUpsertBatch)
	// Fingerprints are deduplicated within a batch because a single upsert
	// statement cannot touch the same conflict target twice.
	inBatch := make(map[string]struct{}, wikiLintUpsertBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.repo.UpsertLintIssues(ctx, batch); err != nil {
			return fmt.Errorf("persist lint findings: %w", err)
		}
		persisted += len(batch)
		batch = batch[:0]
		clear(inBatch)
		return nil
	}

	scan, err := s.scanWiki(ctx, payload.KnowledgeBaseID, func(finding WikiLintIssue) error {
		rule, ok := wikiLintRuleFor(string(finding.Type))
		if !ok || !rule.Durable {
			return nil
		}
		if _, dup := inBatch[finding.Fingerprint]; dup {
			return nil
		}
		inBatch[finding.Fingerprint] = struct{}{}
		batch = append(batch, wikiLintIssueRecord(payload, run.ID, seenAt, finding))
		if len(batch) < wikiLintUpsertBatch {
			return nil
		}
		return flush()
	}, func(percent int) {
		run.Progress = percent
		_ = s.repo.UpdateLintRun(ctx, run)
	})
	if err != nil {
		return err
	}
	if err := flush(); err != nil {
		return err
	}

	if err := s.repo.ResolveMissingLintIssues(ctx, payload.KnowledgeBaseID, run.ID, seenAt); err != nil {
		return fmt.Errorf("reconcile lint findings: %w", err)
	}

	finished := time.Now()
	run.Status, run.Progress, run.FindingCount, run.FinishedAt = "completed", 100, persisted, &finished
	run.ErrorMessage = ""
	logger.Infof(ctx, "wiki lint run %s: KB %s — %d findings scanned, %d persisted",
		run.ID, payload.KnowledgeBaseID, scan.Total, persisted)
	return s.repo.UpdateLintRun(ctx, run)
}

// wikiLintIssueRecord projects a finding onto its durable problem-centre row.
func wikiLintIssueRecord(
	payload WikiLintTaskPayload, runID string, seenAt time.Time, finding WikiLintIssue,
) *types.WikiPageIssue {
	evidence, _ := json.Marshal(map[string]interface{}{
		"target_slug":  finding.TargetSlug,
		"rule_version": wikiLintRuleVersion,
	})
	return &types.WikiPageIssue{
		ID: uuid.New().String(), TenantID: payload.TenantID, KnowledgeBaseID: payload.KnowledgeBaseID,
		PageID: finding.PageID, Slug: finding.PageSlug, IssueType: string(finding.Type),
		Severity: lintSeverityString(finding.Severity), Source: types.WikiIssueSourceLint,
		Fingerprint: finding.Fingerprint, Description: finding.Description, Evidence: types.JSON(evidence),
		RepairMode: finding.RepairMode, DetectedPageVersion: finding.PageVersion,
		LastSeenRunID: runID, LastSeenAt: seenAt, OccurrenceCount: 1,
		Status: types.WikiIssueStatusOpen, ReportedBy: "wiki-lint",
	}
}

// AutoFix applies deterministic repairs as the walk finds them. It consumes the
// scan directly rather than RunLint's capped report so a large KB is fixed in
// full, and a page whose links were already rewritten by an earlier finding is
// skipped instead of written twice.
func (s *WikiLintService) AutoFix(ctx context.Context, kbID string) (int, error) {
	fixed := 0
	repairedPages := make(map[string]struct{})
	_, err := s.scanWiki(ctx, kbID, func(finding WikiLintIssue) error {
		if !finding.AutoFixable || finding.TargetSlug == "" {
			return nil
		}
		if _, done := repairedPages[finding.PageSlug]; done {
			return nil
		}
		// Only the high-confidence rewrite helper is allowed here. A link with
		// no unique live target stays an open finding instead of being
		// destructively flattened into plain text.
		page, err := s.wikiService.GetPageBySlug(ctx, kbID, finding.PageSlug)
		if err != nil {
			return nil
		}
		repaired, changed, err := s.wikiService.RepairContentLinks(ctx, kbID, page.Slug, page.Content)
		if err != nil || !changed {
			return nil
		}
		page.Content = repaired
		pipelineCtx := types.WithWikiEditSource(ctx, types.WikiEditSourcePipeline)
		if _, err := s.wikiService.UpdatePage(pipelineCtx, page); err != nil {
			return nil
		}
		repairedPages[finding.PageSlug] = struct{}{}
		fixed++
		return nil
	}, nil)
	if err != nil {
		return 0, err
	}

	if fixed > 0 {
		_ = s.wikiService.RebuildLinks(ctx, kbID)
	}

	logger.Infof(ctx, "wiki auto-fix: KB %s — fixed %d pages", kbID, fixed)
	return fixed, nil
}
