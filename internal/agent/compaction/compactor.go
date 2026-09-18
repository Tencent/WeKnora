package compaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// maxSummarizationAttempts bounds the retries for one compaction. Each
	// attempt is a full LLM round-trip, and a compaction that keeps failing is
	// better served by the raw archive than by stalling the turn.
	maxSummarizationAttempts = 2

	// summarizationTimeout is the per-attempt budget for a summarization call.
	summarizationTimeout = 60 * time.Second

	// llmCallLabel identifies compaction traffic in tracing and usage
	// accounting, separating it from the agent's own reasoning rounds.
	llmCallLabel = "agent_context_compaction"
)

// Reason records what asked for a compaction, for logging and for the UI.
type Reason string

const (
	// ReasonThreshold is the routine case: context crossed the budget.
	ReasonThreshold Reason = "threshold"
	// ReasonOverflow is repair after the provider rejected or truncated a
	// request because the window was full.
	ReasonOverflow Reason = "overflow"
)

// ErrNothingToCompact means the context cannot be made smaller: everything
// outside the keep-recent budget is already gone. Callers must treat this as a
// stop signal rather than retrying, or they reproduce the every-round
// summarization loop this package exists to prevent.
var ErrNothingToCompact = errors.New("compaction: nothing outside the keep-recent budget")

// Result describes what one compaction did.
type Result struct {
	Messages       []chat.Message
	Summary        string
	Reason         Reason
	TokensBefore   int
	TokensAfter    int
	MessagesBefore int
	MessagesAfter  int
	// Degraded reports that at least one summary came from the raw archive
	// because the summarizer failed. The context still shrank; its memory is
	// just coarser.
	Degraded bool
	// SplitTurn reports that the cut divided a single turn, so a turn-prefix
	// summary was generated alongside the history summary.
	SplitTurn bool
	// Checkpoint is the part of this compaction a later turn can start from,
	// or nil when there is none (see Preparation.checkpoint).
	Checkpoint *Checkpoint
}

// Checkpoint is a summary that ends exactly on a stored turn. Persisted onto
// that turn, it lets the next turn load the summary in place of everything up
// to and including the turn, instead of summarizing the same history again.
type Checkpoint struct {
	// TurnID is the stored turn the summary covers through, inclusive.
	TurnID  string
	Summary string
}

// Freed is how much room the compaction actually recovered. A non-positive
// value means the summary cost as much as the history it replaced.
func (r *Result) Freed() int {
	return r.TokensBefore - r.TokensAfter
}

// Compactor summarizes conversation history into a checkpoint.
type Compactor struct {
	chatModel chat.Chat
	estimator *agenttoken.Estimator
	settings  Settings
}

// New builds a compactor. It returns nil when compaction cannot run, so the
// caller can treat a nil compactor as "feature off" without a second flag.
func New(chatModel chat.Chat, estimator *agenttoken.Estimator, settings Settings) *Compactor {
	if chatModel == nil || estimator == nil || settings.MaxContextTokens <= 0 {
		return nil
	}
	return &Compactor{chatModel: chatModel, estimator: estimator, settings: settings.Normalize()}
}

// Settings returns the normalized settings, including the derived threshold.
func (c *Compactor) Settings() Settings {
	if c == nil {
		return Settings{}
	}
	return c.settings
}

// Compact replaces history older than the keep-recent budget with a summary.
// It returns ErrNothingToCompact when no such history exists.
func (c *Compactor) Compact(
	ctx context.Context, messages []chat.Message, reason Reason,
) (*Result, error) {
	if c == nil {
		return nil, ErrNothingToCompact
	}
	prep := Prepare(messages, c.settings, c.estimator)
	if prep == nil {
		return nil, ErrNothingToCompact
	}

	history, historyDegraded := c.summarizeHistory(ctx, prep)
	summary, prefixDegraded := c.appendTurnPrefix(ctx, prep, history)
	summary += prep.fileOps.format()
	compacted := Apply(messages, prep, summary)

	return &Result{
		Messages:       compacted,
		Summary:        summary,
		Reason:         reason,
		TokensBefore:   prep.TokensBefore,
		TokensAfter:    c.estimator.EstimateMessages(compacted),
		MessagesBefore: len(messages),
		MessagesAfter:  len(compacted),
		Degraded:       historyDegraded || prefixDegraded,
		SplitTurn:      prep.IsSplitTurn,
		Checkpoint:     prep.checkpoint(history, historyDegraded),
	}, nil
}

// summarizeHistory folds the complete turns being dropped into the previous
// summary, falling back to a raw archive when the summarizer fails.
func (c *Compactor) summarizeHistory(ctx context.Context, p *Preparation) (string, bool) {
	if len(p.MessagesToSummarize) == 0 {
		return p.PreviousSummary, false
	}
	instructions := initialSummarizationInstructions
	if p.PreviousSummary != "" {
		instructions = updateSummarizationInstructions
	}
	text, err := c.summarize(
		ctx, p.MessagesToSummarize, p.PreviousSummary, instructions, c.settings.summaryBudget(),
	)
	if err != nil {
		// The previous summary is still the best record of everything
		// before this span, so the archive is appended to it rather than
		// replacing it.
		return joinNonEmpty(p.PreviousSummary, rawArchive(p.MessagesToSummarize)), true
	}
	return text, false
}

// appendTurnPrefix adds the summary of a split turn's discarded head, so the
// retained tail still has its originating request.
func (c *Compactor) appendTurnPrefix(ctx context.Context, p *Preparation, history string) (string, bool) {
	if !p.IsSplitTurn || len(p.TurnPrefixMessages) == 0 {
		return history, false
	}
	degraded := false
	prefix, err := c.summarize(
		ctx, p.TurnPrefixMessages, "", turnPrefixInstructions, c.settings.turnPrefixBudget(),
	)
	if err != nil {
		degraded = true
		prefix = rawArchive(p.TurnPrefixMessages)
	}
	if history == "" {
		history = "No prior history."
	}
	return history + splitTurnSeparator + prefix, degraded
}

// summarize runs one summarization call with retries.
func (c *Compactor) summarize(
	ctx context.Context,
	messages []chat.Message,
	previousSummary, instructions string,
	maxTokens int,
) (string, error) {
	prompt := buildSummarizationPrompt(messages, previousSummary, instructions)
	var lastErr error

	for attempt := 1; attempt <= maxSummarizationAttempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, summarizationTimeout)
		callCtx = types.WithLLMCallMetadata(callCtx, llmCallLabel, "")
		resp, err := c.chatModel.Chat(callCtx, []chat.Message{
			{Role: "system", Content: summarizationSystemPrompt},
			{Role: "user", Content: prompt},
		}, &chat.ChatOptions{
			Temperature:    0.3, // low temperature for factual summarization
			MaxTokens:      maxTokens,
			CacheRetention: chat.CacheRetentionNone,
		})
		cancel()

		if err != nil {
			lastErr = err
			continue
		}
		if err := validateSummary(resp); err != nil {
			lastErr = err
			continue
		}
		return strings.TrimSpace(resp.Content), nil
	}

	return "", fmt.Errorf("summarization failed after %d attempts: %w",
		maxSummarizationAttempts, lastErr)
}

// validateSummary rejects responses that cannot serve as a checkpoint.
//
// The length stop is the one worth spelling out: a summary cut off by the token
// cap reads like a valid summary but silently ends mid-section, and every later
// round inherits that truncation as its only memory of the dropped history.
// A partial summary is a failure: it cannot serve as a checkpoint.
func validateSummary(resp *types.ChatResponse) error {
	if resp == nil || strings.TrimSpace(resp.Content) == "" {
		return errors.New("empty response from LLM")
	}
	switch strings.ToLower(strings.TrimSpace(resp.FinishReason)) {
	case "length", "max_tokens", "max_output_tokens":
		return errors.New("generation hit the token cap and the summary is incomplete")
	}
	return nil
}

// buildSummarizationPrompt wraps the transcript in a tag and puts the
// instructions last, so the summarizer cannot mistake conversation text for
// its own instructions.
func buildSummarizationPrompt(messages []chat.Message, previousSummary, instructions string) string {
	var sb strings.Builder
	sb.WriteString("<conversation>\n")
	sb.WriteString(serializeConversation(messages))
	sb.WriteString("\n</conversation>\n\n")
	if previousSummary != "" {
		sb.WriteString("<previous-summary>\n")
		sb.WriteString(previousSummary)
		sb.WriteString("\n</previous-summary>\n\n")
	}
	sb.WriteString(instructions)
	return sb.String()
}

func joinNonEmpty(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}
