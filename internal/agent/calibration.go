package agent

import (
	"context"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	// A single sample outside these bounds is a usage report that does not
	// mean what the delta assumes (a provider counting only uncached input,
	// a prefix that changed underneath), not a tokenizer.
	minPlausibleTokenRatio = 0.3
	maxPlausibleTokenRatio = 3.0
	// minCalibrationTokens is how much estimated conversation the samples must
	// cover before their ratio replaces the prior. A tool result of a few
	// dozen tokens is mostly per-message overhead and says little.
	minCalibrationTokens = 256
)

// tokenCalibration learns how many provider tokens one estimated token of
// conversation costs, from consecutive requests within a turn.
//
// Two requests in a row share their tool schemas, system prompt and earlier
// messages, so the growth of the provider's prompt count between them is the
// cost of exactly the messages appended in between: the previous reply, the
// tool results, any steered message. Dividing it by their estimate prices
// conversation alone. A ratio over the whole request would also measure how
// the provider renders tool schemas, which OpenAI compacts; applied to
// conversation that under-counts it, the direction that overflows the window.
type tokenCalibration struct {
	provider, estimated float64 // summed over this turn's samples

	// The previous request, when the next one can be compared with it.
	prevPrompt   int
	prevSent     int
	prevTools    int // unscaled tool schema estimate
	prevRewrites int
}

// calibrateEstimator compares this round's request with the previous one and,
// once the turn has enough samples, rescales the estimator and records the
// scale on the turn's usage so the next turn starts from it. messages is the
// transcript whose first e.lastSentMsgCount entries were just sent.
func (e *AgentEngine) calibrateEstimator(
	ctx context.Context, round int, messages []chat.Message, tools []chat.Tool,
	usage types.TokenUsage, state *types.AgentState,
) {
	if usage.PromptTokens <= 0 {
		return
	}
	c := &e.calibration
	sent := e.lastSentMsgCount
	toolTokens := e.rawEstimator.EstimateTools(tools)

	comparable := c.prevPrompt > 0 && usage.PromptTokens > c.prevPrompt &&
		c.prevRewrites == e.contextRewrites && c.prevTools == toolTokens &&
		c.prevSent > 0 && c.prevSent < sent && sent <= len(messages)
	if comparable {
		appended := messages[c.prevSent:sent]
		estimated := 0
		for i := range appended {
			estimated += e.rawEstimator.EstimateMessage(&appended[i])
		}
		counted := usage.PromptTokens - c.prevPrompt
		if estimated > 0 && !hasImageParts(appended) {
			if ratio := float64(counted) / float64(estimated); ratio >= minPlausibleTokenRatio &&
				ratio <= maxPlausibleTokenRatio {
				c.provider += float64(counted)
				c.estimated += float64(estimated)
			} else {
				logger.Debugf(ctx, "[Agent][Round-%d][ctx] calibration sample skipped: "+
					"provider=%d estimated=%d", round, counted, estimated)
			}
		}
	}
	c.prevPrompt, c.prevSent, c.prevTools, c.prevRewrites = usage.PromptTokens, sent, toolTokens, e.contextRewrites

	if c.estimated < minCalibrationTokens {
		return
	}
	e.tokenEstimator.SetScale(c.provider / c.estimated)
	state.TurnUsage.ContextTokenScale = e.tokenEstimator.Scale()
	logger.Debugf(ctx, "[Agent][Round-%d][ctx] token scale %.2f (provider=%.0f estimated=%.0f)",
		round, state.TurnUsage.ContextTokenScale, c.provider, c.estimated)
}

// hasImageParts reports whether any message carries an image. Images are
// estimated at a fixed cost while providers bill them by tiles, so a sample
// containing one measures image pricing, not text.
func hasImageParts(messages []chat.Message) bool {
	for i := range messages {
		if len(messages[i].Images) > 0 {
			return true
		}
		for _, part := range messages[i].MultiContent {
			if part.ImageURL != nil || part.Type == "image_url" {
				return true
			}
		}
	}
	return false
}
