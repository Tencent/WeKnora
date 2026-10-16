package runtime_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/providers"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestExplicitChatProtocolSurvivesVendorPreference(t *testing.T) {
	for _, protocol := range []api.API{api.APIOpenAICompletions, api.APIOpenAIResponses} {
		r, err := modelruntime.Resolve(
			modelruntime.Ref{
				Provider: "openai",
				Model:    "gpt-5",
				BaseURL:  "https://api.openai.com/v1",
				Override: &types.ModelSpecOverride{
					API: string(
						protocol,
					),
				},
			},
		)
		require.NoError(t, err)
		require.Equal(t, protocol, r.API)
	}
}

func TestProtocolOverrideDoesNotDecodeCatalogCompatAsAnotherProtocol(t *testing.T) {
	r, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "aliyun", Model: "qwen-plus",
		Override: &types.ModelSpecOverride{
			API:    string(api.APIAnthropicMessages),
			Compat: map[string]any{"thinking_mode": "adaptive"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, api.APIAnthropicMessages, r.API)
	require.Equal(t, api.AnthropicThinkingAdaptive, r.AnthropicMessages.ThinkingMode)
}

func TestEveryProviderKeepsModelCredentialsIsolated(t *testing.T) {
	for _, v := range modelruntime.List() {
		t.Run(v.ID, func(t *testing.T) {
			r, err := modelruntime.Resolve(
				modelruntime.Ref{
					Provider: v.ID,
					Model:    "test",
					BaseURL:  "https://example.com/v1",
				},
			)
			require.NoError(t, err)
			for _, key := range []string{"first-model-key", "second-model-key", "first-model-key"} {
				ep, err := r.Endpoint(
					types.ModelTypeKnowledgeQA,
					modelruntime.Connection{
						ModelID: key,
						Credentials: api.Credentials{
							APIKey:    key,
							AppID:     key,
							AppSecret: key,
						},
						Headers: map[string]string{
							"X-Model": key,
						},
					},
				)
				require.NoError(t, err)
				req, err := http.NewRequestWithContext(context.Background(), "POST", "https://example.com/v1", nil)
				require.NoError(t, err)
				ep.Auth(req, []byte(`{}`))
				switch v.AuthStyleFor(r.API) {
				case providers.AuthBearer:
					require.Equal(t, "Bearer "+key, req.Header.Get("Authorization"))
				case providers.AuthAPIKeyHeader:
					require.Equal(t, key, req.Header.Get("api-key"))
				case providers.AuthXAPIKey:
					require.Equal(t, key, req.Header.Get("x-api-key"))
				case providers.AuthGoogleAPIKey:
					require.Equal(t, key, req.Header.Get("x-goog-api-key"))
				case providers.AuthSigned:
					require.Equal(t, key, req.Header.Get("X-APPID"))
				}
				require.Equal(t, key, ep.ModelID)
				require.Equal(t, key, ep.Headers["X-Model"])
			}
		})
	}
}

func TestOverlayReloadRestoresBaselineAndRollsBackFailure(t *testing.T) {
	before := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(before) })
	original, _ := modelruntime.Get("openai")
	require.NoError(t, modelruntime.ApplyOverlay([]byte(`{
  "providers": {
    "openai": {
      "base_url": "https://relay.example/v1",
      "models": [
        {
          "id": "gpt-5",
          "cost": {
            "input": 999
          }
        }
      ]
    },
    "temporary": {
      "name": "Temporary"
    }
  }
}`), ""))
	changed, _ := modelruntime.Get("openai")
	require.Equal(t, "https://relay.example/v1", changed.GetDefaultURL(types.ModelTypeKnowledgeQA))
	require.Error(
		t,
		modelruntime.ApplyOverlay(
			[]byte(
				`{"providers":{"openai":{"base_url":"https://wrong.example"},"broken":{"api":"typo"}}}`,
			),
			"",
		),
	)
	current, _ := modelruntime.Get("openai")
	require.Equal(t, changed.DefaultBaseURLs, current.DefaultBaseURLs)
	require.Equal(t, changed.Models(), current.Models())
	require.Error(t, modelruntime.ApplyOverlay([]byte(`{"providers":{}} {"providers":{}}`), ""))
	require.NoError(t, modelruntime.ApplyOverlay([]byte(`{"providers":{}}`), ""))
	restored, _ := modelruntime.Get("openai")
	require.Equal(t, original.DefaultBaseURLs, restored.DefaultBaseURLs)
	require.Equal(t, original.Models(), restored.Models())
	_, exists := modelruntime.Get("temporary")
	require.False(t, exists)
}

func TestConcurrentOverlayAndModelResolution(t *testing.T) {
	before := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(before) })
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 30; j++ {
				r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
				if err != nil || r.RemoteModel != "gpt-5" {
					t.Errorf("resolve during reload: %v", err)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		require.NoError(
			t,
			modelruntime.ApplyOverlay(
				[]byte(
					`{"providers":{"openai":{"headers":{"X-Deployment":"one"}}}}`,
				),
				"",
			),
		)
	}
	wg.Wait()
}

func TestEveryProviderDefinitionCanBeReloaded(t *testing.T) {
	before := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(before) })
	entries := map[string]any{}
	for _, v := range modelruntime.List() {
		entries[v.ID] = map[string]any{}
	}
	data, err := json.Marshal(map[string]any{"providers": entries})
	require.NoError(t, err)
	require.NoError(t, modelruntime.Reload(data, ""))
	require.ErrorContains(
		t,
		modelruntime.Reload(
			[]byte(
				`{"providers":{"openai":{"models":[{"id":"gpt-5","compat":{"typo":true}}]}}}`,
			),
			"",
		),
		"unknown field",
	)
	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "openai", Model: "gpt-5"})
	require.NoError(t, err)
	require.Equal(t, "gpt-5", r.RemoteModel)
}

func TestOverlayCanDeclareSameModelNameForDifferentCapabilities(t *testing.T) {
	before := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(before) })
	require.NoError(t, modelruntime.Reload([]byte(`{
  "providers": {
    "generic": {
      "models": [
        {
          "id": "shared-name",
          "type": "KnowledgeQA",
          "context_window": 64000
        },
        {
          "id": "shared-name",
          "type": "Embedding",
          "dimension": 768,
          "compat": {
            "dimensions_field": "dimensions"
          }
        }
      ]
    }
  }
}`), ""))
	chat, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "generic", Model: "shared-name", ModelType: types.ModelTypeKnowledgeQA,
	})
	require.NoError(t, err)
	require.Equal(t, 64000, chat.Spec.ContextWindow)
	embed, err := modelruntime.Resolve(modelruntime.Ref{
		Provider: "generic", Model: "shared-name", ModelType: types.ModelTypeEmbedding,
	})
	require.NoError(t, err)
	require.Equal(t, 768, embed.Spec.Dimension)
	require.Equal(t, "dimensions", embed.Embeddings.DimensionsField)
}
