package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type userInputStreamManager struct {
	events []interfaces.StreamEvent
}

func (m *userInputStreamManager) AppendEvent(
	_ context.Context, _, _ string, streamEvent interfaces.StreamEvent,
) error {
	m.events = append(m.events, streamEvent)
	return nil
}

func (m *userInputStreamManager) GetEvents(
	_ context.Context, _, _ string, _ int,
) ([]interfaces.StreamEvent, int, error) {
	return append([]interfaces.StreamEvent(nil), m.events...), len(m.events), nil
}

func TestAgentStreamUserInputRequired(t *testing.T) {
	bus := event.NewEventBus()
	stream := &userInputStreamManager{}
	handler := NewAgentStreamHandler(
		context.Background(), "session-1", "message-1", "request-1", time.Now(),
		&types.Message{ID: "message-1"}, stream, bus,
	)
	handler.Subscribe()

	err := bus.Emit(context.Background(), event.Event{
		ID: "pending-1-required", Type: event.EventUserInputRequired, SessionID: "session-1",
		Data: event.UserInputRequiredData{
			PendingID: "pending-1", SessionID: "session-1", AssistantMessageID: "message-1",
			ToolCallID: "tool-1", Question: "公司如何通知你？", Mode: "single_choice",
			QuestionGroupID: "dismissal", QuestionIndex: 1, QuestionTotal: 3,
			FieldKey: "notice_method", SchemaVersion: 2, CompletedCount: 1, RemainingCount: 2,
			Options:    []event.UserInputOptionData{{ID: "written", Label: "书面通知"}},
			AllowOther: true, AllowSkip: true, TimeoutSeconds: 600,
		},
	})
	if err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if len(stream.events) != 1 {
		t.Fatalf("event count = %d", len(stream.events))
	}
	got := stream.events[0]
	if got.Type != types.ResponseTypeUserInputRequired || !got.Done {
		t.Fatalf("stream event = %+v", got)
	}
	if got.Data["pending_id"] != "pending-1" || got.Data["question_index"] != float64(1) || got.Data["question_total"] != float64(3) {
		t.Fatalf("stream metadata = %#v", got.Data)
	}
	if got.Data["field_key"] != "notice_method" || got.Data["remaining_count"] != float64(2) {
		t.Fatalf("stream progress metadata = %#v", got.Data)
	}
}

func TestAgentStreamUserInputResolved(t *testing.T) {
	bus := event.NewEventBus()
	stream := &userInputStreamManager{}
	handler := NewAgentStreamHandler(
		context.Background(), "session-1", "message-1", "request-1", time.Now(),
		&types.Message{ID: "message-1"}, stream, bus,
	)
	handler.Subscribe()

	err := bus.Emit(context.Background(), event.Event{
		ID: "pending-1-resolved", Type: event.EventUserInputResolved, SessionID: "session-1",
		Data: event.UserInputResolvedData{
			PendingID: "pending-1", Status: "answered", QuestionGroupID: "dismissal",
			QuestionIndex: 1, QuestionTotal: 3,
			SelectedOptions: []event.UserInputOptionData{{ID: "written", Label: "书面通知"}},
		},
	})
	if err != nil {
		t.Fatalf("Emit() error = %v", err)
	}
	if len(stream.events) != 1 {
		t.Fatalf("event count = %d", len(stream.events))
	}
	got := stream.events[0]
	if got.Type != types.ResponseTypeUserInputResolved || !got.Done {
		t.Fatalf("stream event = %+v", got)
	}
	if got.Data["pending_id"] != "pending-1" || got.Data["status"] != "answered" {
		t.Fatalf("stream metadata = %#v", got.Data)
	}
}
