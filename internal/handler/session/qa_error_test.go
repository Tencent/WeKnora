package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type recordingQAStreamManager struct {
	interfaces.StreamManager
	events []interfaces.StreamEvent
}

func (m *recordingQAStreamManager) AppendEvent(
	_ context.Context, _ string, _ string, streamEvent interfaces.StreamEvent,
) error {
	m.events = append(m.events, streamEvent)
	return nil
}

func TestEmitQAErrorEvent(t *testing.T) {
	bus := event.NewEventBus()
	streamManager := &recordingQAStreamManager{}
	streamHandler := NewAgentStreamHandler(
		context.Background(), "session-1", "message-1", "request-1", time.Now(),
		&types.Message{ID: "message-1"}, streamManager, bus,
	)
	streamHandler.Subscribe()
	received := make(chan event.ErrorData, 1)
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.ErrorData)
		if !ok {
			t.Fatalf("event data type = %T, want event.ErrorData", evt.Data)
		}
		received <- data
		return nil
	})

	emitQAErrorEvent(context.Background(), bus, "session-1", "Agent QA", errors.New("service panicked"))

	select {
	case data := <-received:
		if data.Error != "service panicked" {
			t.Fatalf("error = %q, want %q", data.Error, "service panicked")
		}
		if data.Stage != "Agent QA" {
			t.Fatalf("stage = %q, want %q", data.Stage, "Agent QA")
		}
		if data.SessionID != "session-1" {
			t.Fatalf("session ID = %q, want %q", data.SessionID, "session-1")
		}
	default:
		t.Fatal("expected EventError to be emitted")
	}
	if len(streamManager.events) != 1 {
		t.Fatalf("stream event count = %d, want 1", len(streamManager.events))
	}
	streamEvent := streamManager.events[0]
	if streamEvent.Type != types.ResponseTypeError {
		t.Fatalf("stream event type = %q, want %q", streamEvent.Type, types.ResponseTypeError)
	}
	if !streamEvent.Done {
		t.Fatal("error stream event must terminate the SSE response")
	}
}
