package compaction

import (
	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// CutPoint is where the conversation is divided into "summarize this" and
// "keep this verbatim".
type CutPoint struct {
	// FirstKeptIdx is the index of the first message kept verbatim.
	FirstKeptIdx int
	// TurnStartIdx is the user message that opened the turn the cut lands in,
	// or -1 when the cut falls on a turn boundary.
	TurnStartIdx int
	// IsSplitTurn reports that the cut divides a single turn, so the part
	// before it needs its own summary to explain the part after it.
	IsSplitTurn bool
}

// isCutPointMessage reports whether history may be cut immediately before this
// message. Everything except a tool result qualifies. Tool results are excluded
// because they must stay with the assistant message that requested them —
// cutting between the two leaves an orphaned tool result and providers reject
// the request outright.
//
// The corollary is what makes mid-turn compaction safe: a cut that lands on an
// assistant message keeps that message and every tool result following it, so
// the pairing is never split from either side.
func isCutPointMessage(msg *chat.Message) bool {
	return msg.Role != "tool"
}

// isTurnStartMessage reports whether this message opens a turn. Compaction
// summaries count: they stand in for everything before them, so the turn they
// begin is complete on its own.
func isTurnStartMessage(msg *chat.Message) bool {
	return msg.Role == "user"
}

// historyStart is the first index compaction may touch. The system prompt is
// never a candidate — it carries the agent's instructions, not its history.
func historyStart(messages []chat.Message) int {
	if len(messages) > 0 && messages[0].Role == "system" {
		return 1
	}
	return 0
}

// FindCutPoint walks backwards from the newest message accumulating estimated
// size, and keeps the largest suffix that still fits keepRecentTokens.
//
// The budget is a ceiling, not a floor. Stopping at the first message that
// reaches the budget and keeping that message too is the obvious reading and
// the wrong one: a single knowledge_search result is routinely ten thousand
// tokens on its own, so "keep the recent 16k" would hand back 25k and leave
// barely a round of headroom before the next compaction.
//
// Nothing here consults the current turn. That is deliberate: the budget is the
// only rule, and a turn large enough to exceed it on its own gets split rather
// than exempted.
func FindCutPoint(
	messages []chat.Message,
	start, keepRecentTokens int,
	estimator *agenttoken.Estimator,
) CutPoint {
	if start < 0 {
		start = 0
	}
	var cutPoints []int
	for i := start; i < len(messages); i++ {
		if isCutPointMessage(&messages[i]) {
			cutPoints = append(cutPoints, i)
		}
	}
	if len(cutPoints) == 0 {
		return CutPoint{FirstKeptIdx: start, TurnStartIdx: -1}
	}

	// Worst case, keep only the newest group. A single message larger than the
	// whole budget cannot be cut any further here — trimming that is the tool
	// result trimmer's job, not the cut point's.
	cutIdx := cutPoints[len(cutPoints)-1]
	accumulated := 0
	for i := len(messages) - 1; i >= start; i-- {
		accumulated += estimator.EstimateMessage(&messages[i])
		if accumulated > keepRecentTokens {
			break
		}
		// Still within budget, so this is a better (earlier) place to cut.
		// Only legal cut points are recorded, which keeps every tool result
		// attached to the assistant message that requested it.
		if isCutPointMessage(&messages[i]) {
			cutIdx = i
		}
	}

	if isTurnStartMessage(&messages[cutIdx]) {
		return CutPoint{FirstKeptIdx: cutIdx, TurnStartIdx: -1}
	}
	turnStart := findTurnStartIdx(messages, cutIdx, start)
	return CutPoint{
		FirstKeptIdx: cutIdx,
		TurnStartIdx: turnStart,
		IsSplitTurn:  turnStart >= 0,
	}
}

// findTurnStartIdx returns the index of the user message that opened the turn
// containing entryIdx, or -1 if the turn started before the searchable range.
func findTurnStartIdx(messages []chat.Message, entryIdx, start int) int {
	for i := entryIdx; i >= start; i-- {
		if isTurnStartMessage(&messages[i]) {
			return i
		}
	}
	return -1
}
