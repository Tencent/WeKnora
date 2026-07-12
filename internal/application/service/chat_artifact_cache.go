package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/redis/go-redis/v9"
)

type cachedArtifactChat struct {
	inner     chat.Chat
	redis     *redis.Client
	namespace string
}

func cacheArtifactChat(client *redis.Client, inner chat.Chat, namespace string) chat.Chat {
	if client == nil || inner == nil {
		return inner
	}
	return &cachedArtifactChat{inner: inner, redis: client, namespace: namespace}
}

func (c *cachedArtifactChat) Chat(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	input, _ := json.Marshal(struct {
		Messages []chat.Message    `json:"messages"`
		Options  *chat.ChatOptions `json:"options"`
	}{messages, opts})
	sum := sha256.Sum256(input)
	key := "weknora:artifact:chat:" + c.namespace + ":" + artifactModelKey(c.GetModelID(), c.GetModelName()) + ":" + hex.EncodeToString(sum[:])
	if raw, err := c.redis.Get(ctx, key).Bytes(); err == nil {
		var response types.ChatResponse
		if json.Unmarshal(raw, &response) == nil {
			return &response, nil
		}
	}
	response, err := c.inner.Chat(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	if raw, marshalErr := json.Marshal(response); marshalErr == nil {
		_ = c.redis.Set(ctx, key, raw, artifactCacheTTL).Err()
	}
	return response, nil
}

func (c *cachedArtifactChat) ChatStream(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return c.inner.ChatStream(ctx, messages, opts)
}

func (c *cachedArtifactChat) GetModelName() string { return c.inner.GetModelName() }
func (c *cachedArtifactChat) GetModelID() string   { return c.inner.GetModelID() }
