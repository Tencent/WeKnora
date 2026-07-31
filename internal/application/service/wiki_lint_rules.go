package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// WikiLintIssueType defines the type of lint issue
type WikiLintIssueType string

const (
	LintIssueOrphanPage      WikiLintIssueType = "orphan_page"
	LintIssueBrokenLink      WikiLintIssueType = "broken_link"
	LintIssueStaleRef        WikiLintIssueType = "stale_ref"
	LintIssueMissingCrossRef WikiLintIssueType = "missing_cross_ref"
	LintIssueEmptyContent    WikiLintIssueType = "empty_content"
)

// WikiLintIssueSeverity defines the severity of a lint issue
type WikiLintIssueSeverity string

const (
	SeverityInfo    WikiLintIssueSeverity = "info"
	SeverityWarning WikiLintIssueSeverity = "warning"
	SeverityError   WikiLintIssueSeverity = "error"
)

// wikiMinContentRunes is the single source of truth for "this page is
// effectively empty". The detector and the postcondition that closes an
// empty-content finding both read it, so the two can never disagree about
// what counts as fixed.
const wikiMinContentRunes = 50

// wikiPageBySlugReader is the narrow read capability a postcondition may use.
// Keeping it minimal stops rule code from reaching into unrelated persistence.
type wikiPageBySlugReader interface {
	GetBySlug(ctx context.Context, kbID string, slug string) (*types.WikiPage, error)
}

// wikiVerifyInput carries everything a postcondition is allowed to inspect.
type wikiVerifyInput struct {
	Issue *types.WikiPageIssue
	// Page is the current state of the page named by the issue. Never nil:
	// the deleted-page case is settled by verifyWikiIssuePostcondition before
	// any rule runs.
	Page    *types.WikiPage
	Attempt *types.WikiRepairAttempt
	// TargetSlug is the counterpart recorded in the finding's evidence (the
	// dangling link target, the stale knowledge id, the unlinked entity).
	TargetSlug string
	Pages      wikiPageBySlugReader
}

// wikiLintRule binds a finding's identity, the metadata a detector stamps onto
// it, and the postcondition that proves it resolved. Findings can only be
// constructed through a rule value (see finding), so a new rule cannot ship
// with a detector but no verifier — the compiler requires both fields.
type wikiLintRule struct {
	Type       WikiLintIssueType
	Severity   WikiLintIssueSeverity
	RepairMode string
	// AutoFixable marks findings the deterministic AutoFix sweep may touch.
	AutoFixable bool
	// Durable marks findings that belong in the persistent problem centre.
	// Advisory rules stay out of it: they are suggestions rather than defects,
	// and one row per (page, suggestion) pair would bury the real findings.
	Durable bool
	// RequiresTarget rejects a resolution claim whose evidence lost the
	// counterpart the rule needs, instead of silently passing verification.
	RequiresTarget bool
	Verify         func(ctx context.Context, in wikiVerifyInput) error
}

// finding stamps a detector hit with its rule's metadata. Callers supply only
// the facts they observed; severity, repair mode and durability always come
// from the rule.
func (r wikiLintRule) finding(page *types.WikiPage, targetSlug, description string) WikiLintIssue {
	return WikiLintIssue{
		Type:        r.Type,
		Severity:    r.Severity,
		RepairMode:  r.RepairMode,
		AutoFixable: r.AutoFixable,
		PageSlug:    page.Slug,
		PageID:      page.ID,
		PageVersion: page.Version,
		TargetSlug:  targetSlug,
		Description: description,
	}
}

var (
	wikiRuleOrphanPage = wikiLintRule{
		Type: LintIssueOrphanPage, Severity: SeverityWarning,
		RepairMode: types.WikiIssueRepairManual, Durable: true,
		Verify: func(_ context.Context, in wikiVerifyInput) error {
			if len(in.Page.InLinks) == 0 {
				return errors.New("page still has no inbound links")
			}
			return nil
		},
	}

	wikiRuleBrokenLink = wikiLintRule{
		Type: LintIssueBrokenLink, Severity: SeverityError,
		RepairMode: types.WikiIssueRepairDeterministic, AutoFixable: true,
		Durable: true, RequiresTarget: true,
		Verify: func(_ context.Context, in wikiVerifyInput) error {
			if containsWikiRef(in.Page.OutLinks, in.TargetSlug) {
				return fmt.Errorf("broken link %s is still present", in.TargetSlug)
			}
			return nil
		},
	}

	wikiRuleStaleRef = wikiLintRule{
		Type: LintIssueStaleRef, Severity: SeverityError,
		RepairMode: types.WikiIssueRepairAgent, Durable: true, RequiresTarget: true,
		Verify: func(_ context.Context, in wikiVerifyInput) error {
			if containsWikiRef(in.Page.SourceRefs, in.TargetSlug) {
				return fmt.Errorf("stale source reference %s is still present", in.TargetSlug)
			}
			return nil
		},
	}

	wikiRuleEmptyContent = wikiLintRule{
		Type: LintIssueEmptyContent, Severity: SeverityWarning,
		RepairMode: types.WikiIssueRepairAgent, Durable: true,
		Verify: func(_ context.Context, in wikiVerifyInput) error {
			if wikiContentRunes(in.Page.Content) < wikiMinContentRunes {
				return fmt.Errorf("page still has fewer than %d content characters", wikiMinContentRunes)
			}
			return nil
		},
	}

	// wikiRuleMissingCrossRef is advisory: it fires once per (page, mentioned
	// entity) pair, which is the product of two large sets. It is reported in
	// the synchronous health report but deliberately not persisted, so the
	// durable finding set stays proportional to real defects.
	wikiRuleMissingCrossRef = wikiLintRule{
		Type: LintIssueMissingCrossRef, Severity: SeverityInfo,
		RepairMode: types.WikiIssueRepairAgent, Durable: false, RequiresTarget: true,
		Verify: func(ctx context.Context, in wikiVerifyInput) error {
			if containsWikiRef(in.Page.OutLinks, in.TargetSlug) {
				return nil
			}
			targetPage, err := in.Pages.GetBySlug(ctx, in.Issue.KnowledgeBaseID, in.TargetSlug)
			if errors.Is(err, repository.ErrWikiPageNotFound) {
				// The referenced entity disappeared, so the finding is obsolete.
				return nil
			}
			if err != nil {
				return err
			}
			matcher := newWikiLintTitleMatcher(map[string]string{in.TargetSlug: targetPage.Title})
			if len(matcher.Find(in.Page.Content)) > 0 {
				return fmt.Errorf("page still mentions %q without linking to %s",
					targetPage.Title, in.TargetSlug)
			}
			return nil
		},
	}
)

// wikiLintRules indexes every rule by the issue type persisted on a finding.
// verifyWikiIssuePostcondition and the durability filter both resolve through
// it, so registering a rule here is the only step needed to make a new lint
// type fully participate in the repair lifecycle.
var wikiLintRules = map[WikiLintIssueType]wikiLintRule{
	wikiRuleOrphanPage.Type:      wikiRuleOrphanPage,
	wikiRuleBrokenLink.Type:      wikiRuleBrokenLink,
	wikiRuleStaleRef.Type:        wikiRuleStaleRef,
	wikiRuleEmptyContent.Type:    wikiRuleEmptyContent,
	wikiRuleMissingCrossRef.Type: wikiRuleMissingCrossRef,
}

// wikiLintRuleFor resolves the rule behind a persisted issue type. Agent-
// reported types (mixed_entities, out_of_date, other, …) have no lint rule and
// report ok=false.
func wikiLintRuleFor(issueType string) (wikiLintRule, bool) {
	rule, ok := wikiLintRules[WikiLintIssueType(issueType)]
	return rule, ok
}

// verifyWikiIssuePostcondition proves a finding is actually gone before its
// issue may close.
//
// Types without a registered rule are agent-reported semantic findings whose
// only machine-checkable signal is that the page really advanced during the
// attempt — see verifyWikiSemanticProgress. Routing them through one explicit
// branch (rather than a switch default) keeps "no typed postcondition" a
// deliberate answer instead of an oversight.
func verifyWikiIssuePostcondition(ctx context.Context, in wikiVerifyInput) error {
	if in.Issue == nil || in.Attempt == nil {
		return errors.New("issue and repair attempt are required")
	}
	if in.Page == nil {
		if in.Attempt.Action == "deleted" {
			return nil
		}
		return errors.New("target page no longer exists without a recorded delete action")
	}
	rule, ok := wikiLintRuleFor(in.Issue.IssueType)
	if !ok {
		return verifyWikiSemanticProgress(in)
	}
	if rule.RequiresTarget && in.TargetSlug == "" {
		return fmt.Errorf("%s issue is missing target evidence", rule.Type)
	}
	return rule.Verify(ctx, in)
}

// verifyWikiSemanticProgress is the fallback postcondition for findings whose
// truth lives in prose ("this page mixes two products"). We cannot re-derive
// them, so we require either a real edit during this attempt or evidence that
// the page already advanced after the issue was detected.
func verifyWikiSemanticProgress(in wikiVerifyInput) error {
	if in.Page.Version <= in.Attempt.BeforeVersion && in.Page.Version <= in.Issue.DetectedPageVersion {
		return errors.New("page version did not change and the issue cannot be verified as already resolved")
	}
	return nil
}

// wikiContentRunes counts the visible characters of a page body. Rune counting
// (not len) keeps the empty-content threshold meaningful for CJK content.
func wikiContentRunes(content string) int {
	return utf8.RuneCountInString(strings.TrimSpace(content))
}

// containsWikiRef reports whether refs holds target, tolerating the
// "id|display" form used by both out_links and source_refs.
func containsWikiRef(refs []string, target string) bool {
	for _, ref := range refs {
		if ref == target || strings.HasPrefix(ref, target+"|") {
			return true
		}
	}
	return false
}
