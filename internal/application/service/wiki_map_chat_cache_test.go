package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type countingChat struct {
	calls     int
	modelID   string
	modelName string
}

func (c *countingChat) Chat(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	c.calls++
	return &types.ChatResponse{Content: fmt.Sprintf("response-%d", c.calls)}, nil
}

func (c *countingChat) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, nil
}

func (c *countingChat) GetModelName() string { return c.modelName }
func (c *countingChat) GetModelID() string   { return c.modelID }

func TestWikiMapCachedChatReusesSameMapRequest(t *testing.T) {
	resetProcessWikiMapChatCacheForTest()

	base := &countingChat{modelID: "chat-1", modelName: "gpt"}
	cached := newWikiMapCachedChat(base, 7, "knowledge-1", "kb-1", "same content")
	messages := []chat.Message{{Role: "user", Content: "extract same content"}}

	first, err := cached.Chat(context.Background(), messages, &chat.ChatOptions{Temperature: 0.1})
	require.NoError(t, err)
	second, err := cached.Chat(context.Background(), messages, &chat.ChatOptions{Temperature: 0.1})
	require.NoError(t, err)

	require.Equal(t, first.Content, second.Content)
	require.Equal(t, 1, base.calls)
}

func TestWikiMapCachedChatMissesOnContentOrModelChange(t *testing.T) {
	resetProcessWikiMapChatCacheForTest()

	firstModel := &countingChat{modelID: "chat-1", modelName: "gpt"}
	messages := []chat.Message{{Role: "user", Content: "extract"}}
	cacheA := newWikiMapCachedChat(firstModel, 7, "knowledge-1", "kb-1", "content-a")
	_, err := cacheA.Chat(context.Background(), messages, &chat.ChatOptions{Temperature: 0.1})
	require.NoError(t, err)

	cacheB := newWikiMapCachedChat(firstModel, 7, "knowledge-1", "kb-1", "content-b")
	_, err = cacheB.Chat(context.Background(), messages, &chat.ChatOptions{Temperature: 0.1})
	require.NoError(t, err)

	secondModel := &countingChat{modelID: "chat-2", modelName: "gpt"}
	cacheOtherModel := newWikiMapCachedChat(secondModel, 7, "knowledge-1", "kb-1", "content-a")
	_, err = cacheOtherModel.Chat(context.Background(), messages, &chat.ChatOptions{Temperature: 0.1})
	require.NoError(t, err)

	require.Equal(t, 2, firstModel.calls)
	require.Equal(t, 1, secondModel.calls)
}

func resetProcessWikiMapChatCacheForTest() {
	processWikiMapChatCache.Lock()
	defer processWikiMapChatCache.Unlock()
	processWikiMapChatCache.data = make(map[wikiMapChatCacheKey]*types.ChatResponse)
}
