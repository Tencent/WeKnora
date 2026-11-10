package chatpipeline

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recentMessagesStub struct {
	interfaces.MessageService
	messages []*types.Message
}

func (s *recentMessagesStub) GetRecentMessagesBySession(context.Context, string, int) ([]*types.Message, error) {
	return s.messages, nil
}

func TestLoadAndProcessHistoryKeepsImageOnlyTurn(t *testing.T) {
	at := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	svc := &recentMessagesStub{messages: []*types.Message{
		{RequestID: "r1", Role: "user", Images: types.MessageImages{{URL: "resource://img"}}, CreatedAt: at},
		{RequestID: "r1", Role: "assistant", Content: "a cat", CreatedAt: at.Add(time.Second)},
	}}
	ctx := context.WithValue(context.Background(), types.LanguageContextKey, "zh-CN")

	history, err := loadAndProcessHistory(ctx, svc, "s1", 5, 20)
	require.NoError(t, err)

	require.Len(t, history, 1, "an image-only turn must stay in history for follow-up questions")
	assert.Equal(t, types.UploadOnlyQuestion("zh-CN"), history[0].Query)
	assert.Equal(t, "a cat", history[0].Answer)
}
