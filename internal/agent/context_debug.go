package agent

// Diagnostics for context accounting.
//
// The failure this exists to catch is silent by nature: the estimate and the
// provider's bill disagree, and nothing in the normal logs compares them. That
// is how reasoning_content went uncounted long enough to make a 130k context
// measure as 26k — compaction fired constantly, cut in the wrong place, and
// freed nothing, with every individual log line looking reasonable.
//
// So the load-bearing line here is the drift check: what we predicted the
// request would cost against what the provider actually charged for it.

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// contextBreakdown attributes context tokens to the kind of content holding
// them, so an unexpected total can be traced to a source rather than guessed at.
type contextBreakdown struct {
	Messages     int
	Total        int
	Text         int
	Reasoning    int
	ToolResults  int
	ToolCallArgs int
	Images       int
	Summaries    int
	ToolSchemas  int
	// LargestKind and LargestTokens identify the single biggest message,
	// which is usually the thing worth acting on.
	LargestKind   string
	LargestTokens int
}

func (e *AgentEngine) breakdownContext(messages []chat.Message, tools []chat.Tool) contextBreakdown {
	b := contextBreakdown{Messages: len(messages), ToolSchemas: e.tokenEstimator.EstimateTools(tools)}
	est := e.tokenEstimator

	for i := range messages {
		msg := &messages[i]
		msgTokens := est.EstimateMessage(msg)
		b.Total += msgTokens

		if msgTokens > b.LargestTokens {
			b.LargestTokens = msgTokens
			b.LargestKind = msg.Role
			if msg.Name != "" {
				b.LargestKind += ":" + msg.Name
			}
		}

		if reasoning := est.EstimateString(msg.ReasoningContent); reasoning > 0 {
			b.Reasoning += reasoning
		}
		for _, tc := range msg.ToolCalls {
			b.ToolCallArgs += est.EstimateString(tc.Function.Arguments)
		}
		for _, part := range msg.MultiContent {
			if part.ImageURL != nil || part.Type == "image_url" {
				b.Images += estimatedImageTokensForLog
			}
		}
		if len(msg.MultiContent) == 0 {
			b.Images += len(msg.Images) * estimatedImageTokensForLog
		}

		content := est.EstimateString(msg.Content)
		switch {
		case msg.Kind == chat.MessageKindCompactionSummary:
			b.Summaries += content
		case msg.Role == "tool":
			b.ToolResults += content
		default:
			b.Text += content
		}
	}
	b.Total += b.ToolSchemas
	return b
}

// estimatedImageTokensForLog mirrors the estimator's per-image constant. It is
// duplicated rather than exported because it is only ever a reporting detail.
const estimatedImageTokensForLog = 1200

func (b contextBreakdown) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "msgs=%d total=%d", b.Messages, b.Total)
	fmt.Fprintf(&sb, " text=%d reasoning=%d tool_results=%d tool_args=%d",
		b.Text, b.Reasoning, b.ToolResults, b.ToolCallArgs)
	if b.Images > 0 {
		fmt.Fprintf(&sb, " images=%d", b.Images)
	}
	if b.Summaries > 0 {
		fmt.Fprintf(&sb, " summary=%d", b.Summaries)
	}
	fmt.Fprintf(&sb, " tool_schemas=%d", b.ToolSchemas)
	if b.LargestTokens > 0 {
		fmt.Fprintf(&sb, " largest=%s(%d)", b.LargestKind, b.LargestTokens)
	}
	return sb.String()
}

// logContextPrediction records what this round expects the request to cost,
// broken down by source, alongside the threshold it is being judged against.
func (e *AgentEngine) logContextPrediction(
	ctx context.Context, round int, messages []chat.Message, tools []chat.Tool, predicted int,
) {
	settings := e.compactor.Settings()
	b := e.breakdownContext(messages, tools)

	messagesEst := b.Total - b.ToolSchemas
	logger.Debugf(ctx, "[Agent][Round-%d][ctx] predicted=%d (baseline_usage=%d + delta) "+
		"| messages=%d tool_schemas=%d request=%d | threshold=%d window=%d keep_recent=%d",
		round, predicted, contextTokensFromUsage(e.lastUsage), messagesEst, b.ToolSchemas, b.Total,
		settings.Threshold(), settings.MaxContextTokens, settings.KeepRecentTokens)
	logger.Debugf(ctx, "[Agent][Round-%d][ctx] breakdown: %s", round, b)

	// Compaction's predicted size is the messages-only (or usage+delta) figure.
	// The independent check has to use the same accounting: with a usage
	// baseline the provider already billed tools, so compare against the full
	// request; without one, compare against messages alone. Mixing the two is
	// how a 12k chat with 105k of tool schemas looked like an 8× miss.
	compare := messagesEst
	if contextTokensFromUsage(e.lastUsage) > 0 {
		compare = b.Total
	}
	if predicted > 0 && compare > 0 {
		if ratio := float64(predicted) / float64(compare); ratio > 1.5 || ratio < 0.67 {
			logger.Warnf(ctx, "[Agent][Round-%d][ctx] usage baseline and direct estimate "+
				"disagree by %.1fx (predicted=%d, estimated=%d) — one of them is missing "+
				"content the other counts", round, ratio, predicted, compare)
		}
	}
}

// logContextDrift compares the prediction against what the provider billed.
// This is the ground truth for whether the estimator is complete: everything
// else in the pipeline is downstream of getting this number right.
func (e *AgentEngine) logContextDrift(ctx context.Context, round, predicted int, usage types.TokenUsage) {
	actual := usage.PromptTokens
	if actual <= 0 || predicted <= 0 {
		return
	}
	drift := predicted - actual
	pct := float64(drift) / float64(actual) * 100

	logger.Debugf(ctx, "[Agent][Round-%d][ctx] drift: predicted=%d actual_prompt=%d "+
		"diff=%+d (%+.1f%%) completion=%d",
		round, predicted, actual, drift, pct, usage.CompletionTokens)

	// Under-estimating is the dangerous direction: it is how a request gets
	// sent that the provider then rejects for size.
	if pct < -25 {
		logger.Warnf(ctx, "[Agent][Round-%d][ctx] estimate is %.0f%% below the provider's "+
			"count (predicted=%d actual=%d) — compaction will fire late and cut too little",
			round, -pct, predicted, actual)
	} else if pct > 50 {
		logger.Warnf(ctx, "[Agent][Round-%d][ctx] estimate is %.0f%% above the provider's "+
			"count (predicted=%d actual=%d) — compaction will fire on a context that fits",
			round, pct, predicted, actual)
	}
}
