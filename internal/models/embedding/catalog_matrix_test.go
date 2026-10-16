package embedding

import (
	"context"
	"net/url"
	"strings"
	"testing"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Exercise the production factory and HTTP transport for every embedding rule,
// including aliases, pattern entries and both query/document task modes.
func TestEveryCatalogEmbeddingTransport(t *testing.T) {
	for _, v := range modelruntime.List() {
		for _, m := range v.Models() {
			if m.Type != types.ModelTypeEmbedding {
				continue
			}
			names := append([]string{m.ID}, m.Aliases...)
			if m.ID == "" {
				names[0] = strings.ReplaceAll(m.Match, "*", "snapshot")
			}
			for _, name := range names {
				t.Run(v.ID+"/"+name, func(t *testing.T) {
					u := newUpstream(t)
					base, err := url.Parse(
						strings.ReplaceAll(
							v.GetDefaultURL(
								types.ModelTypeEmbedding,
							),
							"{resource}",
							"test-resource",
						),
					)
					require.NoError(t, err)
					cfg := Config{
						Source:                    types.ModelSourceRemote,
						Provider:                  v.ID,
						ModelName:                 name,
						ModelID:                   "row-id",
						BaseURL:                   u.url + base.Path,
						APIKey:                    "row-key",
						AppID:                     "app",
						AppSecret:                 "secret",
						Dimensions:                256,
						SupportsDimensionOverride: true,
					}
					client, err := newRemoteEmbedder(cfg, nil)
					require.NoError(t, err)
					for _, ctx := range []context.Context{
						context.Background(),
						types.WithEmbedQuery(
							context.Background(),
						),
					} {
						vectors, err := client.BatchEmbed(ctx, []string{"a", "中文"})
						require.NoError(t, err)
						require.Equal(t, [][]float32{{1}, {2}}, vectors)
					}
					require.NotEmpty(t, u.requests)
					require.Equal(t, "row-id", client.GetModelID())
				})
			}
		}
	}
}

func TestStoredEmbeddingSpecReachesWire(t *testing.T) {
	u := newUpstream(t)
	row := &types.Model{
		ID:     "stable-id",
		Name:   "custom",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "generic",
			BaseURL:  u.url,
			APIKey:   "model-key",
			Spec: &types.ModelSpecOverride{
				Compat: map[string]any{
					"path":           "/custom-embeddings",
					"max_batch_size": 1,
					"extra_body": map[string]any{
						"custom_flag": true,
					},
				},
			},
		},
	}
	client, err := newRemoteEmbedder(ConfigFromModel(row, "", ""), nil)
	require.NoError(t, err)
	_, err = client.BatchEmbed(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
	require.Len(t, u.requests, 2)
	for _, req := range u.requests {
		require.Equal(t, "/custom-embeddings", req.path)
		require.Equal(t, true, req.body["custom_flag"])
		require.Equal(t, "Bearer model-key", req.header.Get("Authorization"))
	}
	row.Parameters.Spec.Compat = map[string]any{"misspelled_option": true}
	_, err = newRemoteEmbedder(ConfigFromModel(row, "", ""), nil)
	require.ErrorContains(t, err, "unknown field")
}
