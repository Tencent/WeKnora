package agent

import (
	"context"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// snapshotContextUsage records the prompt mix of the request that is about to
// be sent (or was just sent). promptTokens of 0 means the provider has not
// priced it yet and the snapshot is marked as an estimate.
//
// An unpriced snapshot does not replace a mix the provider already priced.
// Live SSE stays opt-in: call publishContextUsage after the response lands,
// so the ring does not jump from estimate to measured mid-round.
func (e *AgentEngine) snapshotContextUsage(
	_ context.Context,
	state *types.AgentState,
	messages []chat.Message,
	tools []chat.Tool,
	promptTokens int,
) types.ContextUsage {
	if e == nil || state == nil {
		return types.ContextUsage{}
	}
	usage := e.contextAttributor().
		Attribute(messages, tools, e.promptSectionTokens, promptTokens)
	// A round that starts before the provider prices it must not wipe a mix
	// we already measured. The estimate would otherwise be what gets persisted
	// if the turn ends in between.
	if promptTokens > 0 || !measuredContext(state.ContextUsage) {
		state.ContextUsage = usage
	}
	return usage
}

func measuredContext(usage types.ContextUsage) bool {
	return !usage.Estimated && usage.Total > 0
}

// recalibrateContextUsage re-reports the round's request now that the provider
// has priced it, without walking the history a second time.
func (e *AgentEngine) recalibrateContextUsage(state *types.AgentState, promptTokens int) {
	if e == nil || state == nil || promptTokens <= 0 {
		return
	}
	state.ContextUsage = e.contextAttributor().Recalibrate(promptTokens)
}

func (e *AgentEngine) publishContextUsage(ctx context.Context, state *types.AgentState) {
	if e == nil || e.eventBus == nil || state == nil {
		return
	}
	if state.ContextUsage.Window <= 0 {
		return
	}
	_ = e.eventBus.Emit(ctx, event.Event{
		ID:        generateEventID("context-usage"),
		Type:      event.EventAgentContextUsage,
		SessionID: e.sessionID,
		Data:      state.ContextUsage,
	})
}
