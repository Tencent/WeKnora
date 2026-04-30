package handler

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMemoryOIDCCallbackTicketStoreConsumeOnce(t *testing.T) {
	now := time.Now()
	store := &memoryOIDCCallbackTicketStore{
		entries: make(map[string]memoryOIDCCallbackTicketEntry),
		ttl:     time.Minute,
		now:     func() time.Time { return now },
	}

	payload, err := json.Marshal(&types.OIDCCallbackResponse{
		Success:      true,
		Token:        "access-token",
		RefreshToken: "refresh-token",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if err := store.SavePayload("ticket-1", payload); err != nil {
		t.Fatalf("SavePayload() error = %v", err)
	}

	got, ok, err := store.ConsumePayload("ticket-1")
	if err != nil {
		t.Fatalf("ConsumePayload() error = %v", err)
	}
	if !ok {
		t.Fatalf("ConsumePayload() ok = false, want true")
	}
	if string(got) != string(payload) {
		t.Fatalf("ConsumePayload() payload = %q, want %q", string(got), string(payload))
	}

	_, ok, err = store.ConsumePayload("ticket-1")
	if err != nil {
		t.Fatalf("ConsumePayload() second call error = %v", err)
	}
	if ok {
		t.Fatalf("ConsumePayload() second call ok = true, want false")
	}
}

func TestMemoryOIDCCallbackTicketStoreExpires(t *testing.T) {
	now := time.Now()
	store := &memoryOIDCCallbackTicketStore{
		entries: make(map[string]memoryOIDCCallbackTicketEntry),
		ttl:     time.Minute,
		now:     func() time.Time { return now },
	}

	payload, err := json.Marshal(&types.OIDCCallbackResponse{Success: true, Token: "access-token"})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if err := store.SavePayload("ticket-1", payload); err != nil {
		t.Fatalf("SavePayload() error = %v", err)
	}

	now = now.Add(2 * time.Minute)

	_, ok, err = store.ConsumePayload("ticket-1")
	if err != nil {
		t.Fatalf("ConsumePayload() error = %v", err)
	}
	if ok {
		t.Fatalf("ConsumePayload() ok = true, want false for expired ticket")
	}
}
