package service

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// BackgroundEventEmitter publishes the background work events of shared
// design §7. Features call it; they never pick the stream themselves.
type BackgroundEventEmitter struct {
	streams interfaces.StreamManager
}

// NewBackgroundEventEmitter returns an emitter that publishes background work
// events on the stream selected by the shared live-run rules.
func NewBackgroundEventEmitter(streams interfaces.StreamManager) *BackgroundEventEmitter {
	return &BackgroundEventEmitter{streams: streams}
}

// ParentLive reports whether parentMessageID is the session's live run.
func (e *BackgroundEventEmitter) ParentLive(
	ctx context.Context, s interfaces.SessionRef, parentMessageID string,
) (bool, error) {
	if e == nil || e.streams == nil {
		return false, nil
	}
	live, _, err := e.streams.GetLiveRun(ctx, s.SessionID)
	if err != nil {
		return false, err
	}
	return live != "" && live == parentMessageID, nil
}

// EmitCommandOutput previews running output on the parent's stream. When the
// parent run is no longer live the event is dropped: the process can be
// replayed from the feature's own storage, and appending after complete would
// corrupt replay.
func (e *BackgroundEventEmitter) EmitCommandOutput(
	ctx context.Context, s interfaces.SessionRef, parentMessageID, workID string,
	data event.CommandOutputData,
) error {
	if e == nil || e.streams == nil {
		return nil
	}
	live, err := e.ParentLive(ctx, s, parentMessageID)
	if err != nil {
		return err
	}
	if !live {
		return nil
	}
	return e.streams.AppendEvent(ctx, s.SessionID, parentMessageID, interfaces.StreamEvent{
		Type: types.ResponseTypeWorkEvent, Timestamp: time.Now(),
		Data: map[string]interface{}{
			"work_id": workID, "kind": "shell",
			"inner": map[string]interface{}{
				"response_type": "command_output",
				"data": map[string]interface{}{
					"tool_call_id": data.ToolCallID, "command": data.Command,
					"started_at": data.StartedAt, "output": data.Output, "done": data.Done,
				},
			},
		},
	})
}

// EmitWorkStarted routes to the parent stream when live, else to the session's
// current live run, else nowhere.
func (e *BackgroundEventEmitter) EmitWorkStarted(
	ctx context.Context, s interfaces.SessionRef, parentMessageID string,
	a interfaces.BackgroundActivity, toolCallID string,
) error {
	target, err := e.route(ctx, s, parentMessageID)
	if err != nil || target == "" {
		return err
	}
	return e.streams.AppendEvent(ctx, s.SessionID, target, interfaces.StreamEvent{
		Type: types.ResponseTypeWorkStarted, Timestamp: time.Now(),
		Data: map[string]interface{}{
			"work_id": a.ID, "kind": a.Kind, "title": a.Title,
			"parent_tool_call_id": toolCallID, "started_at": a.StartedAt,
		},
	})
}

// EmitWorkFinished publishes work_finished. The parent stream is used when
// that run is live; otherwise the session's current live run is used.
func (e *BackgroundEventEmitter) EmitWorkFinished(
	ctx context.Context, s interfaces.SessionRef, parentMessageID string,
	r interfaces.WorkResult,
) error {
	target, err := e.route(ctx, s, parentMessageID)
	if err != nil || target == "" {
		return err
	}
	return e.streams.AppendEvent(ctx, s.SessionID, target, interfaces.StreamEvent{
		Type: types.ResponseTypeWorkFinished, Timestamp: time.Now(),
		Data: map[string]interface{}{
			"work_id": r.ID, "kind": "shell", "state": r.State, "payload": r.Payload,
		},
	})
}

func (e *BackgroundEventEmitter) route(
	ctx context.Context, s interfaces.SessionRef, parentMessageID string,
) (string, error) {
	if e == nil || e.streams == nil {
		return "", nil
	}
	live, _, err := e.streams.GetLiveRun(ctx, s.SessionID)
	if err != nil {
		return "", err
	}
	if live == "" {
		return "", nil
	}
	if live == parentMessageID {
		return parentMessageID, nil
	}
	return live, nil
}
