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

	mu      sync.Mutex
	message *types.Message
	answers []*installAnswerSegment
	starts  map[string]time.Time
	// closed guards the terminal event and the final write, both of which say
	// "this install is over" and must be said once.
	closed bool
	// Totals accumulated across the run's engine turns, reported on the one
	// terminal event Finish emits.
	totalSteps      int
	totalDurationMs int64
}

// installAnswerSegment accumulates the prose streamed under one final-answer
// event ID.
//
// The engine has no separate channel for a round's commentary: "the venv is
// missing, so I'll create it" arrives as a final-answer chunk exactly like the
// closing summary does. An install runs dozens of rounds, so keeping every
// chunk would persist dozens of preambles glued end to end. A round that goes
// on to call a tool was, by definition, not the last one, so its prose is
// retracted when that call arrives and only the round that ends the run
// survives as the answer.
type installAnswerSegment struct {
	id         string
	content    string
	superseded bool
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

	// The prompt goes into the event log too, ahead of everything the agent
	// does, so one replay of the log is the whole conversation. Without it a
	// console following a running install would show the agent's side of a
	// conversation whose opening line it cannot see.
	tr.append(interfaces.StreamEvent{
		ID:        uuid.NewString(),
		Type:      types.ResponseTypeInstallPrompt,
		Content:   prompt,
		Done:      true,
		Timestamp: now,
		Data:      map[string]interface{}{},
	})
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

// Finish closes the record. runErr is the install's verdict, not just the
// engine's: verification runs after the agent stops, and a failure there is the
// one people actually come to read, so it is written here rather than hoped for.
func (tr *installTranscript) Finish(ctx context.Context, runErr error) {
	if tr == nil {
		return
	}
	tr.mu.Lock()
	alreadyClosed := tr.closed
	tr.closed = true
	tr.mu.Unlock()
	if alreadyClosed {
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
		// The verdict is never a preamble, so it gets its own segment that no
		// later call can retract.
		failure := tr.segmentLocked("install-failure")
		if prose := tr.composeAnswerLocked(); prose != "" {
			failure.content = "\n\n"
		}
		failure.content += runErr.Error()
		tr.mu.Unlock()
	}

	tr.mu.Lock()
	steps, duration := tr.totalSteps, tr.totalDurationMs
	tr.mu.Unlock()
	tr.append(interfaces.StreamEvent{
		ID:        uuid.NewString(),
		Type:      types.ResponseTypeComplete,
		Done:      true,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"total_steps":       steps,
			"total_duration_ms": duration,
		},
	})
	tr.save(ctx)
}

// RecordPrompt logs a follow-up instruction the installer was given mid-run.
//
// The transcript exists so one replay of the event log is the whole
// conversation. A repair round whose instruction is missing reads as the agent
// spontaneously deciding to install more packages, which is precisely the
// moment someone is reading this to find out why.
func (tr *installTranscript) RecordPrompt(prompt string) {
	if tr == nil {
		return
	}
	tr.append(interfaces.StreamEvent{
		ID:        uuid.NewString(),
		Type:      types.ResponseTypeInstallPrompt,
		Content:   prompt,
		Done:      true,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{},
	})
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
	// This round called a tool, so it is not the round that ends the run: any
	// prose it streamed was a preamble and must not reach Message.Content.
	for _, seg := range tr.answers {
		if !seg.superseded && seg.content != "" {
			seg.superseded = true
		}
	}
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
	if data.Content != "" {
		tr.segmentLocked(evt.ID).content += data.Content
	}
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

// onComplete records what the round finished with. It deliberately emits no
// terminal event: an install may run another installer round after
// verification, and a console that saw "complete" would stop following before
// that round began. Only Finish closes the stream.
func (tr *installTranscript) onComplete(_ context.Context, evt event.Event) error {
	data, ok := evt.Data.(event.AgentCompleteData)
	if !ok {
		return nil
	}
	tr.mu.Lock()
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
	if tr.composeAnswerLocked() == "" && data.FinalAnswer != "" {
		tr.segmentLocked(evt.ID).content = data.FinalAnswer
	}
	// Summed rather than overwritten: an install that needed a repair round ran
	// two engine turns, and its cost is both of them.
	tr.totalSteps += data.TotalSteps
	tr.totalDurationMs += data.TotalDurationMs
	tr.mu.Unlock()
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

// segmentLocked returns the segment accumulating an answer event ID, creating
// it on first sight. Callers must hold tr.mu.
func (tr *installTranscript) segmentLocked(id string) *installAnswerSegment {
	for _, seg := range tr.answers {
		if seg.id == id {
			return seg
		}
	}
	seg := &installAnswerSegment{id: id}
	tr.answers = append(tr.answers, seg)
	return seg
}

// composeAnswerLocked rebuilds the answer from the segments no tool call
// retracted, in arrival order. Callers must hold tr.mu.
func (tr *installTranscript) composeAnswerLocked() string {
	var b strings.Builder
	for _, seg := range tr.answers {
		if !seg.superseded {
			b.WriteString(seg.content)
		}
	}
	return b.String()
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
	msg.Content = tr.composeAnswerLocked()
	msg.IsCompleted = true
	msg.UpdatedAt = time.Now()
	tr.mu.Unlock()

	if err := tr.messages.UpdateMessage(ctx, msg); err != nil {
		logger.Warnf(ctx, "[skill] persist install transcript %s failed: %v", tr.sessionID, err)
	}
}
