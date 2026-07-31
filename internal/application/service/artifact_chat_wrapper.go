package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type artifactCachingChat struct {
	inner   chat.Chat
	runtime *artifact.Runtime
	tenant  uint64
	stage   string
}

func newArtifactCachingChat(inner chat.Chat, runtime *artifact.Runtime, tenantID uint64, stage string) chat.Chat {
	return &artifactCachingChat{
		inner:   inner,
		runtime: runtime,
		tenant:  tenantID,
		stage:   stage,
	}
}

func (c *artifactCachingChat) Chat(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	if c.runtime == nil || c.tenant == 0 {
		return c.inner.Chat(ctx, messages, opts)
	}
	material, err := chatArtifactKeyMaterial(c.stage, c.inner, messages, opts)
	if err != nil {
		return c.inner.Chat(ctx, messages, opts)
	}
	encoded, _, err := c.runtime.GetOrCompute(ctx, c.tenant, material, func(callCtx context.Context) ([]byte, error) {
		response, computeErr := c.inner.Chat(callCtx, messages, opts)
		if computeErr != nil {
			return nil, computeErr
		}
		content := ""
		if response != nil {
			content = strings.TrimSpace(response.Content)
		}
		if content == "" {
			return nil, errEmptyChatArtifact
		}
		return json.Marshal(content)
	})
	if err != nil {
		if errors.Is(err, errEmptyChatArtifact) {
			return &types.ChatResponse{}, nil
		}
		return nil, err
	}
	var content string
	if err := json.Unmarshal(encoded, &content); err != nil {
		return nil, err
	}
	return &types.ChatResponse{Content: strings.TrimSpace(content)}, nil
}

func (c *artifactCachingChat) ChatStream(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return c.inner.ChatStream(ctx, messages, opts)
}

func (c *artifactCachingChat) GetModelName() string { return c.inner.GetModelName() }
func (c *artifactCachingChat) GetModelID() string   { return c.inner.GetModelID() }
