package im

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockStreamSender is a test double that records streaming calls.
type mockStreamSender struct {
	mu            sync.Mutex
	started       bool
	streamID      string
	updates       []string
	finalContent  string
	ended         bool
}

func (m *mockStreamSender) StartStream(_ context.Context, _ *IncomingMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.streamID = "test-stream-1"
	return m.streamID, nil
}

func (m *mockStreamSender) UpdateStreamContent(_ context.Context, _ *IncomingMessage, _ string, fullContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updates = append(m.updates, fullContent)
	return nil
}

func (m *mockStreamSender) FinalizeStream(_ context.Context, _ *IncomingMessage, _ string, finalContent string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finalContent = finalContent
	return nil
}

func (m *mockStreamSender) EndStream(_ context.Context, _ *IncomingMessage, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ended = true
	return nil
}

func (m *mockStreamSender) getUpdates() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.updates))
	copy(out, m.updates)
	return out
}

func TestStreamSenderInterface(t *testing.T) {
	mock := &mockStreamSender{}

	ctx := context.Background()
	incoming := &IncomingMessage{
		Platform: PlatformFeishu,
		UserID:   "test-user",
		Content:  "hello",
	}

	streamID, err := mock.StartStream(ctx, incoming)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}
	if streamID == "" {
		t.Fatal("expected non-empty stream ID")
	}

	// Replace-based updates send the full visible content each time.
	updates := []string{"Hello", "Hello, world", "Hello, world!"}
	for _, content := range updates {
		if err := mock.UpdateStreamContent(ctx, incoming, streamID, content); err != nil {
			t.Fatalf("UpdateStreamContent failed: %v", err)
		}
	}

	if err := mock.FinalizeStream(ctx, incoming, streamID, "Hello, world!"); err != nil {
		t.Fatalf("FinalizeStream failed: %v", err)
	}
	if err := mock.EndStream(ctx, incoming, streamID); err != nil {
		t.Fatalf("EndStream failed: %v", err)
	}

	if !mock.started {
		t.Error("expected stream to be started")
	}
	if !mock.ended {
		t.Error("expected stream to be ended")
	}
	if mock.finalContent != "Hello, world!" {
		t.Errorf("finalContent = %q, want %q", mock.finalContent, "Hello, world!")
	}

	got := mock.getUpdates()
	if len(got) != len(updates) {
		t.Fatalf("expected %d updates, got %d", len(updates), len(got))
	}
	for i, want := range updates {
		if got[i] != want {
			t.Errorf("update[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestStreamFlushBatching(t *testing.T) {
	mock := &mockStreamSender{}

	ctx := context.Background()
	incoming := &IncomingMessage{
		Platform: PlatformFeishu,
		UserID:   "test-user",
		Content:  "test",
	}

	streamID, _ := mock.StartStream(ctx, incoming)

	var buf string
	tokens := []string{"Hello", " ", "world", "!"}
	for _, tok := range tokens {
		buf += tok
	}

	if err := mock.UpdateStreamContent(ctx, incoming, streamID, buf); err != nil {
		t.Fatalf("UpdateStreamContent failed: %v", err)
	}

	got := mock.getUpdates()
	if len(got) != 1 {
		t.Fatalf("expected 1 batched update, got %d", len(got))
	}
	if got[0] != "Hello world!" {
		t.Errorf("batched update = %q, want %q", got[0], "Hello world!")
	}
}

func TestStreamFlushIntervalConstant(t *testing.T) {
	// Verify the flush interval is set to a reasonable value
	if streamFlushInterval < 100*time.Millisecond {
		t.Errorf("streamFlushInterval too small: %v (may cause API rate limiting)", streamFlushInterval)
	}
	if streamFlushInterval > 2*time.Second {
		t.Errorf("streamFlushInterval too large: %v (poor user experience)", streamFlushInterval)
	}
}
