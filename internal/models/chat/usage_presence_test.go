package chat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/sashabaranov/go-openai"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageFieldPresent(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"absent", `{"choices":[{"message":{"content":"hi"}}]}`, false},
		{"explicit zero", `{"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`, true},
		{"normal", `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, true},
		{"explicit null", `{"choices":[],"usage":null}`, false},
		{"empty body", "", false},
		{"malformed", `{not json`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, usageFieldPresent([]byte(tt.body)))
		})
	}
}

// TestOpenAIUsagePresenceSemantics drives the full non-stream chain from raw
// JSON through tokenUsageFromOpenAI and applyChatUsage, asserting the frozen
// semantics: an absent usage block is unreported with NULL counters, while an
// explicit all-zero usage block is provider_reported with non-nil zeros.
func TestOpenAIUsagePresenceSemantics(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		wantProvenance types.TokenProvenance
		wantInput      *int
		wantOutput     *int
		wantTotal      *int
	}{
		{
			name:           "usage absent",
			body:           `{"choices":[{"message":{"content":"hi"}}]}`,
			wantProvenance: types.TokenProvenanceUnreported,
			wantInput:      nil,
			wantOutput:     nil,
			wantTotal:      nil,
		},
		{
			name: "usage explicit zero",
			body: `{"choices":[{"message":{"content":"hi"}}],` +
				`"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			wantProvenance: types.TokenProvenanceProviderReported,
			wantInput:      intPtr(0),
			wantOutput:     intPtr(0),
			wantTotal:      intPtr(0),
		},
		{
			name: "normal usage",
			body: `{"choices":[{"message":{"content":"hi"}}],` +
				`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
			wantProvenance: types.TokenProvenanceProviderReported,
			wantInput:      intPtr(10),
			wantOutput:     intPtr(5),
			wantTotal:      intPtr(15),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp openai.ChatCompletionResponse
			require.NoError(t, json.Unmarshal([]byte(tt.body), &resp))

			reported := usageFieldPresent([]byte(tt.body))
			tu := tokenUsageFromOpenAI(resp.Usage, provider.ProviderOpenAI, reported)
			mu := &types.ModelUsage{}
			applyChatUsage(mu, &tu)

			assert.Equal(t, tt.wantProvenance, mu.TokenProvenance)
			assertTokenPtr(t, tt.wantInput, mu.InputTokens)
			assertTokenPtr(t, tt.wantOutput, mu.OutputTokens)
			assertTokenPtr(t, tt.wantTotal, mu.TotalTokens)
		})
	}
}

func assertTokenPtr(t *testing.T, want, got *int) {
	t.Helper()
	if want == nil {
		assert.Nil(t, got)
		return
	}
	require.NotNil(t, got)
	assert.Equal(t, *want, *got)
}

func TestResponseBodyCaptureRoundTripper(t *testing.T) {
	const body = `{"choices":[{"message":{"content":"hi"}}],"usage":{"total_tokens":7}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: &responseBodyCaptureRoundTripper{next: http.DefaultTransport},
	}

	t.Run("captures body when slot present", func(t *testing.T) {
		ctx, slot := withResponseBodyCapture(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(got), "replaced body must still be fully readable")
		assert.Equal(t, body, string(slot.body), "slot must hold the raw response body")
	})

	t.Run("passes through without slot", func(t *testing.T) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
		require.NoError(t, err)

		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		got, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, body, string(got))
	})
}
