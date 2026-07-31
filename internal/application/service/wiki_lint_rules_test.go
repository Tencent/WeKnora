package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWikiLintRegistryIsInternallyConsistent is the guard rail behind the
// registry: it fails when a rule is added with half its contract filled in.
// Previously severity, repair mode and the postcondition lived in three
// different switch statements, so a new rule could ship detecting findings that
// nothing could ever verify — and it would silently fall into the semantic
// version-bump fallback instead of being rejected.
func TestWikiLintRegistryIsInternallyConsistent(t *testing.T) {
	require.NotEmpty(t, wikiLintRules)

	validSeverities := map[WikiLintIssueSeverity]bool{
		SeverityInfo: true, SeverityWarning: true, SeverityError: true,
	}
	validModes := map[string]bool{
		types.WikiIssueRepairManual:        true,
		types.WikiIssueRepairAgent:         true,
		types.WikiIssueRepairDeterministic: true,
	}

	for key, rule := range wikiLintRules {
		t.Run(string(key), func(t *testing.T) {
			assert.Equal(t, key, rule.Type, "map key must match the rule it indexes")
			assert.NotNil(t, rule.Verify, "every rule needs a postcondition")
			assert.True(t, validSeverities[rule.Severity], "severity %q is not a known level", rule.Severity)
			assert.True(t, validModes[rule.RepairMode], "repair mode %q is not a known mode", rule.RepairMode)
			if rule.AutoFixable {
				assert.Equal(t, types.WikiIssueRepairDeterministic, rule.RepairMode,
					"only a deterministic rule may be swept by AutoFix")
			}
			if rule.Severity == SeverityInfo {
				assert.False(t, rule.Durable,
					"advisory findings must stay out of the problem centre")
			}
		})
	}
}

// TestWikiLintFindingInheritsRuleMetadata pins the reason findings are built
// through a rule: a detector supplies only what it observed, so a finding's
// severity and repair mode cannot drift from the rule that verifies it.
func TestWikiLintFindingInheritsRuleMetadata(t *testing.T) {
	page := &types.WikiPage{ID: "page-1", Slug: "concept/one", Version: 7}
	finding := wikiRuleBrokenLink.finding(page, "concept/missing", "dangling")

	assert.Equal(t, LintIssueBrokenLink, finding.Type)
	assert.Equal(t, SeverityError, finding.Severity)
	assert.Equal(t, types.WikiIssueRepairDeterministic, finding.RepairMode)
	assert.True(t, finding.AutoFixable)
	assert.Equal(t, "page-1", finding.PageID)
	assert.Equal(t, "concept/one", finding.PageSlug)
	assert.Equal(t, 7, finding.PageVersion)
	assert.Equal(t, "concept/missing", finding.TargetSlug)
}

// TestVerifyPostconditionRejectsMissingTargetEvidence covers the case that used
// to slip through: a rule needing a counterpart whose evidence was lost must
// refuse to close rather than treat "nothing to check" as "resolved".
func TestVerifyPostconditionRejectsMissingTargetEvidence(t *testing.T) {
	in := wikiVerifyInput{
		Issue:   &types.WikiPageIssue{IssueType: string(LintIssueBrokenLink)},
		Page:    &types.WikiPage{Version: 3},
		Attempt: &types.WikiRepairAttempt{BeforeVersion: 1},
	}
	err := verifyWikiIssuePostcondition(context.Background(), in)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing target evidence")
}

// TestVerifyPostconditionFallsBackToSemanticProgress documents the deliberate
// answer for agent-reported findings: they have no re-derivable signal, so the
// page must actually have advanced.
func TestVerifyPostconditionFallsBackToSemanticProgress(t *testing.T) {
	issue := &types.WikiPageIssue{IssueType: "mixed_entities", DetectedPageVersion: 4}
	attempt := &types.WikiRepairAttempt{BeforeVersion: 4}

	stalled := wikiVerifyInput{Issue: issue, Attempt: attempt, Page: &types.WikiPage{Version: 4}}
	assert.Error(t, verifyWikiIssuePostcondition(context.Background(), stalled))

	edited := wikiVerifyInput{Issue: issue, Attempt: attempt, Page: &types.WikiPage{Version: 5}}
	assert.NoError(t, verifyWikiIssuePostcondition(context.Background(), edited))
}

// TestVerifyPostconditionAcceptsDeletedPageOnlyWithDeleteAction keeps a repair
// from closing an issue merely because the page vanished.
func TestVerifyPostconditionAcceptsDeletedPageOnlyWithDeleteAction(t *testing.T) {
	issue := &types.WikiPageIssue{IssueType: string(LintIssueOrphanPage)}

	assert.Error(t, verifyWikiIssuePostcondition(context.Background(), wikiVerifyInput{
		Issue: issue, Attempt: &types.WikiRepairAttempt{},
	}))
	assert.NoError(t, verifyWikiIssuePostcondition(context.Background(), wikiVerifyInput{
		Issue: issue, Attempt: &types.WikiRepairAttempt{Action: "deleted"},
	}))
}

// TestScanPageCrossRefsCapsPerPageSuggestions is the bound that keeps the
// advisory rule from dominating a scan. It fires once per (page, mentioned
// entity) pair, so a single page mentioning many entities used to be able to
// contribute an unbounded number of findings on its own.
func TestScanPageCrossRefsCapsPerPageSuggestions(t *testing.T) {
	titles := make(map[string]string)
	var mentions []string
	for i := 0; i < wikiCrossRefPerPageLimit*3; i++ {
		slug := fmt.Sprintf("entity/e%02d", i)
		title := fmt.Sprintf("Entity%02d", i)
		titles[slug] = title
		mentions = append(mentions, title)
	}
	page := &types.WikiPage{
		ID: "page-noisy", Slug: "summary/noisy", Version: 1,
		Content: strings.Join(mentions, " and "),
	}

	collect := func() []string {
		var targets []string
		require.NoError(t, scanPageCrossRefs(page, newWikiLintTitleMatcher(titles), func(f WikiLintIssue) error {
			targets = append(targets, f.TargetSlug)
			return nil
		}))
		return targets
	}

	// The cap must be a stable prefix in slug order, not an arbitrary sample. A
	// finding's identity is its fingerprint, so a selection that shifted with map
	// iteration would make every run close the previous run's suggestions and
	// open a fresh set of equivalent ones.
	assert.Equal(t,
		[]string{"entity/e00", "entity/e01", "entity/e02", "entity/e03", "entity/e04"},
		collect(),
	)
	for i := 0; i < 5; i++ {
		assert.Equal(t, collect(), collect(), "the selection must not depend on map iteration order")
	}
}

// TestScanPageCrossRefsSkipsSelfAndExistingLinks keeps the advisory rule from
// suggesting links that are already there.
func TestScanPageCrossRefsSkipsSelfAndExistingLinks(t *testing.T) {
	titles := map[string]string{
		"entity/self":   "Self",
		"entity/linked": "Linked",
		"entity/absent": "Absent",
	}
	page := &types.WikiPage{
		ID: "page-1", Slug: "entity/self", Version: 1,
		Content:  "Self mentions Linked and Absent.",
		OutLinks: types.StringArray{"entity/linked"},
	}

	var got []string
	require.NoError(t, scanPageCrossRefs(page, newWikiLintTitleMatcher(titles), func(f WikiLintIssue) error {
		got = append(got, f.TargetSlug)
		return nil
	}))
	assert.Equal(t, []string{"entity/absent"}, got)
}

// TestWikiLintHealthScoreUsesExactCounters proves the score is derived from the
// scan's counters rather than from the truncated slice the report carries, so a
// large knowledge base is not silently graded on a 500-finding sample.
func TestWikiLintHealthScoreUsesExactCounters(t *testing.T) {
	scan := &wikiLintScan{
		Total: 10_000,
		ByType: map[WikiLintIssueType]int{
			LintIssueBrokenLink:   4,
			LintIssueEmptyContent: 3,
		},
		Stats: &types.WikiStats{TotalPages: 100, TotalLinks: 40, OrphanCount: 60},
	}
	// 100 - 25 (>50% orphans) - 4*5 (broken) - 3*3 (empty)
	assert.Equal(t, 46, wikiLintHealthScore(scan))

	scan.ByType[LintIssueBrokenLink] = 1_000
	assert.Equal(t, 0, wikiLintHealthScore(scan), "the score floors at zero")

	assert.Equal(t, 100, wikiLintHealthScore(&wikiLintScan{
		ByType: map[WikiLintIssueType]int{}, Stats: &types.WikiStats{},
	}), "an empty KB cannot be penalised")
}

// TestWikiLintSummaryReportsFullTotals guards the user-visible count: the
// summary must describe every finding the walk saw, not just the ones the report
// materialized.
func TestWikiLintSummaryReportsFullTotals(t *testing.T) {
	assert.Equal(t, "Wiki is healthy! No issues found.", wikiLintSummary(&wikiLintScan{
		BySeverity: map[WikiLintIssueSeverity]int{},
	}))
	assert.Equal(t, "Found 900 issues: 100 errors, 300 warnings, 500 suggestions.",
		wikiLintSummary(&wikiLintScan{
			Total: 900,
			BySeverity: map[WikiLintIssueSeverity]int{
				SeverityError: 100, SeverityWarning: 300, SeverityInfo: 500,
			},
		}))
}
