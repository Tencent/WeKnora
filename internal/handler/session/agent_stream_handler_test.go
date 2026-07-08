package session

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/stream"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func TestAgentStreamHandlerErrorIncludesPartialAnswer(t *testing.T) {
	ctx := context.Background()
	streamManager := stream.NewMemoryStreamManager()
	handler := NewAgentStreamHandler(
		ctx,
		"session-1",
		"message-1",
		"request-1",
		time.Now(),
		&types.Message{ID: "message-1", SessionID: "session-1", Role: "assistant"},
		streamManager,
		event.NewEventBus(),
	)

	err := handler.handleFinalAnswer(ctx, event.Event{
		ID:        "answer-1",
		Type:      event.EventAgentFinalAnswer,
		SessionID: "session-1",
		Data: event.AgentFinalAnswerData{
			Content: "已经生成的部分答案",
			Done:    false,
		},
	})
	require.NoError(t, err)

	err = handler.handleError(ctx, event.Event{
		ID:        "error-1",
		Type:      event.EventError,
		SessionID: "session-1",
		Data: event.ErrorData{
			Error:     "回答生成中断：模型服务暂时不可用或连接中断，请稍后重试。",
			Stage:     "chat_completion_stream",
			SessionID: "session-1",
			Extra: map[string]interface{}{
				"terminal": true,
			},
		},
	})
	require.NoError(t, err)

	events, _, err := streamManager.GetEvents(ctx, "session-1", "message-1", 0)
	require.NoError(t, err)
	require.Len(t, events, 2)
	require.Equal(t, types.ResponseTypeAnswer, events[0].Type)
	require.Equal(t, types.ResponseTypeError, events[1].Type)
	require.True(t, events[1].Done)
	require.Equal(t,
		"已经生成的部分答案\n\n回答生成中断：模型服务暂时不可用或连接中断，请稍后重试。",
		events[1].Content,
	)
	require.Equal(t, true, events[1].Data["terminal"])
}

func TestAppendTerminalErrorMessageAvoidsDuplicate(t *testing.T) {
	msg := appendTerminalErrorMessage("已有答案\n\n生成失败", "生成失败")
	require.Equal(t, "已有答案\n\n生成失败", msg)

	msg = appendTerminalErrorMessage("", "生成失败")
	require.Equal(t, "生成失败", msg)

	msg = appendTerminalErrorMessage("已有答案", "生成失败")
	require.Equal(t, "已有答案\n\n生成失败", msg)
}

var _ interfaces.StreamManager = (*stream.MemoryStreamManager)(nil)
