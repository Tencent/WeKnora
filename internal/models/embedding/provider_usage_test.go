package embedding

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/usage"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNativeEmbeddingUsageFieldsDistinguishAbsentAndZero(t *testing.T) {
	var aliyunAbsent AliyunEmbedResponse
	require.NoError(t, json.Unmarshal([]byte(`{"usage":{}}`), &aliyunAbsent))
	require.Nil(t, aliyunAbsent.Usage.TotalTokens)

	var aliyunZero AliyunEmbedResponse
	require.NoError(t, json.Unmarshal([]byte(`{"usage":{"total_tokens":0}}`), &aliyunZero))
	require.NotNil(t, aliyunZero.Usage.TotalTokens)
	require.Zero(t, *aliyunZero.Usage.TotalTokens)

	var volcengineAbsent VolcengineEmbedResponse
	require.NoError(t, json.Unmarshal([]byte(`{"usage":{}}`), &volcengineAbsent))
	require.Nil(t, volcengineAbsent.Usage.PromptTokens)
	require.Nil(t, volcengineAbsent.Usage.TotalTokens)

	var volcengineZero VolcengineEmbedResponse
	require.NoError(t, json.Unmarshal(
		[]byte(`{"usage":{"prompt_tokens":0,"total_tokens":0}}`), &volcengineZero,
	))
	require.NotNil(t, volcengineZero.Usage.PromptTokens)
	require.NotNil(t, volcengineZero.Usage.TotalTokens)
	require.Zero(t, *volcengineZero.Usage.PromptTokens)
	require.Zero(t, *volcengineZero.Usage.TotalTokens)
}

func TestOpenAICompatibleEmbeddingUsageParsing(t *testing.T) {
	tests := []struct {
		name           string
		usageJSON      string
		wantInput      *int
		wantTotal      *int
		wantProvenance types.TokenProvenance
	}{
		{
			name:           "total only",
			usageJSON:      `,"usage":{"total_tokens":27}`,
			wantTotal:      intPtr(27),
			wantProvenance: types.TokenProvenanceProviderReported,
		},
		{
			name:           "prompt and total",
			usageJSON:      `,"usage":{"prompt_tokens":184,"total_tokens":184}`,
			wantInput:      intPtr(184),
			wantTotal:      intPtr(184),
			wantProvenance: types.TokenProvenanceProviderReported,
		},
		{
			name:           "usage absent",
			wantProvenance: types.TokenProvenanceUnreported,
		},
		{
			name:           "reported zero",
			usageJSON:      `,"usage":{"prompt_tokens":0,"total_tokens":0}`,
			wantInput:      intPtr(0),
			wantTotal:      intPtr(0),
			wantProvenance: types.TokenProvenanceProviderReported,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := runEmbeddingUsageFixture(t, test.usageJSON, func(baseURL string) (Embedder, error) {
				return NewOpenAIEmbedder("test-key", baseURL, "text-embedding-v4", 511, 2, "model-id", nil)
			})
			requireOptionalInt(t, test.wantInput, u.InputTokens)
			requireOptionalInt(t, test.wantTotal, u.TotalTokens)
			require.Nil(t, u.OutputTokens)
			require.Equal(t, test.wantProvenance, u.TokenProvenance)
		})
	}
}

func TestAzureAndZhipuEmbeddingUsageParsing(t *testing.T) {
	tests := []struct {
		name    string
		factory func(string) (Embedder, error)
	}{
		{
			name: "azure openai",
			factory: func(baseURL string) (Embedder, error) {
				return NewAzureOpenAIEmbedder(
					"test-key", baseURL, "deployment", 511, 2, "model-id", "2024-10-21", nil,
				)
			},
		},
		{
			name: "zhipu",
			factory: func(baseURL string) (Embedder, error) {
				return NewZhipuEmbedder("test-key", baseURL, "embedding-3", 511, 2, "model-id", nil)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			u := runEmbeddingUsageFixture(
				t, `,"usage":{"prompt_tokens":31,"total_tokens":31}`, test.factory,
			)
			requireOptionalInt(t, intPtr(31), u.InputTokens)
			requireOptionalInt(t, intPtr(31), u.TotalTokens)
			require.Equal(t, types.TokenProvenanceProviderReported, u.TokenProvenance)
		})
	}
}

func runEmbeddingUsageFixture(
	t *testing.T,
	usageJSON string,
	factory func(string) (Embedder, error),
) *types.ModelUsage {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":[{"embedding":[0.1,0.2],"index":0}]%s}`, usageJSON)
	}))
	t.Cleanup(server.Close)

	embedder, err := factory(server.URL)
	require.NoError(t, err)

	repo := &fakeUsageRepo{}
	usage.SetRecorder(usage.NewRecorder(repo))
	t.Cleanup(func() { usage.SetRecorder(nil) })

	config := testEmbeddingConfig()
	config.BaseURL = server.URL
	w := wrapEmbeddingUsage(embedder, config, nil)
	_, err = w.BatchEmbed(tenantCtx(1), []string{"hello"})
	require.NoError(t, err)
	require.NotNil(t, repo.last())
	return repo.last()
}

func requireOptionalInt(t *testing.T, want, got *int) {
	t.Helper()
	if want == nil {
		require.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	require.Equal(t, *want, *got)
}
