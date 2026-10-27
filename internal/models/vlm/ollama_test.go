package vlm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/stretchr/testify/require"
)

func TestOllamaPredictThinkingRequest(t *testing.T) {
	on, off := true, false
	for _, tc := range []struct {
		name string
		opts *PredictOptions
		want *bool
	}{
		{"legacy", nil, nil},
		{"unset", &PredictOptions{}, nil},
		{"enabled", &PredictOptions{Thinking: &on}, &on},
		{"disabled", &PredictOptions{Thinking: &off}, &off},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan map[string]any, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusOK)
					return
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"caption"},"done":true}`))
			}))
			defer server.Close()
			t.Setenv("OLLAMA_BASE_URL", server.URL)
			service, err := ollama.GetOllamaService()
			require.NoError(t, err)
			model, err := NewOllamaVLM(&Config{ModelName: "test"}, service)
			require.NoError(t, err)
			var result string
			if tc.opts == nil {
				result, err = model.Predict(context.Background(), nil, "describe")
			} else {
				result, err = model.PredictWithOptions(context.Background(), nil, "describe", tc.opts)
			}
			require.NoError(t, err)
			require.Equal(t, "caption", result)
			body := <-requests
			if tc.want == nil {
				require.NotContains(t, body, "think")
			} else {
				require.Equal(t, *tc.want, body["think"])
			}
			options := body["options"].(map[string]any)
			require.NotContains(t, options, "think")
			require.Equal(t, 0.1, options["temperature"])
		})
	}
}
