package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOllamaEmbeddingKeepAlive(t *testing.T) {
	for _, tt := range []struct {
		name  string
		value string
		want  any
	}{
		{"default_preserves_server_policy", "", nil},
		{"finite_residency", "30m", "30m0s"},
		{"explicit_unload", "0", "0s"},
		{"explicit_indefinite", "-1", float64(-1)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/":
					w.WriteHeader(http.StatusOK)
				case "/api/tags":
					_, _ = w.Write([]byte(`{"models":[{"name":"bge-m3:latest"}]}`))
				case "/api/embed":
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					requests <- body
					_, _ = w.Write([]byte(`{"model":"bge-m3:latest","embeddings":[[0.1,0.2]]}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			t.Setenv("OLLAMA_BASE_URL", server.URL)
			svc, err := ollama.GetOllamaService()
			require.NoError(t, err)
			e, err := NewEmbedder(Config{
				Source: types.ModelSourceLocal, ModelName: "bge-m3:latest",
				Dimensions: 2, SupportsDimensionOverride: true, TruncatePromptTokens: 511,
				ExtraConfig: map[string]string{"keep_alive": tt.value},
			}, nil, svc)
			require.NoError(t, err)
			vectors, err := e.BatchEmbed(context.Background(), []string{"synthetic text"})
			require.NoError(t, err)
			require.Len(t, vectors, 1)
			require.Len(t, vectors[0], 2)
			body := <-requests
			assert.Equal(t, tt.want, body["keep_alive"])
			if tt.want == nil {
				assert.NotContains(t, body, "keep_alive")
			}
			assert.Equal(t, "bge-m3:latest", body["model"])
			assert.Equal(t, []any{"synthetic text"}, body["input"])
			assert.Equal(t, float64(2), body["dimensions"])
			assert.Equal(t, true, body["truncate"])
			assert.Equal(t, float64(511), body["options"].(map[string]any)["num_ctx"])
		})
	}
}

func TestOllamaEmbeddingRejectsInvalidKeepAlive(t *testing.T) {
	for _, value := range []string{"forever", "30", "-2m", "999999999999999999999h"} {
		t.Run(value, func(t *testing.T) {
			_, err := NewEmbedder(Config{
				Source: types.ModelSourceLocal, ModelName: "bge-m3:latest",
				ExtraConfig: map[string]string{"keep_alive": value},
			}, nil, nil)
			require.ErrorContains(t, err, "keep_alive")
		})
	}
}
