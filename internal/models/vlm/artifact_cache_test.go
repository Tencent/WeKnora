package vlm

import (
	"context"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/testutil/artifactrepo"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingVLM struct {
	mu       sync.Mutex
	calls    int
	response string
}

func (v *countingVLM) Predict(context.Context, [][]byte, string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	return v.response, nil
}

func (v *countingVLM) GetModelName() string { return "vision-model" }
func (v *countingVLM) GetModelID() string   { return "vision-id" }

func (v *countingVLM) callCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls
}

func setupVLMArtifactRuntime(t *testing.T) *artifact.Runtime {
	t.Helper()
	return artifact.NewRuntime(artifactrepo.New(), nil)
}

func vlmArtifactConfig() ArtifactCacheConfig {
	return ArtifactCacheConfig{
		TenantID: 1,
		Processor: artifact.ProcessorIdentity{
			ModelID:   "vision-id",
			ModelName: "vision-model",
			Source:    "remote",
			Provider:  "openai",
		},
	}
}

func TestArtifactCachedVLMUsesExactImagesAndPrompt(t *testing.T) {
	provider := &countingVLM{response: "caption"}
	cached := NewArtifactCachedVLM(provider, setupVLMArtifactRuntime(t), vlmArtifactConfig())
	ctx := WithArtifactStage(context.Background(), ArtifactStage{
		Stage:        "vlm_caption",
		OutputSchema: "vlm-caption.text.v1",
	})

	first, err := cached.Predict(ctx, [][]byte{[]byte{0, 1, 2}}, " describe ")
	require.NoError(t, err)
	second, err := cached.Predict(ctx, [][]byte{[]byte{0, 1, 2}}, " describe ")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, provider.callCount())

	_, err = cached.Predict(ctx, [][]byte{[]byte{0, 1, 3}}, " describe ")
	require.NoError(t, err)
	provider.mu.Lock()
	provider.response = "changed caption"
	provider.mu.Unlock()
	changed, err := cached.Predict(ctx, [][]byte{[]byte{0, 1, 2}}, "describe")
	require.NoError(t, err)
	assert.Equal(t, "changed caption", changed)
	assert.Equal(t, 3, provider.callCount())
}

func TestVLMArtifactConfigExcludesCredentialRotation(t *testing.T) {
	model := &types.Model{
		ID:     "vision-id",
		Name:   "vision-model",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			BaseURL:        "https://example.com/v1",
			APIKey:         "secret-one",
			Provider:       "openai",
			SupportsVision: true,
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
