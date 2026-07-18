package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

const wikiMapPromptVersion = "v1"

type wikiMapChatCacheKey struct {
	TenantID      uint64
	KnowledgeID   string
	KnowledgeBaseID string
	ModelID       string
	ModelName     string
	PromptVersion string
	ContentHash   string
	RequestHash   string
}

var processWikiMapChatCache = struct {
	sync.RWMutex
	data map[wikiMapChatCacheKey]*types.ChatResponse
}{data: make(map[wikiMapChatCacheKey]*types.ChatResponse)}

type wikiMapCachedChat struct {
	base            chat.Chat
	tenantID        uint64
	knowledgeID     string
	knowledgeBaseID string
	contentHash      string
}

func newWikiMapCachedChat(
	base chat.Chat,
	tenantID uint64,
	knowledgeID string,
	knowledgeBaseID string,
	content string,
) chat.Chat {
	return &wikiMapCachedChat{
		base:            base,
		tenantID:        tenantID,
		knowledgeID:     knowledgeID,
		knowledgeBaseID: knowledgeBaseID,
		contentHash:      types.StableContentHash(content),
	}
}

func (c *wikiMapCachedChat) Chat(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	key, err := c.cacheKey(messages, opts)
	if err != nil || key.ContentHash == "" {
		return c.base.Chat(ctx, messages, opts)
	}

	processWikiMapChatCache.RLock()
	resp, ok := processWikiMapChatCache.data[key]
	processWikiMapChatCache.RUnlock()
	if ok {
		return cloneChatResponse(resp), nil
	}

	resp, err := c.base.Chat(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	processWikiMapChatCache.Lock()
	processWikiMapChatCache.data[key] = cloneChatResponse(resp)
	processWikiMapChatCache.Unlock()
	return resp, nil
}

func (c *wikiMapCachedChat) ChatStream(
	ctx context.Context,
	messages []chat.Message,
	opts *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return c.base.ChatStream(ctx, messages, opts)
}

func (c *wikiMapCachedChat) GetModelName() string {
	return c.base.GetModelName()
}

func (c *wikiMapCachedChat) GetModelID() string {
	return c.base.GetModelID()
}

func (c *wikiMapCachedChat) cacheKey(messages []chat.Message, opts *chat.ChatOptions) (wikiMapChatCacheKey, error) {
	requestBytes, err := json.Marshal(struct {
		Messages []chat.Message    `json:"messages"`
		Options  *chat.ChatOptions `json:"options"`
	}{Messages: messages, Options: opts})
	if err != nil {
		return wikiMapChatCacheKey{}, fmt.Errorf("marshal wiki map cache request: %w", err)
	}
	return wikiMapChatCacheKey{
		TenantID:        c.tenantID,
		KnowledgeID:     c.knowledgeID,
		KnowledgeBaseID: c.knowledgeBaseID,
		ModelID:         c.base.GetModelID(),
		ModelName:       c.base.GetModelName(),
		PromptVersion:   wikiMapPromptVersion,
		ContentHash:     c.contentHash,
		RequestHash:     stableStringHash(string(requestBytes)),
	}, nil
}

func cloneChatResponse(resp *types.ChatResponse) *types.ChatResponse {
	if resp == nil {
		return nil
	}
	cloned := *resp
	cloned.ToolCalls = append([]types.LLMToolCall(nil), resp.ToolCalls...)
	return &cloned
}
