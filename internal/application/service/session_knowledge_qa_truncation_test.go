package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestConsumeFallbackStreamTruncation(t *testing.T) {
	tests := []struct {
		name      string
		chunks    []string
		reason    string
		truncated bool
	}{
		{"empty length", nil, "length", true},
		{"whitespace length", []string{" ", "\n"}, "length", true},
		{"partial length", []string{"first ", "second"}, "length", true},
		{"max tokens", nil, "max_tokens", true},
		{"max output tokens", nil, "max_output_tokens", true},
		{"normalized reason", nil, " LENGTH ", true},
		{"natural stop", []string{"first ", "second"}, "stop", false},
		{"empty stop", nil, "stop", false},
		{"unknown reason", []string{"answer"}, "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := event.NewEventBus()
			var answers []event.AgentFinalAnswerData
			bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
				answers = append(answers, evt.Data.(event.AgentFinalAnswerData))
				return nil
			})
			cm := &types.ChatManage{}
			cm.EventBus = bus.AsEventBusInterface()
			ch := make(chan types.StreamResponse, len(tt.chunks)+3)
			ch <- types.StreamResponse{ResponseType: types.ResponseTypeThinking, Content: "reasoning", Done: true}
			for _, chunk := range tt.chunks {
				ch <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer, Content: chunk, FinishReason: "length",
				}
			}
			ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: tt.reason}
			ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true}
			close(ch)
			(&sessionService{}).consumeFallbackStream(context.Background(), cm, ch, modelcontext.NewRegistry(false))
			require.GreaterOrEqual(t, len(answers), len(tt.chunks)+1, "ignore reasoning and duplicate EOF")
			// The Done marker is deferred to the channel close — it carries the
			// closing chunk's usage — so every content event precedes it.
			last := len(answers) - 1
			require.True(t, answers[last].Done, "the deferred Done marker closes the stream")
			require.Equal(t, tt.truncated, answers[last].Truncated)
			var delivered strings.Builder
			var lastContent string
			for i, answer := range answers {
				delivered.WriteString(answer.Content)
				require.True(t, answer.IsFallback)
				if i < last {
					require.False(t, answer.Done)
					lastContent = answer.Content
				}
			}
			require.NotNil(t, cm.ChatResponse)
			require.Equal(t, delivered.String(), cm.ChatResponse.Content, "store all emitted chunks")
			original := strings.Join(tt.chunks, "")
			if tt.truncated && strings.TrimSpace(original) == "" {
				// The empty-truncation fallback text streamed as the last content event.
				require.NotEmpty(t, strings.TrimSpace(lastContent))
			} else {
				require.Equal(t, original, delivered.String(), "partial and natural-stop answers must be preserved")
			}
		})
	}
}

func TestConsumeFallbackStreamDecodedTerminalContent(t *testing.T) {
	const resource = "resource://AbCdEfGhIjKlMnOpQrStUv"
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{"terminal text", []string{"last text"}, "last text"},
		{"held suffix", []string{"answer", ""}, "answer"},
		{"split resource", []string{"res://00", "01", ""}, resource},
		{"dropped private handle", []string{"res://9999"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			registry := modelcontext.NewRegistry(false)
			registry.EncodeMessages([]chat.Message{{Role: "user", Content: resource}})
			bus := event.NewEventBus()
			var answers []event.AgentFinalAnswerData
			bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
				answers = append(answers, evt.Data.(event.AgentFinalAnswerData))
				return nil
			})
			cm := &types.ChatManage{}
			cm.EventBus = bus.AsEventBusInterface()
			ch := make(chan types.StreamResponse, len(tc.chunks))
			for i, chunk := range tc.chunks {
				ch <- types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer, Content: chunk,
					Done: i == len(tc.chunks)-1, FinishReason: "length",
				}
			}
			close(ch)
			(&sessionService{}).consumeFallbackStream(context.Background(), cm, ch, registry)
			require.NotEmpty(t, answers)
			last := answers[len(answers)-1]
			require.True(t, last.Truncated)
			require.True(t, last.Done)
			if tc.want != "" {
				require.Equal(t, tc.want, cm.ChatResponse.Content)
			} else {
				require.NotEmpty(t, strings.TrimSpace(cm.ChatResponse.Content))
				require.NotContains(t, cm.ChatResponse.Content, "res://")
			}
		})
	}
}

func TestConsumeFallbackStreamPrematureClose(t *testing.T) {
	bus := event.NewEventBus()
	var answers []event.AgentFinalAnswerData
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		answers = append(answers, evt.Data.(event.AgentFinalAnswerData))
		return nil
	})
	cm := &types.ChatManage{}
	cm.EventBus = bus.AsEventBusInterface()
	cm.FallbackResponse = "configured fallback"
	ch := make(chan types.StreamResponse, 1)
	ch <- types.StreamResponse{
		ResponseType: types.ResponseTypeAnswer, Content: "partial ", FinishReason: "length",
	}
	close(ch)
	(&sessionService{}).consumeFallbackStream(context.Background(), cm, ch, modelcontext.NewRegistry(false))
	require.Len(t, answers, 2)
	require.Equal(t, "partial ", answers[0].Content)
	require.False(t, answers[0].Done)
	require.Equal(t, event.AgentFinalAnswerData{
		Content: cm.FallbackResponse, Done: true, IsFallback: true,
	}, answers[1], "a missing Done marker must keep the existing fixed-fallback behavior")
}

// TestConsumeFallbackStreamCarriesUsageOnDone verifies the fallback half of
// #3865: the Done marker is deferred to the channel close so the usage
// reported on the closing chunk (after finish_reason) rides out with it.
func TestConsumeFallbackStreamCarriesUsageOnDone(t *testing.T) {
	usage := &types.TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}
	bus := event.NewEventBus()
	var answers []event.AgentFinalAnswerData
	bus.On(event.EventAgentFinalAnswer, func(_ context.Context, evt event.Event) error {
		answers = append(answers, evt.Data.(event.AgentFinalAnswerData))
		return nil
	})
	cm := &types.ChatManage{}
	cm.EventBus = bus.AsEventBusInterface()
	ch := make(chan types.StreamResponse, 3)
	ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Content: "fallback text"}
	ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, FinishReason: "stop"}
	ch <- types.StreamResponse{ResponseType: types.ResponseTypeAnswer, Done: true, Usage: usage}
	close(ch)
	(&sessionService{}).consumeFallbackStream(context.Background(), cm, ch, modelcontext.NewRegistry(false))

	require.Len(t, answers, 2)
	require.Equal(t, "fallback text", answers[0].Content)
	require.False(t, answers[0].Done)
	require.True(t, answers[1].Done)
	require.True(t, answers[1].IsFallback)
	gotUsage, ok := answers[1].Usage.(*types.TokenUsage)
	require.True(t, ok, "done marker must carry the turn usage")
	require.Equal(t, usage, gotUsage)
	require.NotNil(t, cm.ChatResponse)
}
