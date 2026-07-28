package chat

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/testutil/artifactrepo"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingChat struct {
	mu       sync.Mutex
	calls    int
	response string
}

func (c *countingChat) Chat(
	context.Context,
	[]Message,
	*ChatOptions,
) (*types.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return &types.ChatResponse{Content: c.response}, nil
}

func (c *countingChat) ChatStream(
	context.Context,
	[]Message,
	*ChatOptions,
) (<-chan types.StreamResponse, error) {
	result := make(chan types.StreamResponse)
	close(result)
	return result, nil
}

func (c *countingChat) GetModelName() string { return "chat-model" }
func (c *countingChat) GetModelID() string   { return "chat-id" }

func (c *countingChat) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func setupChatArtifactRuntime(t *testing.T) *artifact.Runtime {
	t.Helper()
	return artifact.NewRuntime(artifactrepo.New(), nil)
}

func chatArtifactConfig() ArtifactCacheConfig {
	return ArtifactCacheConfig{
		TenantID: 1,
		Processor: artifact.ProcessorIdentity{
			ModelID:   "chat-id",
			ModelName: "chat-model",
			Source:    "remote",
			Provider:  "openai",
		},
	}
}

func TestArtifactCachedChatUsesExactRenderedRequest(t *testing.T) {
	provider := &countingChat{response: "summary"}
	cached := NewArtifactCachedChat(provider, setupChatArtifactRuntime(t), chatArtifactConfig())
	ctx := WithArtifactStage(context.Background(), ArtifactStage{
		Stage:        "summary",
		OutputSchema: "summary.text.v1",
	})
	thinking := false
	options := &ChatOptions{Temperature: 0.3, Thinking: &thinking}

	first, err := cached.Chat(ctx, []Message{{Role: "user", Content: " alpha\r\n "}}, options)
	require.NoError(t, err)
	second, err := cached.Chat(ctx, []Message{{Role: "user", Content: " alpha\r\n "}}, options)
	require.NoError(t, err)
	assert.Equal(t, first.Content, second.Content)
	assert.Equal(t, 1, provider.callCount())

	_, err = cached.Chat(ctx, []Message{{Role: "user", Content: "alpha\n"}}, options)
	require.NoError(t, err)
	assert.Equal(t, 2, provider.callCount())
}

func TestArtifactCachedChatDoesNotCacheInvalidOutput(t *testing.T) {
	provider := &countingChat{response: ""}
	cached := NewArtifactCachedChat(provider, setupChatArtifactRuntime(t), chatArtifactConfig())
	ctx := WithArtifactStage(context.Background(), ArtifactStage{
		Stage:        "summary",
		OutputSchema: "summary.text.v1",
		Validate: func(content string) error {
			if content == "" {
				return errors.New("empty")
			}
			return nil
		},
	})

	_, err := cached.Chat(ctx, []Message{{Role: "user", Content: "content"}}, &ChatOptions{})
	require.Error(t, err)
	_, err = cached.Chat(ctx, []Message{{Role: "user", Content: "content"}}, &ChatOptions{})
	require.Error(t, err)
	assert.Equal(t, 2, provider.callCount())
}

func TestChatArtifactConfigExcludesCredentialRotation(t *testing.T) {
	model := &types.Model{
		ID:     "chat-id",
		Name:   "chat-model",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:  "https://example.com/v1",
			APIKey:   "secret-one",
			Provider: "openai",
		},
	}
	first, ok := ArtifactCacheConfigFromModel(model, 1)
	require.True(t, ok)
	model.Parameters.APIKey = "secret-two"
	model.Description = "unrelated"
	second, ok := ArtifactCacheConfigFromModel(model, 1)
	require.True(t, ok)
	assert.Equal(t, first, second)
}
