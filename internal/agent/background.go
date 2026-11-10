package agent

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/background"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// BackgroundRun is the run-scoped source of background-work context. The
// engine asks it what to tell the model at the two boundaries steer uses, and
// which finished items tools showed the model during a step.
// *background.RunNotices implements it.
type BackgroundRun interface {
	RoundStart(ctx context.Context) (background.Delivery, error)
	BeforeFinal(ctx context.Context) (background.Delivery, error)
	TakeSeen() []string
}

// SetBackgroundRun wires background-work context for this run. Nil (the
// default) disables it. Must be called before Execute.
func (e *AgentEngine) SetBackgroundRun(run BackgroundRun) {
	e.backgroundRun = run
}

// backgroundNoticesContent wraps background-work context so the model can tell
// it apart from what the user typed.
func backgroundNoticesContent(text string) string {
	return "<background_notices>\n" + strings.TrimSpace(text) + "\n</background_notices>"
}

// injectBackground appends background-work context before the next LLM call
// and queues it for the next step, which is where history replays it. It
// returns how many messages it appended (0 or 1). Failures are logged and
// skipped: the inbox keeps unacknowledged notices for a later boundary.
func (e *AgentEngine) injectBackground(
	ctx context.Context, state *types.AgentState, messagesPtr *[]chat.Message, beforeFinal bool,
) int {
	if e.backgroundRun == nil {
		return 0
	}
	read := e.backgroundRun.RoundStart
	if beforeFinal {
		read = e.backgroundRun.BeforeFinal
	}
	delivery, err := read(ctx)
	if err != nil {
		logger.Warnf(ctx, "[Agent][Round-%d] Background notices unavailable: %v", state.CurrentRound+1, err)
		return 0
	}
	if strings.TrimSpace(delivery.Text) == "" {
		return 0
	}
	content := backgroundNoticesContent(delivery.Text)
	*messagesPtr = append(*messagesPtr, chat.Message{Role: "user", Content: content})
	state.PendingBackground = append(state.PendingBackground, types.BackgroundInjection{
		Content: content, AckIDs: delivery.AckIDs,
	})
	return 1
}

// takeBackgroundSeen returns the finished IDs tools showed the model since the
// previous call.
func (e *AgentEngine) takeBackgroundSeen() []string {
	if e.backgroundRun == nil {
		return nil
	}
	return e.backgroundRun.TakeSeen()
}

// HasPendingInput reports whether a steered user message is waiting for the
// run that owns messageID. Blocking tools poll it so the user is not kept
// waiting behind them. It reads the queue without consuming it.
func (e *AgentEngine) HasPendingInput(ctx context.Context, sessionID, messageID string) bool {
	if e == nil || e.steerSink == nil {
		return false
	}
	events, _, err := e.steerSink.PollSteer(ctx, sessionID, messageID, 0)
	if err != nil {
		return false
	}
	for _, evt := range events {
		if sanitizeSteerContent(getString(evt, "content")) != "" {
			return true
		}
	}
	return false
}
