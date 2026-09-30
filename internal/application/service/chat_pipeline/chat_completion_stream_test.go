package chatpipeline

import (
	"context"
	"strings"
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

func (b *syncEventBus) finalAnswerEvents() []event.AgentFinalAnswerData {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []event.AgentFinalAnswerData
	for _, evt := range b.events {
		if evt.Type != types.EventType(event.EventAgentFinalAnswer) {
			continue
		}
		if data, ok := evt.Data.(event.AgentFinalAnswerData); ok {
			out = append(out, data)
		}
	}
	return out
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

func TestStreamReportsEmptyLengthTruncation(t *testing.T) {
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeThinking, Content: "planning"},
		{ResponseType: types.ResponseTypeThinking, Done: true},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "length"},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-empty-length"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(
		context.Background(), types.CHAT_COMPLETION_STREAM, chatManage,
		func() *PluginError { return nil },
	))

	// The fallback text streams out as a content event; the Done marker waits
	// for the channel to close so it can carry the closing chunk's usage.
	want := []event.AgentFinalAnswerData{
		{Content: EmptyTruncatedAnswerFallback, Truncated: true},
		{Done: true, Truncated: true},
	}
	require.Eventually(t, func() bool {
		return len(bus.finalAnswerEvents()) == len(want)
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, want, bus.finalAnswerEvents())
}

func TestStreamMarksPartialLengthTruncation(t *testing.T) {
	for _, finishReason := range []string{"length", "max_tokens", "max_output_tokens"} {
		t.Run(finishReason, func(t *testing.T) {
			bus := &syncEventBus{}
			model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
				{ResponseType: types.ResponseTypeAnswer, Content: "partial answer"},
				{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: finishReason},
			}}

			chatManage := &types.ChatManage{}
			chatManage.SessionID = "sess-partial-" + finishReason
			chatManage.EventBus = bus
			plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
			require.Nil(t, plugin.OnEvent(
				context.Background(), types.CHAT_COMPLETION_STREAM, chatManage,
				func() *PluginError { return nil },
			))

			require.Eventually(t, func() bool {
				return len(bus.finalAnswerEvents()) >= 2
			}, 2*time.Second, 5*time.Millisecond)
			events := bus.finalAnswerEvents()
			var delivered strings.Builder
			for _, e := range events[:len(events)-1] {
				delivered.WriteString(e.Content)
				require.False(t, e.Done)
			}
			last := events[len(events)-1]
			require.True(t, last.Done)
			require.True(t, last.Truncated)
			require.NotContains(t, last.Content, EmptyTruncatedAnswerFallback)
			require.Equal(t, "partial answer", delivered.String())
		})
	}
}

// TestStreamCarriesUsageOnDoneMarker verifies the fix for #3865: providers
// report usage on the stream's closing chunk, after finish_reason, so the
// Done marker must wait for the channel close and carry that usage out.
func TestStreamCarriesUsageOnDoneMarker(t *testing.T) {
	usage := &types.TokenUsage{PromptTokens: 21203, CompletionTokens: 27, TotalTokens: 21230}
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "DONE"},
		// finish_reason first…
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
		// …then the EOF sentinel carrying usage, mirroring StreamAssembler.End.
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop", Usage: usage},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-usage"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(
		context.Background(), types.CHAT_COMPLETION_STREAM, chatManage,
		func() *PluginError { return nil },
	))

	want := []event.AgentFinalAnswerData{
		{Content: "DONE"},
		{Done: true, Usage: usage},
	}
	require.Eventually(t, func() bool {
		return len(bus.finalAnswerEvents()) == len(want)
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, want, bus.finalAnswerEvents())
}

// TestStreamUsageOnFinishChunkAloneStillCarried covers providers that attach
// usage to the finish_reason chunk itself and close right after: the Done
// marker is emitted at the channel close either way.
func TestStreamUsageOnFinishChunkAloneStillCarried(t *testing.T) {
	usage := &types.TokenUsage{PromptTokens: 5, CompletionTokens: 7, TotalTokens: 12}
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "ok"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop", Usage: usage},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-usage-inline"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(
		context.Background(), types.CHAT_COMPLETION_STREAM, chatManage,
		func() *PluginError { return nil },
	))

	want := []event.AgentFinalAnswerData{
		{Content: "ok"},
		{Done: true, Usage: usage},
	}
	require.Eventually(t, func() bool {
		return len(bus.finalAnswerEvents()) == len(want)
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, want, bus.finalAnswerEvents())
}

func TestStreamLeavesEmptyNaturalStopUnchanged(t *testing.T) {
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-empty-stop"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(
		context.Background(), types.CHAT_COMPLETION_STREAM, chatManage,
		func() *PluginError { return nil },
	))

	require.Eventually(t, func() bool {
		return len(bus.finalAnswerEvents()) == 1
	}, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, []event.AgentFinalAnswerData{{Done: true}}, bus.finalAnswerEvents())
}

func TestStreamReportsIncompleteAsError(t *testing.T) {
	bus := &syncEventBus{}
	model := &openStreamChat{closeStream: true, chunks: []types.StreamResponse{
		{ResponseType: types.ResponseTypeAnswer, Content: "partial"},
		{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: types.FinishReasonIncomplete},
	}}

	chatManage := &types.ChatManage{}
	chatManage.SessionID = "sess-incomplete"
	chatManage.EventBus = bus
	plugin := &PluginChatCompletionStream{modelService: &stubModelService{model: model}}
	require.Nil(t, plugin.OnEvent(
		context.Background(), types.CHAT_COMPLETION_STREAM, chatManage,
		func() *PluginError { return nil },
	))

	errorMessage := func() string {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		for _, evt := range bus.events {
			if data, ok := evt.Data.(event.ErrorData); ok {
				return data.Error
			}
		}
		return ""
	}
	require.Eventually(t, func() bool { return errorMessage() != "" }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, types.StreamEndedEarlyError, errorMessage())
	for _, answer := range bus.finalAnswerEvents() {
		require.False(t, answer.Done, "a cut-off stream must not close the answer as complete")
	}
}
