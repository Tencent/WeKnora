package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// installTranscript records one installer conversation.
//
// It is the installer's counterpart to handler/session.AgentStreamHandler, not
// a copy of it: that type lives above the service layer (it needs the artifact
// collector), so importing it here would close an import cycle. The overlap is
// bounded on purpose — install mode registers only shell_exec, so the six
// events below are all an install can produce, and references, memories,
// reflection, tool approvals and MCP OAuth are unreachable.
//
// The event shapes deliberately mirror AgentStreamHandler's so the console can
// render an install with the same components it renders a chat turn with.
type installTranscript struct {
	// ctx is the run's context, kept for logging and for writes that outlive
	// the emitting goroutine. Every write here is best-effort.
	ctx      context.Context
	bus      *event.EventBus
	streams  interfaces.StreamManager
	messages interfaces.MessageRepository

	sessionID          string
	assistantMessageID string

	mu       sync.Mutex
	message  *types.Message
	answer   strings.Builder
	starts   map[string]time.Time
	finished bool
}

func newInstallTranscript(
	ctx context.Context,
	bus *event.EventBus,
	streams interfaces.StreamManager,
	messages interfaces.MessageRepository,
	sessionID, assistantMessageID string,
) *installTranscript {
	return &installTranscript{
		ctx:                ctx,
		bus:                bus,
		streams:            streams,
		messages:           messages,
		sessionID:          sessionID,
		assistantMessageID: assistantMessageID,
		starts:             map[string]time.Time{},
	}
}

// Create writes the two rows the conversation needs before the engine starts.
//
// The assistant row cannot wait until the run ends: /sessions/continue-stream
// validates the message before it opens the stream, so a console that attaches
// while the install is running would be refused.
func (tr *installTranscript) Create(ctx context.Context, prompt string) error {
	if tr == nil || tr.messages == nil {
		return nil
	}
	now := time.Now()
	if _, err := tr.messages.CreateMessage(ctx, &types.Message{
		ID:          uuid.NewString(),
		SessionID:   tr.sessionID,
		Role:        "user",
		Content:     prompt,
		IsCompleted: true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}); err != nil {
		return fmt.Errorf("create installer prompt message: %w", err)
	}
	assistant := &types.Message{
		ID:        tr.assistantMessageID,
		SessionID: tr.sessionID,
		Role:      "assistant",
		CreatedAt: now.Add(time.Millisecond),
		UpdatedAt: now.Add(time.Millisecond),
	}
	if _, err := tr.messages.CreateMessage(ctx, assistant); err != nil {
		return fmt.Errorf("create installer answer message: %w", err)
	}
	tr.mu.Lock()
	tr.message = assistant
	tr.mu.Unlock()
	return nil
}

// Subscribe wires the six events an install can produce.
func (tr *installTranscript) Subscribe() {
	if tr == nil || tr.bus == nil {
		return
	}
	tr.bus.On(event.EventAgentThought, tr.onThought)
	tr.bus.On(event.EventAgentToolCall, tr.onToolCall)
	tr.bus.On(event.EventAgentToolResult, tr.onToolResult)
	tr.bus.On(event.EventAgentFinalAnswer, tr.onAnswer)
	tr.bus.On(event.EventError, tr.onError)
	tr.bus.On(event.EventAgentComplete, tr.onComplete)
}

// Finish closes the record. runErr is the engine's verdict: the engine emits
// no complete event when it fails, and a failed install is the one people
// actually come to read, so the failure is written here rather than hoped for.
func (tr *installTranscript) Finish(ctx context.Context, runErr error) {
	if tr == nil {
		return
	}
	if runErr != nil {
		tr.append(interfaces.StreamEvent{
			ID:        uuid.NewString(),
			Type:      types.ResponseTypeError,
			Content:   runErr.Error(),
			Done:      true,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"stage": "install",
				"error": runErr.Error(),
			},
		})
		tr.mu.Lock()
		if tr.answer.Len() > 0 {
			tr.answer.WriteString("\n\n")
		}
		tr.answer.WriteString(runErr.Error())
		tr.mu.Unlock()
	}

	tr.mu.Lock()
	alreadyComplete := tr.finished
	tr.finished = true
	tr.mu.Unlock()

	if !alreadyComplete {
		tr.append(interfaces.StreamEvent{
			ID:        uuid.NewString(),
			Type:      types.ResponseTypeComplete,
			Done:      true,
			Timestamp: time.Now(),
			Data:      map[string]interface{}{},
		})
	}
	tr.save(ctx)
}

func (tr *installTranscript) onThought(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentThoughtData)
	if !ok {
		return nil
	}
	tr.append(interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeThinking,
		Content:   data.Content,
		Done:      data.Done,
		Timestamp: time.Now(),
		Data:      tr.spanMeta(evt.ID, data.Done),
	})
	return nil
}

func (tr *installTranscript) onToolCall(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentToolCallData)
	if !ok {
		return nil
	}
	tr.mu.Lock()
	tr.starts[data.ToolCallID] = time.Now()
	tr.mu.Unlock()

	tr.append(interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeToolCall,
		Content:   fmt.Sprintf("Calling tool: %s", data.ToolName),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"tool_name":    data.ToolName,
			"arguments":    data.Arguments,
			"tool_call_id": data.ToolCallID,
		},
	})
	return nil
}

func (tr *installTranscript) onToolResult(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentToolResultData)
	if !ok {
		return nil
	}
	tr.mu.Lock()
	durationMs := data.Duration
	if start, ok := tr.starts[data.ToolCallID]; ok {
		durationMs = time.Since(start).Milliseconds()
		delete(tr.starts, data.ToolCallID)
	}
	tr.mu.Unlock()

	// A failed command is surfaced as an error, matching the chat path, so the
	// console highlights it instead of filing it as one more quiet step.
	responseType := types.ResponseTypeToolResult
	content := agenttools.StreamContentForToolResult(data.ToolName, data.Success, data.Error, data.Data)
	if !data.Success {
		responseType = types.ResponseTypeError
		if content == "" && data.Error != "" {
			content = data.Error
		}
	}

	meta := map[string]interface{}{
		"tool_name":    data.ToolName,
		"success":      data.Success,
		"error":        data.Error,
		"duration_ms":  durationMs,
		"tool_call_id": data.ToolCallID,
	}
	for k, v := range agenttools.SanitizeToolResultForClient(data.ToolName, &types.ToolResult{
		Success: data.Success,
		Output:  data.Output,
		Error:   data.Error,
		Data:    data.Data,
	}) {
		meta[k] = v
	}

	tr.append(interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      responseType,
		Content:   content,
		Timestamp: time.Now(),
		Data:      meta,
	})
	return nil
}

func (tr *installTranscript) onAnswer(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentFinalAnswerData)
	if !ok {
		return nil
	}
	tr.mu.Lock()
	tr.answer.WriteString(data.Content)
	tr.mu.Unlock()

	tr.append(interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeAnswer,
		Content:   data.Content,
		Done:      data.Done,
		Timestamp: time.Now(),
		Data:      tr.spanMeta(evt.ID, data.Done),
	})
	return nil
}

func (tr *installTranscript) onError(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.ErrorData)
	if !ok {
		return nil
	}
	tr.append(interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeError,
		Content:   data.Error,
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"stage": data.Stage,
			"error": data.Error,
		},
	})
	return nil
}

func (tr *installTranscript) onComplete(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentCompleteData)
	if !ok {
		return nil
	}
	tr.mu.Lock()
	tr.finished = true
	if data.MessageID == tr.assistantMessageID {
		msg := tr.ensureMessageLocked()
		msg.IsCompleted = true
		msg.AgentDurationMs = data.TotalDurationMs
		if steps, ok := data.AgentSteps.([]types.AgentStep); ok {
			msg.AgentSteps = agenttools.SanitizeAgentStepsForStorage(steps)
		}
	}
	// The engine may finish without ever streaming an answer chunk (it stops
	// naturally with plain text). Take the summary from the completion payload
	// so the transcript is not left with an empty final message.
	if tr.answer.Len() == 0 && data.FinalAnswer != "" {
		tr.answer.WriteString(data.FinalAnswer)
	}
	tr.mu.Unlock()

	tr.append(interfaces.StreamEvent{
		ID:        evt.ID,
		Type:      types.ResponseTypeComplete,
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"total_steps":       data.TotalSteps,
			"total_duration_ms": data.TotalDurationMs,
		},
	})
	return nil
}

// spanMeta mirrors the chat path's per-chunk metadata so the console can group
// chunks by event ID and show a duration once the span closes.
func (tr *installTranscript) spanMeta(eventID string, done bool) map[string]interface{} {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if _, ok := tr.starts[eventID]; !ok {
		tr.starts[eventID] = time.Now()
	}
	if !done {
		return map[string]interface{}{"event_id": eventID}
	}
	start := tr.starts[eventID]
	delete(tr.starts, eventID)
	return map[string]interface{}{
		"event_id":     eventID,
		"duration_ms":  time.Since(start).Milliseconds(),
		"completed_at": time.Now().Unix(),
	}
}

func (tr *installTranscript) append(evt interfaces.StreamEvent) {
	if tr.streams == nil {
		return
	}
	if err := tr.streams.AppendEvent(tr.ctx, tr.sessionID, tr.assistantMessageID, evt); err != nil {
		logger.Warnf(tr.ctx, "[skill] append %s to install transcript %s failed: %v",
			evt.Type, tr.sessionID, err)
	}
}

// ensureMessageLocked returns the assistant row being accumulated, creating the
// in-memory shell if Create never ran (a transcript whose seeding failed still
// records what it can). Callers must hold tr.mu.
func (tr *installTranscript) ensureMessageLocked() *types.Message {
	if tr.message == nil {
		tr.message = &types.Message{
			ID:        tr.assistantMessageID,
			SessionID: tr.sessionID,
			Role:      "assistant",
			CreatedAt: time.Now(),
		}
	}
	return tr.message
}

func (tr *installTranscript) save(ctx context.Context) {
	if tr.messages == nil {
		return
	}
	tr.mu.Lock()
	msg := tr.ensureMessageLocked()
	msg.Content = tr.answer.String()
	msg.IsCompleted = true
	msg.UpdatedAt = time.Now()
	tr.mu.Unlock()

	if err := tr.messages.UpdateMessage(ctx, msg); err != nil {
		logger.Warnf(ctx, "[skill] persist install transcript %s failed: %v", tr.sessionID, err)
	}
}
