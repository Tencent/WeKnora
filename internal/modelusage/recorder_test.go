package modelusage

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

type fakeUsageStore struct {
	mu     sync.Mutex
	events []types.ModelUsageEvent
}

func (s *fakeUsageStore) Create(_ context.Context, event *types.ModelUsageEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, *event)
	return nil
}

func TestAsyncRecorderRecordFillsContextFields(t *testing.T) {
	store := &fakeUsageStore{}
	rec := NewAsyncRecorder(store)

	ctx := context.Background()
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
	ctx = context.WithValue(ctx, types.RequestIDContextKey, "req-1")

	rec.Record(ctx, types.ModelUsageEvent{
		ModelID:          "model-1",
		ModelName:        "gpt-test",
		ModelType:        types.ModelTypeKnowledgeQA,
		ModelSource:      types.ModelSourceRemote,
		RequestKind:      "chat.completion",
		PromptTokens:     10,
		CompletionTokens: 5,
		Success:          true,
	})

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rec.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown recorder: %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(store.events))
	}
	got := store.events[0]
	if got.TenantID != 7 || got.UserID != "user-1" || got.RequestID != "req-1" {
		t.Fatalf("context fields not copied: %+v", got)
	}
	if got.TotalTokens != 15 {
		t.Fatalf("expected total tokens to be derived, got %d", got.TotalTokens)
	}
	if got.UsageSource != types.ModelUsageSourceMissing {
		t.Fatalf("expected missing usage source default, got %q", got.UsageSource)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be set")
	}
}

func TestAsyncRecorderDropsEventsWithoutTenant(t *testing.T) {
	store := &fakeUsageStore{}
	rec := NewAsyncRecorder(store)
	rec.Record(context.Background(), types.ModelUsageEvent{ModelID: "model-1"})

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rec.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("shutdown recorder: %v", err)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.events) != 0 {
		t.Fatalf("expected no events, got %d", len(store.events))
	}
}
