package types

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"time"
)

// LearningInitialMastery is the prior probability before any assessed practice.
const LearningInitialMastery = 0.20

// LearningBKT applies observation Bayes update followed by the learning
// transition. The parameters are fixed and versioned, not fitted proficiency.
func LearningBKT(prior float64, correct bool) float64 {
	if math.IsNaN(prior) || math.IsInf(prior, 0) {
		prior = LearningInitialMastery
	}
	prior = math.Max(0, math.Min(1, prior))
	known, unknown := 0.90, 0.25
	if !correct {
		known, unknown = 0.10, 0.75
	}
	posterior := prior * known / (prior*known + (1-prior)*unknown)
	return posterior + (1-posterior)*0.15
}

// LearningMasteryState projects mastery while accounting for stale sources and review deadlines.
func LearningMasteryState(m *LearningMastery, stamp string, now time.Time) LearningMasteryView {
	v := LearningMasteryView{State: "unseen", PMastery: LearningInitialMastery}
	if m == nil {
		return v
	}
	v.SourceStale = m.Attempts > 0 && (stamp == "" || m.SourceStamp != stamp)
	v.PMastery, v.Attempts, v.Correct = m.PMastery, m.Attempts, m.Correct
	v.ConsecutiveCorrect, v.LastAssessedAt, v.NextReviewAt = m.ConsecutiveCorrect, m.LastAssessedAt, m.NextReviewAt
	if v.SourceStale {
		// Counts and timestamps describe the historical source; probability
		// must not claim mastery of the changed content.
		v.State, v.PMastery, v.ConsecutiveCorrect = "review_due", LearningInitialMastery, 0
		return v
	}
	if m.Attempts == 0 {
		v.PMastery = LearningInitialMastery
		return v
	}
	v.State = "learning"
	if m.Attempts >= 3 && m.PMastery >= 0.85 {
		v.State = "mastered"
	}
	if m.NextReviewAt != nil && !m.NextReviewAt.After(now) {
		v.State = "review_due"
	}
	return v
}

// LearningAssess records an answer and returns the mastery probabilities before and after it.
func LearningAssess(m *LearningMastery, stamp string, correct bool, now time.Time) (float64, float64) {
	if m.SourceStamp != stamp || m.Attempts == 0 {
		m.PMastery, m.Attempts, m.Correct, m.ConsecutiveCorrect = LearningInitialMastery, 0, 0, 0
	}
	before := m.PMastery
	m.PMastery = LearningBKT(before, correct)
	m.Attempts++
	if correct {
		m.Correct++
		m.ConsecutiveCorrect++
	} else {
		m.ConsecutiveCorrect = 0
	}
	days := [...]int{1, 3, 7, 14, 30}
	index := max(0, min(len(days)-1, m.ConsecutiveCorrect-1))
	next := now.Add(time.Duration(days[index]) * 24 * time.Hour)
	m.LastAssessedAt, m.NextReviewAt, m.SourceStamp = &now, &next, stamp
	return before, m.PMastery
}

// LearningNormalize trims and collapses whitespace.
func LearningNormalize(s string) string { return strings.Join(strings.Fields(s), " ") }

// LearningFingerprint hashes only the normalized prompt. Reordering or
// replacing distractors must not turn the same question into fresh credit.
func LearningFingerprint(prompt string) string {
	h := sha256.Sum256([]byte(strings.ToLower(LearningNormalize(prompt))))
	return hex.EncodeToString(h[:])
}

// LearningNodePublic projects a topic's familiarity and assessed mastery for clients.
func LearningNodePublic(n *LearningNode, now time.Time) *LearningNodeView {
	p := n.Page
	return &LearningNodeView{
		PageID: p.ID, KnowledgeBaseID: p.KnowledgeBaseID, Slug: p.Slug, Title: p.Title,
		PageType: p.PageType, Summary: p.Summary,
		Familiar: n.Mastery != nil && n.Mastery.ViewedAt != nil,
		Mastery:  LearningMasteryState(n.Mastery, n.SourceStamp, now),
	}
}

// LearningEligiblePage reports whether a live published Wiki page supports practice.
func LearningEligiblePage(p *WikiPage) bool {
	if p == nil || p.DeletedAt.Valid || p.Status != WikiPageStatusPublished {
		return false
	}
	switch p.PageType {
	case "entity", "concept", "synthesis", "comparison":
		return true
	default:
		return false
	}
}
