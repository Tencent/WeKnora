package chatpipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/invoke/invoketest"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// ansEv builds an answer delta / Done stream event for the fake seam.
func ansEv(content string, done bool) invoke.StreamEvent {
	ev := invoke.StreamEvent{Kind: invoke.StreamKindAnswer}
	if content != "" {
		ev.Delta = &invoke.ContentDelta{Text: content}
	}
	if done {
		ev.Done = &invoke.FinishInfo{FinishReason: "stop"}
	}
	return ev
}

// stubModelService only needs GetModelByID + BuildModelConfig; the rest is
// unused for these tests.
type stubModelService struct {
	interfaces.ModelService
	fake *invoketest.Fake
}

func (s *stubModelService) GetModelByID(context.Context, string) (*types.Model, error) {
	return &types.Model{}, nil
}

func (s *stubModelService) BuildModelConfig(context.Context, *types.Model) (*invoke.ModelConfig, error) {
	return s.fake.Config(), nil
}

// TestStreamDropsIncompleteHandleOnCancel verifies that cancellation cannot
// leak a model-context protocol fragment to the client.
func TestStreamDropsIncompleteHandleOnCancel(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	bus := newSyncEventBus()
	fake := invoketest.New(t)
	fake.EnqueueStream(
		// Ends with a partial alias prefix ("res://0"), so the stream decoder
		// holds it back waiting for the rest that never arrives before cancel.
		ansEv("hello res://0", false),
	)

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-cancel"
	chatManage.UserContent = ref // seeds the registry so res://0001 becomes a known alias
	chatManage.EventBus = bus

	ctx, cancel := context.WithCancel(context.Background())
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{fake: fake}}
	require.Nil(t, plugin.OnEvent(ctx, types.CHAT_COMPLETION_STREAM, chatManage, func() *PluginError { return nil }))

	// Wait until the pre-hold content has been emitted, then cancel.
	require.Eventually(t, func() bool {
		for _, c := range bus.finalAnswerContents() {
			if c == "hello " {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)

	cancel()

	// Give the cancellation path time to flush, then assert that only the
	// meaningful prefix carried content; the incomplete private handle is
	// dropped. The trailing "" is the entry's clean-EOF synthesized terminal
	// (no provider terminator arrived; it carries no content — the leak
	// invariant under test is about handles, not chunks).
	require.Eventually(t, func() bool { return len(bus.finalAnswerContents()) >= 1 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, []string{"hello ", ""}, bus.finalAnswerContents())
}

func TestStreamIgnoresDuplicateTerminalAnswer(t *testing.T) {
	bus := newSyncEventBus()
	fake := invoketest.New(t)
	fake.EnqueueStream(
		ansEv("hello", false),
		ansEv("", true),
		// Some providers repeat the same terminal response when their EOF
		// sentinel is received after finish_reason.
		ansEv("", true),
	)

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-duplicate-done"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{fake: fake}}
	require.Nil(t, plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, chatManage, func() *PluginError { return nil }))

	// Wait until the consumer goroutine has emitted both answer events.
	require.Eventually(t, func() bool { return len(bus.events) >= 2 },
		2*time.Second, 5*time.Millisecond)

	var answerEvents []event.AgentFinalAnswerData
	for _, e := range bus.events {
		if d, ok := e.Data.(event.AgentFinalAnswerData); ok {
			answerEvents = append(answerEvents, d)
		}
	}
	require.Equal(t, []event.AgentFinalAnswerData{{Content: "hello"}, {Done: true}}, answerEvents)
}

// syncEventBus is a recording EventBusInterface stub: it records final-answer
// events as they are emitted, so tests can assert on them after the stream
// completes.
type syncEventBus struct {
	mu     sync.Mutex
	events []types.Event
}

func newSyncEventBus() *syncEventBus { return &syncEventBus{} }

func (b *syncEventBus) On(types.EventType, types.EventHandler) {}

func (b *syncEventBus) Emit(_ context.Context, evt types.Event) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.events = append(b.events, evt)
	return nil
}

func (b *syncEventBus) finalAnswerContents() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, e := range b.events {
		if d, ok := e.Data.(event.AgentFinalAnswerData); ok {
			out = append(out, d.Content)
		}
	}
	return out
}
