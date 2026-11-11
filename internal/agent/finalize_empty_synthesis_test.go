package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// 在迭代上限处的无工具最终合成，如果模型没有产出任何可见正文（空串、纯空白），
// 旧行为是 nil error + 空答案照常完成，用户界面什么都看不到。
// 这绕过了 handleMaxIterations 已有的失败兜底（issue #5386）。
func TestFinalSynthesisRejectsBlankVisibleAnswer(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{name: "empty", content: ""},
		{name: "whitespace_only", content: "   \n\t  "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := &finalSynthesisChat{}
			engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
			model.synthesize = func(
				context.Context, []chat.Message, *chat.ChatOptions,
			) (<-chan types.StreamResponse, error) {
				return finalSynthesisChunks(types.StreamResponse{
					ResponseType: types.ResponseTypeAnswer,
					Content:      tc.content,
					FinishReason: "stop",
					Done:         true,
				}), nil
			}

			state := &types.AgentState{}
			err := engine.streamFinalAnswerToEventBus(
				t.Context(), "Summarize", state, "session", emptyMessages(),
			)

			require.Error(t, err, "空白可见答案不能被当作成功")
			require.Contains(t, err.Error(), "empty visible answer")
			require.Empty(t, strings.TrimSpace(state.FinalAnswer),
				"失败时不应把空白答案留在 state 上")
		})
	}
}

// 只有 thinking 分块、没有 answer 分块时，可见正文为空，同样必须报错。
func TestFinalSynthesisRejectsThinkingOnlyStream(t *testing.T) {
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
	model.synthesize = func(
		context.Context, []chat.Message, *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		return finalSynthesisChunks(
			types.StreamResponse{
				ResponseType: types.ResponseTypeThinking,
				Content:      "internal reasoning only",
			},
			types.StreamResponse{
				ResponseType: types.ResponseTypeAnswer,
				Content:      "",
				FinishReason: "stop",
				Done:         true,
			},
		), nil
	}

	state := &types.AgentState{}
	err := engine.streamFinalAnswerToEventBus(
		t.Context(), "Summarize", state, "session", emptyMessages(),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty visible answer")
}

func TestFinalSynthesisStillAcceptsNonEmptyAnswer(t *testing.T) {
	// 反向保护：正常有内容的答案不能被这个检查误伤
	model := &finalSynthesisChat{}
	engine := newTestEngine(t, model, withMaxContextTokens(64000), withMaxCompletionTokens(2048))
	model.synthesize = func(
		context.Context, []chat.Message, *chat.ChatOptions,
	) (<-chan types.StreamResponse, error) {
		return finalSynthesisChunks(types.StreamResponse{
			ResponseType: types.ResponseTypeAnswer,
			Content:      "  实际答案内容  ",
			FinishReason: "stop",
			Done:         true,
		}), nil
	}

	state := &types.AgentState{}
	require.NoError(t, engine.streamFinalAnswerToEventBus(
		t.Context(), "Summarize", state, "session", emptyMessages(),
	))
	require.Equal(t, "实际答案内容", state.FinalAnswer)
}
