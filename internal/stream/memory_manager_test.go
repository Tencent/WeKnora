package stream

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func mustAppendEvents(t *testing.T, m *MemoryStreamManager, sessionID, messageID string, ids ...string) {
	t.Helper()
	ctx := context.Background()
	for _, id := range ids {
		if err := m.AppendEvent(ctx, sessionID, messageID, interfaces.StreamEvent{ID: id, Content: id}); err != nil {
			t.Fatalf("AppendEvent(%s) error: %v", id, err)
		}
	}
}

func TestGetEvents_NegativeOffsetClampedToZero(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStreamManager()
	mustAppendEvents(t, m, "s1", "m1", "e1", "e2", "e3")

	for _, offset := range []int{-1, -100} {
		events, next, err := m.GetEvents(ctx, "s1", "m1", offset)
		if err != nil {
			t.Fatalf("GetEvents(%d) error: %v", offset, err)
		}
		if next != 3 {
			t.Errorf("GetEvents(%d) nextOffset = %d, want 3", offset, next)
		}
		if len(events) != 3 {
			t.Fatalf("GetEvents(%d) returned %d events, want 3", offset, len(events))
		}
		for i, want := range []string{"e1", "e2", "e3"} {
			if events[i].ID != want {
				t.Errorf("GetEvents(%d) events[%d].ID = %q, want %q", offset, i, events[i].ID, want)
			}
		}
	}
}

func TestGetSteerEvents_NegativeOffsetClampedToZero(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStreamManager()
	if err := m.AppendSteerEvents(ctx, "s1", "m1", []interfaces.StreamEvent{{ID: "s1"}, {ID: "s2"}}); err != nil {
		t.Fatalf("AppendSteerEvents error: %v", err)
	}

	events, next, err := m.GetSteerEvents(ctx, "s1", "m1", -5)
	if err != nil {
		t.Fatalf("GetSteerEvents(-5) error: %v", err)
	}
	if next != 2 {
		t.Errorf("GetSteerEvents(-5) nextOffset = %d, want 2", next)
	}
	if len(events) != 2 || events[0].ID != "s1" || events[1].ID != "s2" {
		t.Errorf("GetSteerEvents(-5) returned %d events, want [s1 s2]", len(events))
	}

	// Missing stream with a negative offset must also not panic and must
	// report the clamped offset.
	events, next, err = m.GetSteerEvents(ctx, "nope", "nope", -5)
	if err != nil {
		t.Fatalf("GetSteerEvents(-5) on missing stream error: %v", err)
	}
	if next != 0 || len(events) != 0 {
		t.Errorf("GetSteerEvents(-5) on missing stream = %d events, next %d; want 0 events, 0", len(events), next)
	}
}

func TestGetEvents_NegativeOffsetMissingStream(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStreamManager()

	events, next, err := m.GetEvents(ctx, "nope", "nope", -3)
	if err != nil {
		t.Fatalf("GetEvents(-3) error: %v", err)
	}
	if next != 0 {
		t.Errorf("GetEvents(-3) on missing stream nextOffset = %d, want 0", next)
	}
	if len(events) != 0 {
		t.Errorf("GetEvents(-3) on missing stream returned %d events, want 0", len(events))
	}
}

func TestGetEvents_PositiveOffsetsUnchanged(t *testing.T) {
	ctx := context.Background()
	m := NewMemoryStreamManager()
	mustAppendEvents(t, m, "s1", "m1", "e1", "e2", "e3")

	events, next, err := m.GetEvents(ctx, "s1", "m1", 1)
	if err != nil {
		t.Fatalf("GetEvents(1) error: %v", err)
	}
	if next != 3 || len(events) != 2 || events[0].ID != "e2" || events[1].ID != "e3" {
		t.Errorf("GetEvents(1) = %v, next %d; want [e2 e3], 3", eventIDs(events), next)
	}

	events, next, err = m.GetEvents(ctx, "s1", "m1", 3)
	if err != nil {
		t.Fatalf("GetEvents(3) error: %v", err)
	}
	if next != 3 || len(events) != 0 {
		t.Errorf("GetEvents(3) = %d events, next %d; want 0 events, 3", len(events), next)
	}

	events, next, err = m.GetEvents(ctx, "s1", "m1", 99)
	if err != nil {
		t.Fatalf("GetEvents(99) error: %v", err)
	}
	if next != 99 || len(events) != 0 {
		t.Errorf("GetEvents(99) = %d events, next %d; want 0 events, 99", len(events), next)
	}
}

func eventIDs(events []interfaces.StreamEvent) []string {
	ids := make([]string, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	return ids
}
