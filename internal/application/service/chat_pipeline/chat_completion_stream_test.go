package chatpipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// syncEventBus is a thread-safe recorder; the stream plugin emits from a
// background goroutine so the test must guard concurrent appends.
type syncEventBus struct {
	mu     sync.Mutex
	events []types.Event
}

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
	for _, evt := range b.events {
		if evt.Type != types.EventType(event.EventAgentFinalAnswer) {
			continue
		}
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			out = append(out, data.Content)
		}
	}
	return out
}

func (b *syncEventBus) thoughtContents() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for _, evt := range b.events {
		if evt.Type != types.EventType(event.EventAgentThought) {
			continue
		}
		if data, ok := evt.Data.(event.AgentThoughtData); ok && data.Content != "" {
			out = append(out, data.Content)
		}
	}
	return out
}

// thinkingDoneMarkers counts the empty Done markers that flip the thinking
// card to its completed state.
func (b *syncEventBus) thinkingDoneMarkers() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, evt := range b.events {
		if evt.Type != types.EventType(event.EventAgentThought) {
			continue
		}
		if data, ok := evt.Data.(event.AgentThoughtData); ok && data.Done && data.Content == "" {
			n++
		}
	}
	return n
}

func (b *syncEventBus) eventCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}

// openStreamChat returns a buffered channel preloaded with chunks and never
// closes it, so the stream plugin blocks on the channel until ctx is cancelled
// — deterministically exercising the ctx.Done() branch.
type openStreamChat struct {
	chunks      []types.StreamResponse
	closeStream bool
}

func (m *openStreamChat) Chat(context.Context, []chat.Message, *chat.ChatOptions) (*types.ChatResponse, error) {
	return nil, nil
}

func (m *openStreamChat) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	ch := make(chan types.StreamResponse, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	if m.closeStream {
		close(ch)
	}
	return ch, nil
}

func (m *openStreamChat) GetModelName() string { return "mock" }
func (m *openStreamChat) GetModelID() string   { return "mock" }

// stubModelService only needs GetChatModel; the rest is unused for this test.
type stubModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s *stubModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

// TestStreamDropsIncompleteHandleOnCancel verifies that cancellation cannot
// leak a model-context protocol fragment to the client.
func TestStreamDropsIncompleteHandleOnCancel(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	bus := &syncEventBus{}
	model := &openStreamChat{chunks: []types.StreamResponse{
		// Ends with a partial alias prefix ("res://0"), so the stream decoder
		// holds it back waiting for the rest that never arrives before cancel.
		{ResponseType: types.ResponseTypeAnswer, Content: "hello res://0"},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-cancel"
	chatManage.UserContent = ref // seeds the registry so res://0001 becomes a known alias
	chatManage.EventBus = bus

	ctx, cancel := context.WithCancel(context.Background())
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
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
	// meaningful prefix was emitted; the incomplete private handle is dropped.
	require.Eventually(t, func() bool { return len(bus.finalAnswerContents()) >= 1 }, 2*time.Second, 5*time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, []string{"hello "}, bus.finalAnswerContents())
}

func TestStreamIgnoresDuplicateTerminalAnswer(t *testing.T) {
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "hello"},
		{ResponseType: types.ResponseTypeAnswer, Done: true},
		// Some providers repeat the same terminal response when their EOF
		// sentinel is received after finish_reason.
		{ResponseType: types.ResponseTypeAnswer, Done: true},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-duplicate-done"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, chatManage, func() *PluginError { return nil }))

	require.Eventually(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return len(bus.events) == 2
	}, 2*time.Second, 5*time.Millisecond)

	bus.mu.Lock()
	defer bus.mu.Unlock()
	var answerEvents []event.AgentFinalAnswerData
	for _, evt := range bus.events {
		if evt.Type == types.EventType(event.EventAgentFinalAnswer) {
			answerEvents = append(answerEvents, evt.Data.(event.AgentFinalAnswerData))
		}
	}
	require.Equal(t, []event.AgentFinalAnswerData{{Content: "hello"}, {Done: true}}, answerEvents)
}

// runStreamPlugin feeds the chunks through the stream plugin and waits for the
// goroutine to drain the channel (or the plugin to return).
func runStreamPlugin(t *testing.T, chunks []types.StreamResponse) *syncEventBus {
	t.Helper()
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: chunks}
	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-inline-think"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(context.Background(), types.CHAT_COMPLETION_STREAM, chatManage, func() *PluginError { return nil }))
	require.Eventually(t, func() bool { return bus.eventCount() > 0 }, 2*time.Second, 5*time.Millisecond)
	return bus
}

// TestStreamSplitsInlineThinkBlocks verifies that <think>…</think> reasoning
// embedded in the plain content channel is routed to thought events instead of
// leaking raw tags into the answer stream (#3099).
func TestStreamSplitsInlineThinkBlocks(t *testing.T) {
	bus := runStreamPlugin(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "<think>rea"},
		{ResponseType: types.ResponseTypeAnswer, Content: "soning</think>ans"},
		{ResponseType: types.ResponseTypeAnswer, Content: "wer"},
		{ResponseType: types.ResponseTypeAnswer, Done: true},
	})

	require.Equal(t, []string{"rea", "soning"}, bus.thoughtContents())
	require.Equal(t, 1, bus.thinkingDoneMarkers())
	answers := bus.finalAnswerContents()
	require.NotEmpty(t, answers)
	var joined string
	for _, a := range answers {
		joined += a
	}
	require.Equal(t, "answer", joined)
	for _, a := range answers {
		require.NotContains(t, a, "<think>")
		require.NotContains(t, a, "</think>")
	}
}

// TestStreamSplitsMultipleInlineThinkBlocks covers models that reason once per
// round and therefore emit several interleaved think blocks.
func TestStreamSplitsMultipleInlineThinkBlocks(t *testing.T) {
	bus := runStreamPlugin(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "<think>a</think>A<think>b</think>B"},
		{ResponseType: types.ResponseTypeAnswer, Done: true},
	})

	require.Equal(t, []string{"ab"}, bus.thoughtContents())
	require.Equal(t, 1, bus.thinkingDoneMarkers())
	answers := bus.finalAnswerContents()
	require.NotEmpty(t, answers)
	var joined string
	for _, a := range answers {
		joined += a
	}
	require.Equal(t, "AB", joined)
	for _, a := range answers {
		require.NotContains(t, a, "think>")
	}
}

// TestStreamUnterminatedThinkBlockGoesToThought verifies the flush path: an
// unterminated <think> block at end of stream is thinking text, not answer.
func TestStreamUnterminatedThinkBlockGoesToThought(t *testing.T) {
	bus := runStreamPlugin(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "<think>only thinking"},
		{ResponseType: types.ResponseTypeAnswer, Done: true},
	})

	require.Equal(t, []string{"only thinking"}, bus.thoughtContents())
	require.Equal(t, 1, bus.thinkingDoneMarkers())
	answers := bus.finalAnswerContents()
	var joined string
	for _, a := range answers {
		joined += a
		require.NotContains(t, a, "think>")
	}
	require.Equal(t, "", joined)
}

// TestStreamPlainAnswerUnchanged guards the no-tags path: plain content must
// stream exactly as before the inline-think splitter was introduced.
func TestStreamPlainAnswerUnchanged(t *testing.T) {
	bus := runStreamPlugin(t, []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "hello "},
		{ResponseType: types.ResponseTypeAnswer, Content: "world"},
		{ResponseType: types.ResponseTypeAnswer, Done: true},
	})

	require.Empty(t, bus.thoughtContents())
	require.Equal(t, []string{"hello ", "world", ""}, bus.finalAnswerContents())
}
