package embedding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type weKnoraCloudRoundTripper func(*http.Request) (*http.Response, error)

func (f weKnoraCloudRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newWeKnoraCloudEmbedderTestServer(t *testing.T, response string) *WeKnoraCloudEmbedder {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != weKnoraCloudEmbedPath {
			http.Error(w, fmt.Sprintf("request path = %q, want %q", r.URL.Path, weKnoraCloudEmbedPath), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, response)
	}))
	t.Cleanup(server.Close)

	return &WeKnoraCloudEmbedder{
		modelName: "test-embedding",
		modelID:   "test-model-id",
		appID:     "test-app-id",
		apiKey:    "test-api-key",
		baseURL:   server.URL,
		client:    server.Client(),
	}
}

func TestWeKnoraCloudBatchEmbedPreservesInputOrder(t *testing.T) {
	embedder := newWeKnoraCloudEmbedderTestServer(t, `{
		"data": [
			{"index": 1, "embedding": [0.3, 0.4]},
			{"index": 0, "embedding": [0.1, 0.2]}
		]
	}`)

	got, err := embedder.BatchEmbed(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}
	want := [][]float32{{0.1, 0.2}, {0.3, 0.4}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BatchEmbed result = %v, want %v", got, want)
	}
}

func TestWeKnoraCloudBatchEmbedRejectsMalformedResponse(t *testing.T) {
	tests := []struct {
		name       string
		response   string
		texts      []string
		wantErrMsg string
	}{
		{
			name:       "negative index",
			response:   `{"data": [{"index": -1, "embedding": [0.1, 0.2]}]}`,
			texts:      []string{"first"},
			wantErrMsg: "response index -1 out of range",
		},
		{
			name:       "index above input range",
			response:   `{"data": [{"index": 1, "embedding": [0.1, 0.2]}]}`,
			texts:      []string{"first"},
			wantErrMsg: "response index 1 out of range",
		},
		{
			name:       "duplicate index",
			response:   `{"data": [{"index": 0, "embedding": [0.1]}, {"index": 0, "embedding": [0.2]}]}`,
			texts:      []string{"first"},
			wantErrMsg: "duplicate response index 0",
		},
		{
			name:       "missing result",
			response:   `{"data": [{"index": 0, "embedding": [0.1, 0.2]}]}`,
			texts:      []string{"first", "second"},
			wantErrMsg: "missing embedding for input index 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			embedder := newWeKnoraCloudEmbedderTestServer(t, tt.response)

			_, err := embedder.BatchEmbed(context.Background(), tt.texts)
			if err == nil {
				t.Fatalf("BatchEmbed returned nil error, want error containing %q", tt.wantErrMsg)
			}
			if !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Fatalf("BatchEmbed error = %q, want it to contain %q", err, tt.wantErrMsg)
			}
		})
	}
}

func TestWeKnoraCloudBatchEmbedRetriesTransportErrors(t *testing.T) {
	attempts := 0
	embedder := &WeKnoraCloudEmbedder{
		modelName:  "test-embedding",
		appID:      "test-app-id",
		apiKey:     "test-api-key",
		baseURL:    "https://cloud.example.test",
		maxRetries: 1,
		client: &http.Client{Transport: weKnoraCloudRoundTripper(func(req *http.Request) (*http.Response, error) {
			attempts++
			if attempts < 2 {
				return nil, errors.New("temporary network failure")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[{"index":0,"embedding":[0.1,0.2]}]}`)),
				Request:    req,
			}, nil
		})},
	}

	got, err := embedder.BatchEmbed(context.Background(), []string{"first"})
	if err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("request attempts = %d, want 2", attempts)
	}
	if !reflect.DeepEqual(got, [][]float32{{0.1, 0.2}}) {
		t.Fatalf("BatchEmbed result = %v, want [[0.1 0.2]]", got)
	}
}
