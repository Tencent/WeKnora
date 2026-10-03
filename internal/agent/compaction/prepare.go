package compaction

import (
	"strings"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

// The summary is injected as a `user` message rather than a second `system`
// one. A system message is an instruction; this is conversation history, and
// providers that merge or specially weight system messages were the wrong
// place to put it.
const (
	summaryPrefix = "The conversation history before this point was compacted " +
		"into the following summary:\n\n<summary>\n"
	summarySuffix = "\n</summary>"
)

// Preparation is everything the summarization step needs, computed before any
// LLM call so the caller can decide not to make one.
type Preparation struct {
	// FirstKeptIdx is where the verbatim tail begins.
	FirstKeptIdx int
	// MessagesToSummarize are the complete turns being replaced by prose.
	MessagesToSummarize []chat.Message
	// TurnPrefixMessages is the discarded head of a split turn, summarized
	// separately so the retained tail still has its originating request.
	TurnPrefixMessages []chat.Message
	IsSplitTurn        bool
	// PreviousSummary is the prior compaction's text, updated in place rather
	// than summarized again.
	PreviousSummary string
	TokensBefore    int
	// OmittedHistory and OmittedTurnPrefix count the oldest messages of each
	// range left out because one summarization request could not carry them
	// (see fitSummarizerInput). Their file paths are still recorded.
	OmittedHistory    int
	OmittedTurnPrefix int
	fileOps           fileOps
	// historyFileOps is fileOps without the turn prefix, for the checkpoint.
	historyFileOps fileOps
	// storedTurnID is the stored turn MessagesToSummarize ends exactly on, or
	// "" when it ends inside a turn or reaches into the live one.
	storedTurnID string
}

// Prepare picks the cut point and collects the message ranges around it.
//
// It returns nil when compaction cannot help — no messages fall outside the
// keep-recent budget, or the last compaction already left nothing to remove.
// That nil is the guard against the failure mode where every round spends a
// summarization call on a context it cannot shrink.
func Prepare(messages []chat.Message, s Settings, estimator *agenttoken.Estimator) *Preparation {
	if estimator == nil || len(messages) == 0 {
		return nil
	}
	s = s.Normalize()

	// Summarizing starts after the previous compaction's boundary. The
	// summary message itself is excluded: feeding it back in is what turns
	// successive compactions into a summary of a summary of a summary.
	boundaryStart := historyStart(messages)
	previousSummary := ""
	for i := len(messages) - 1; i >= boundaryStart; i-- {
		if messages[i].Kind != chat.MessageKindCompactionSummary {
			continue
		}
		previousSummary = unwrapSummary(messages[i].Content)
		boundaryStart = i + 1
		break
	}
	if boundaryStart >= len(messages) {
		return nil
	}

	cut := FindCutPoint(messages, boundaryStart, s.KeepRecentTokens, estimator)

	historyEnd := cut.FirstKeptIdx
	if cut.IsSplitTurn {
		historyEnd = cut.TurnStartIdx
	}
	toSummarize := cloneRange(messages, boundaryStart, historyEnd)

	var turnPrefix []chat.Message
	if cut.IsSplitTurn {
		turnPrefix = cloneRange(messages, cut.TurnStartIdx, cut.FirstKeptIdx)
	}

	if len(toSummarize) == 0 && len(turnPrefix) == 0 {
		return nil
	}

	p := &Preparation{
		FirstKeptIdx:    cut.FirstKeptIdx,
		IsSplitTurn:     cut.IsSplitTurn,
		PreviousSummary: previousSummary,
		TokensBefore:    estimator.EstimateMessages(messages),
		fileOps:         extractFileOps(previousSummary, toSummarize, turnPrefix),
		historyFileOps:  extractFileOps(previousSummary, toSummarize),
		storedTurnID:    storedTurnEndingAt(messages, historyEnd),
	}
	p.MessagesToSummarize, p.OmittedHistory = fitSummarizerInput(
		toSummarize, s.summarizerInputBudget(estimator, previousSummary, s.summaryBudget()), estimator)
	p.TurnPrefixMessages, p.OmittedTurnPrefix = fitSummarizerInput(
		turnPrefix, s.summarizerInputBudget(estimator, "", s.turnPrefixBudget()), estimator)
	return p
}

// fitSummarizerInput keeps the newest messages whose transcript fits budget
// and reports how many older ones it left out. The newest message is kept
// regardless; transcripts truncate each message, so one alone never matters.
//
// History can reach the whole window by design (the loader leaves overflow to
// compaction), so the part being summarized can be larger than one request
// can carry. An overflowing request is rejected, the summary degrades to a raw
// archive, and a degraded summary is never persisted, so the next turn would
// load the same history and fail the same way. Leaving the oldest messages out
// breaks that loop; the prompt says so rather than hiding it.
func fitSummarizerInput(
	messages []chat.Message, budget int, estimator *agenttoken.Estimator,
) ([]chat.Message, int) {
	if budget <= 0 {
		return messages, 0
	}
	used := 0
	for i := len(messages) - 1; i >= 0; i-- {
		used += estimator.EstimateString(serializeConversation(messages[i : i+1]))
		if used > budget && i < len(messages)-1 {
			return messages[i+1:], i + 1
		}
	}
	return messages, 0
}

// checkpoint returns the history summary as a Checkpoint when it ends exactly
// on a stored turn. The turn-prefix summary is left out: it describes part of a
// turn the next turn replays verbatim. Nothing newly summarized means the
// previous checkpoint still stands.
//
// A raw archive is kept too. Not keeping it made a failing summarizer a loop:
// the next turn loaded the same history, compacted it, failed the same way,
// and so on every turn. The archive is bounded like a summary, and the next
// compaction folds it in as the previous summary, so it is refined rather
// than kept as is.
func (p *Preparation) checkpoint(history string, degraded bool) *Checkpoint {
	if p.storedTurnID == "" || len(p.MessagesToSummarize) == 0 {
		return nil
	}
	return &Checkpoint{
		TurnID:   p.storedTurnID,
		Summary:  history + p.historyFileOps.format(),
		Degraded: degraded,
	}
}

// storedTurnEndingAt returns the stored turn that ends immediately before end,
// or "" if the message there is live or the same turn continues past end.
//
// Stored history always precedes the live turn, so a tagged message just
// before end means everything since the previous summary is stored history
// too, and the previous summary itself covered nothing live: a cut inside the
// live turn leaves only live messages after it.
func storedTurnEndingAt(messages []chat.Message, end int) string {
	if end <= 0 || end > len(messages) {
		return ""
	}
	turnID := messages[end-1].TurnID
	if turnID == "" {
		return ""
	}
	if end < len(messages) && messages[end].TurnID == turnID {
		return ""
	}
	return turnID
}

// Apply rebuilds the message list as system prompt, summary, and the verbatim
// tail. Nothing between the system prompt and the cut point survives.
func Apply(messages []chat.Message, p *Preparation, summary string) []chat.Message {
	tailStart := min(max(p.FirstKeptIdx, 0), len(messages))
	out := make([]chat.Message, 0, 2+len(messages)-tailStart)
	if historyStart(messages) == 1 {
		out = append(out, messages[0])
	}
	out = append(out, SummaryMessage(summary))
	return append(out, messages[tailStart:]...)
}

// SummaryMessage wraps summary text in the envelope the model sees, tagged so
// the next compaction recognizes it as its own output.
func SummaryMessage(summary string) chat.Message {
	return chat.Message{
		Role:    "user",
		Kind:    chat.MessageKindCompactionSummary,
		Content: summaryPrefix + summary + summarySuffix,
	}
}

// unwrapSummary recovers the raw text from a summary message so it can be
// handed to the update prompt without its envelope.
func unwrapSummary(content string) string {
	start := strings.Index(content, "<summary>")
	if start < 0 {
		return strings.TrimSpace(content)
	}
	start += len("<summary>")
	end := strings.LastIndex(content, "</summary>")
	if end < start {
		return strings.TrimSpace(content[start:])
	}
	return strings.TrimSpace(content[start:end])
}

func cloneRange(messages []chat.Message, start, end int) []chat.Message {
	if start < 0 {
		start = 0
	}
	if end > len(messages) {
		end = len(messages)
	}
	if start >= end {
		return nil
	}
	out := make([]chat.Message, end-start)
	copy(out, messages[start:end])
	return out
}
