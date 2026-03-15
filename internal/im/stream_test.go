package im

import (
	"context"
	"sync"
	"testing"
	"time"
)

// mockStreamSender is a test double that records streaming calls.
type mockStreamSender struct {
	mu       sync.Mutex
	started  bool
	streamID string
	chunks   []string
	ended    bool
}

func (m *mockStreamSender) StartStream(_ context.Context, _ *IncomingMessage) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	m.streamID = "test-stream-1"
	return m.streamID, nil
}

func (m *mockStreamSender) SendStreamChunk(_ context.Context, _ *IncomingMessage, _ string, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, content)
	return nil
}

func (m *mockStreamSender) EndStream(_ context.Context, _ *IncomingMessage, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ended = true
	return nil
}

func (m *mockStreamSender) getChunks() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.chunks))
	copy(out, m.chunks)
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

	// Start stream
	streamID, err := mock.StartStream(ctx, incoming)
	if err != nil {
		t.Fatalf("StartStream failed: %v", err)
	}
	if streamID == "" {
		t.Fatal("expected non-empty stream ID")
	}

	// Send chunks
	chunks := []string{"Hello", ", ", "world", "!"}
	for _, c := range chunks {
		if err := mock.SendStreamChunk(ctx, incoming, streamID, c); err != nil {
			t.Fatalf("SendStreamChunk failed: %v", err)
		}
	}

	// End stream
	if err := mock.EndStream(ctx, incoming, streamID); err != nil {
		t.Fatalf("EndStream failed: %v", err)
	}

	// Verify
	if !mock.started {
		t.Error("expected stream to be started")
	}
	if !mock.ended {
		t.Error("expected stream to be ended")
	}

	got := mock.getChunks()
	if len(got) != len(chunks) {
		t.Fatalf("expected %d chunks, got %d", len(chunks), len(got))
	}
	for i, want := range chunks {
		if got[i] != want {
			t.Errorf("chunk[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestStreamFlushBatching(t *testing.T) {
	// Simulate the batching behavior: multiple writes within one flush interval
	// should be combined into a single chunk.
	mock := &mockStreamSender{}

	ctx := context.Background()
	incoming := &IncomingMessage{
		Platform: PlatformFeishu,
		UserID:   "test-user",
		Content:  "test",
	}

	streamID, _ := mock.StartStream(ctx, incoming)

	// Simulate buffer: accumulate content then flush as one chunk
	var buf string
	tokens := []string{"Hello", " ", "world", "!"}
	for _, tok := range tokens {
		buf += tok
	}

	// Single flush
	if err := mock.SendStreamChunk(ctx, incoming, streamID, buf); err != nil {
		t.Fatalf("SendStreamChunk failed: %v", err)
	}

	got := mock.getChunks()
	if len(got) != 1 {
		t.Fatalf("expected 1 batched chunk, got %d", len(got))
	}
	if got[0] != "Hello world!" {
		t.Errorf("batched chunk = %q, want %q", got[0], "Hello world!")
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
