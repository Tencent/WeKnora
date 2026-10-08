package rerank

import (
	"context"
	"net/url"
	"strings"
	"testing"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEveryCatalogRerankTransport(t *testing.T) {
	for _, v := range modelruntime.List() {
		for _, m := range v.Models() {
			if m.Type != types.ModelTypeRerank {
				continue
			}
			names := append([]string{m.ID}, m.Aliases...)
			if m.ID == "" {
				names[0] = strings.ReplaceAll(m.Match, "*", "snapshot")
			}
			for _, name := range names {
				t.Run(v.ID+"/"+name, func(t *testing.T) {
					u := newUpstream(t)
					base, err := url.Parse(v.GetDefaultURL(types.ModelTypeRerank))
					require.NoError(t, err)
					cfg := &RerankerConfig{
						Source:    types.ModelSourceRemote,
						Provider:  v.ID,
						ModelName: name,
						ModelID:   "row-id",
						BaseURL:   u.url + base.Path,
						APIKey:    "row-key",
						AppID:     "app",
						AppSecret: "secret",
					}
					client, err := newReranker(cfg)
					if strings.Contains(string(m.Compat), "unsupported_reason") {
						require.ErrorContains(t, err, "does not serve")
						require.Empty(t, u.requests)
						return
					}
					require.NoError(t, err)
					result, err := client.Rerank(context.Background(), "q", []string{"a", "中文"})
					require.NoError(t, err)
					require.Len(t, result, 2)
					require.NotEmpty(t, u.requests)
				})
			}
		}
	}
}

func TestStoredRerankSpecReachesWire(t *testing.T) {
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
					"path":       "/custom-rerank",
					"send_top_n": true,
					"extra_body": map[string]any{
						"custom_flag": true,
					},
				},
			},
		},
	}
	client, err := newReranker(ConfigFromModel(row, "", ""))
	require.NoError(t, err)
	_, err = client.Rerank(context.Background(), "q", []string{"a", "bb"})
	require.NoError(t, err)
	require.Len(t, u.requests, 1)
	req := u.requests[0]
	require.Equal(t, "/custom-rerank", req.path)
	require.Equal(t, true, req.body["custom_flag"])
	require.Equal(t, float64(2), req.body["top_n"])
	row.Parameters.Spec.Compat = map[string]any{"misspelled_option": true}
	_, err = newReranker(ConfigFromModel(row, "", ""))
	require.ErrorContains(t, err, "unknown field")
}
