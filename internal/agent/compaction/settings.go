// Package compaction keeps an agent turn inside the model's context window by
// replacing older conversation with a structured summary.
//
// The cut point is chosen by token budget alone — never by "keep the current
// turn intact". A ReAct loop is a single turn that can run twenty rounds and
// produce a hundred thousand tokens of tool traffic; a compactor that refuses
// to touch the current turn has nothing left to compact and burns an LLM call
// per round achieving nothing.
package compaction

// DefaultReserveTokens is the floor on room kept free for the next response.
const DefaultReserveTokens = 16384

// DefaultKeepRecentTokens is how much recent conversation survives a
// compaction untouched. It is what makes compaction terminate: whatever the
// context grew to, it comes out of a compaction at roughly summary + this,
// which is far below any sane threshold, so the next round cannot immediately
// re-trigger.
const DefaultKeepRecentTokens = 20000

// Settings configures when compaction runs and how much it keeps.
type Settings struct {
	// Enabled turns automatic compaction on. Disabled still allows an
	// explicit overflow-recovery compaction, which is a repair, not a policy.
	Enabled bool
	// MaxContextTokens is the model's context window. Zero disables
	// compaction entirely: without a window there is no budget to enforce.
	MaxContextTokens int
	// ReserveTokens is the part of the window history may not occupy because
	// the next response has to fit there.
	ReserveTokens int
	// KeepRecentTokens is the token budget of recent messages kept verbatim.
	KeepRecentTokens int
	// MaxSummaryTokens caps the summarization completion. Zero falls back to
	// the reserve-derived budget alone.
	MaxSummaryTokens int
}

// Normalize fills in defaults and reconciles budgets that cannot all be
// honored at once.
func (s Settings) Normalize() Settings {
	if s.ReserveTokens <= 0 {
		s.ReserveTokens = DefaultReserveTokens
	}
	if s.KeepRecentTokens <= 0 {
		s.KeepRecentTokens = DefaultKeepRecentTokens
	}
	// What matters is not how much a compaction frees but how much room it
	// leaves before the next one. Keeping half the usable window technically
	// compacts and still re-triggers within a round or two, because the
	// summary takes a further slice and a single tool result can be ten
	// thousand tokens. A quarter leaves the post-compaction context at roughly
	// half the threshold, which is several rounds of work.
	//
	// Large windows are unaffected: the default keep-recent budget is already
	// well under a quarter of them. Tool schemas are not subtracted here —
	// keepRecentTokens is a fixed conversation budget, not "whatever is left
	// after the tool list".
	if s.MaxContextTokens > 0 {
		usable := s.MaxContextTokens - s.ReserveTokens
		if quarter := usable / 4; quarter > 0 && s.KeepRecentTokens > quarter {
			s.KeepRecentTokens = quarter
		}
	}
	return s
}

// Threshold is the context size above which history must be compacted. It is
// an absolute reserve rather than a fraction of the window, because what has
// to fit is the response, whose size does not scale with the window.
func (s Settings) Threshold() int {
	if s.MaxContextTokens <= 0 {
		return 0
	}
	return max(s.MaxContextTokens-s.ReserveTokens, 1)
}

// ShouldCompact reports whether the current context has crossed the threshold.
func (s Settings) ShouldCompact(contextTokens int) bool {
	if !s.Enabled {
		return false
	}
	threshold := s.Threshold()
	return threshold > 0 && contextTokens > threshold
}

// summaryBudget is the completion cap for a summarization call: 0.8 of
// reserveTokens, clamped by the model's own output limit. The old hard-coded
// 2000 was the reason long sessions produced summaries that stopped
// mid-sentence.
//
// It is additionally capped at the keep-recent budget. The summary occupies the
// compacted context alongside the retained tail, and successive compactions
// update the summary in place under instructions to preserve what is already
// there — so without a ceiling tied to the same budget it grows every round and
// slowly reclaims the room compaction just freed.
func (s Settings) summaryBudget() int {
	budget := s.ReserveTokens * 4 / 5
	if s.MaxSummaryTokens > 0 && s.MaxSummaryTokens < budget {
		budget = s.MaxSummaryTokens
	}
	if s.KeepRecentTokens > 0 && s.KeepRecentTokens < budget {
		budget = s.KeepRecentTokens
	}
	return max(budget, 1024)
}

// turnPrefixBudget is the smaller cap for summarizing the discarded prefix of
// a split turn: it only has to explain the retained suffix, not the session.
func (s Settings) turnPrefixBudget() int {
	return max(s.summaryBudget()/2, 512)
}
