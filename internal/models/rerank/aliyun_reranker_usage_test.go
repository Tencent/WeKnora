package rerank

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/usage"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestAliyunRerankerUsageParsing(t *testing.T) {
	tests := []struct {
		name           string
		usageJSON      string
		wantInput      *int
		wantTotal      *int
		wantProvenance types.TokenProvenance
	}{
		{
			name:           "total only does not infer input",
			usageJSON:      `{"total_tokens":27}`,
			wantTotal:      ptr(27),
			wantProvenance: types.TokenProvenanceProviderReported,
		},
		{
			name:           "prompt and total",
			usageJSON:      `{"prompt_tokens":19,"total_tokens":27}`,
			wantInput:      ptr(19),
			wantTotal:      ptr(27),
			wantProvenance: types.TokenProvenanceProviderReported,
		},
		{
			name:           "usage absent",
			usageJSON:      `{}`,
			wantProvenance: types.TokenProvenanceUnreported,
		},
		{
			name:           "reported zero",
			usageJSON:      `{"prompt_tokens":0,"total_tokens":0}`,
			wantInput:      ptr(0),
			wantTotal:      ptr(0),
			wantProvenance: types.TokenProvenanceProviderReported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SSRF_WHITELIST", "127.0.0.1")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprintf(
					w,
					`{"output":{"results":[{"index":0,"relevance_score":0.9,`+
						`"document":{"text":"d"}}]},"usage":%s}`,
					test.usageJSON,
				)
			}))
			defer server.Close()

			config := testRerankConfig()
			config.BaseURL = server.URL
			config.Provider = "aliyun"
			reranker, err := NewAliyunReranker(&config)
			require.NoError(t, err)

			repo := &fakeUsageRepo{}
			usage.SetRecorder(usage.NewRecorder(repo))
			defer usage.SetRecorder(nil)
			wrapped, err := wrapRerankUsage(reranker, &config, nil, nil)
			require.NoError(t, err)
			_, err = wrapped.Rerank(tenantCtx(1), "q", []string{"d"})
			require.NoError(t, err)

			u := repo.last()
			require.NotNil(t, u)
			requireOptionalRerankInt(t, test.wantInput, u.InputTokens)
			requireOptionalRerankInt(t, test.wantTotal, u.TotalTokens)
			require.Nil(t, u.OutputTokens)
			require.Equal(t, test.wantProvenance, u.TokenProvenance)
		})
	}
}

func requireOptionalRerankInt(t *testing.T, want, got *int) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Equal(t, *want, *got)
}
