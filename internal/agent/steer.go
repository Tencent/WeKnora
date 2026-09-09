package agent

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// sanitizeSteerContent normalizes newlines and trims, same treatment the chat
// pipeline applies to user queries.
func sanitizeSteerContent(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.TrimSpace(content)
}

// SetSteerSink wires the optional steer channel. Nil (the default) disables
// mid-run injection entirely — every existing call site keeps working. The
// sink satisfies types.SteerSink structurally.
func (e *AgentEngine) SetSteerSink(sink types.SteerSink) {
	e.steerSink = sink
}

// drainSteerMessages is called at the round boundary, after context
// compression and before the next LLM call, so injected text:
//   - lands in the protected tail (CompressContext never removes anything
//     after the last user message),
//   - is included in lastSentMsgCount's delta-token accounting,
//   - and is visible to the very next callLLMWithRetry.
//
// Persist happens before append. A persist failure leaves the event
// unconsumed so the next boundary can retry; appending first would put
// text in the model that history never recorded.
func (e *AgentEngine) drainSteerMessages(
	ctx context.Context,
	state *types.AgentState,
	messagesPtr *[]chat.Message,
	sessionID, messageID string,
) int {
	if e.steerSink == nil {
		return 0
	}
	events, _, err := e.steerSink.PollSteer(ctx, sessionID, messageID, 0)
	if err != nil {
		logger.Warnf(ctx, "[Agent] Steer poll failed at round %d: %v", state.CurrentRound+1, err)
		return 0
	}
	if len(events) == 0 {
		return 0
	}

	injected := 0
	for _, evt := range events {
		content := sanitizeSteerContent(getString(evt, "content"))
		if content == "" {
			continue
		}
		steerID := getString(evt, "id")
		userMessageID := e.steerSink.PersistSteerMessage(ctx, sessionID, messageID, steerID, content,
			types.MentionedItemsFromRaw(evt["mentioned_items"]), getString(evt, "channel"))
		if userMessageID == "" {
			logger.Warnf(ctx, "[Agent] Steer persist failed for %s, leaving event pending", steerID)
			continue
		}
		*messagesPtr = append(*messagesPtr, chat.Message{Role: "user", Content: content})
		_ = e.eventBus.Emit(ctx, event.Event{
			ID:        generateEventID("injected"),
			Type:      event.EventUserMessageInjected,
			SessionID: sessionID,
			Data: event.UserMessageInjectedData{
				SteerID:       steerID,
				Content:       content,
				MessageID:     messageID,
				UserMessageID: userMessageID,
			},
		})
		injected++
	}
	if injected > 0 {
		logger.Infof(ctx, "[Agent][Round-%d] Injected %d steered user message(s) into the turn",
			state.CurrentRound+1, injected)
	}
	return injected
}

func getString(evt map[string]interface{}, key string) string {
	return types.MapString(evt, key)
}
