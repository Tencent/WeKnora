package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestAzureOpenAIEmbedderBatchEmbedSendsConfiguredDimensions(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST request, got %s", r.Method)
		}

		if got, want := r.URL.String(),
			"https://example-resource.openai.azure.com/openai/deployments/text-embedding-3-large-deployment/embeddings?api-version=2024-10-21"; got != want {
			t.Fatalf("unexpected request path: got %s want %s", got, want)
		}

		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewAzureOpenAIEmbedder(
		"test-key",
		"https://example-resource.openai.azure.com",
		"text-embedding-3-large-deployment",
		511,
		256,
		"text-embedding-3-large",
		"2024-10-21",
		nil,
	)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	embedder.SetSupportsDimensionOverride(true)
	embedder.httpClient = &http.Client{Transport: transport}

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}

	got, ok := requestBody["dimensions"]
	if !ok {
		t.Fatalf("expected request body to include dimensions, got %v", requestBody)
	}

	if got != float64(256) {
		t.Fatalf("unexpected dimensions value: got %v want 256", got)
	}
}

func TestAzureOpenAIEmbedderBatchEmbedOmitsDimensionsByDefault(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewAzureOpenAIEmbedder(
		"test-key",
		"https://example-resource.openai.azure.com",
		"ada-002-deployment",
		511,
		1536,
		"text-embedding-ada-002",
		"2024-10-21",
		nil,
	)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	embedder.httpClient = &http.Client{Transport: transport}

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}

	if _, ok := requestBody["dimensions"]; ok {
		t.Fatalf("expected request body to omit dimensions for fixed-size model, got %v", requestBody)
	}
}

func TestAzureOpenAIEmbedderBatchEmbedSendsDimensionsWhenOverrideEnabledRegardlessOfAPIVersion(t *testing.T) {
	t.Parallel()

	var requestBody map[string]any
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(bytes.NewBufferString(`{"data":[{"embedding":[0.1,0.2],"index":0}]}`)),
		}, nil
	})

	embedder, err := NewAzureOpenAIEmbedder(
		"test-key",
		"https://example-resource.openai.azure.com",
		"text-embedding-3-large-deployment",
		511,
		256,
		"text-embedding-3-large",
		"2024-02-15-preview",
		nil,
	)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}
	embedder.SetSupportsDimensionOverride(true)
	embedder.httpClient = &http.Client{Transport: transport}

	if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
		t.Fatalf("BatchEmbed returned error: %v", err)
	}

	got, ok := requestBody["dimensions"]
	if !ok {
		t.Fatalf("expected request body to include dimensions when override is enabled, got %v", requestBody)
	}
	if got != float64(256) {
		t.Fatalf("unexpected dimensions value: got %v want 256", got)
	}
}

// TestAzureOpenAIEmbedderURLFollowsAPIVersionRule pins the one rule a stored
// Azure row picks its data plane by, the same rule the catalog's Endpoint
// hook applies to that row's chat and VLM clients: an empty api_version means
// the v1 GA path with the deployment name in the body, a non-empty one means
// the dated deployments path. Before the catalog, embedding hard-defaulted to
// 2024-10-21 here and put one row on two different data planes.
func TestAzureOpenAIEmbedderURLFollowsAPIVersionRule(t *testing.T) {
	t.Parallel()

	const (
		baseURL    = "https://example-resource.openai.azure.com"
		deployment = "text-embedding-3-large-deployment"
	)

	for _, tc := range []struct {
		name       string
		apiVersion string
		wantURL    string
	}{
		{
			name:       "row without api_version uses the v1 GA data plane",
			apiVersion: "",
			wantURL:    baseURL + "/openai/v1/embeddings",
		},
		{
			name:       "row with the legacy GA api_version keeps the deployments path",
			apiVersion: "2024-10-21",
			wantURL: baseURL + "/openai/deployments/" + deployment +
				"/embeddings?api-version=2024-10-21",
		},
		{
			name:       "row with a preview api_version keeps the deployments path",
			apiVersion: "2025-04-01-preview",
			wantURL: baseURL + "/openai/deployments/" + deployment +
				"/embeddings?api-version=2025-04-01-preview",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotURL string
			var requestBody map[string]any
			transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
				gotURL = r.URL.String()
				if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body: io.NopCloser(bytes.NewBufferString(
						`{"data":[{"embedding":[0.1,0.2],"index":0}]}`)),
				}, nil
			})

			embedder, err := NewAzureOpenAIEmbedder("test-key", baseURL, deployment,
				511, 256, "text-embedding-3-large", tc.apiVersion, nil)
			if err != nil {
				t.Fatalf("create embedder: %v", err)
			}
			embedder.httpClient = &http.Client{Transport: transport}

			if _, err := embedder.BatchEmbed(context.Background(), []string{"hello"}); err != nil {
				t.Fatalf("BatchEmbed returned error: %v", err)
			}
			if gotURL != tc.wantURL {
				t.Fatalf("request URL = %q, want %q", gotURL, tc.wantURL)
			}
			// Both shapes carry the deployment name in the body; the v1
			// path has nowhere else to put it.
			if requestBody["model"] != deployment {
				t.Fatalf("request body model = %v, want %q", requestBody["model"], deployment)
			}
		})
	}
}

// TestAzureEmbeddingURLMatchesVendorHook pins that the embedding URL is the
// vendor hook's answer and not a second, parallel implementation of the rule.
func TestAzureEmbeddingURLMatchesVendorHook(t *testing.T) {
	t.Parallel()

	vendor, ok := catalog.Get("azure_openai")
	if !ok {
		t.Fatal("azure_openai vendor is not registered")
	}
	for _, version := range []string{"", "2024-10-21"} {
		extra := map[string]string{}
		if version != "" {
			extra[catalog.ExtraAPIVersion] = version
		}
		target, query := vendor.Endpoint(catalog.EndpointRequest{
			BaseURL:   "https://example-resource.openai.azure.com",
			Model:     "embed deploy",
			ModelType: types.ModelTypeEmbedding,
			API:       api.APIOpenAICompletions,
			Extra:     extra,
		})
		want := api.Endpoint{URL: target, Query: query}.Resolve("")
		got := azureEmbeddingURL("https://example-resource.openai.azure.com/", "embed deploy", version)
		if got != want {
			t.Fatalf("azureEmbeddingURL(api_version=%q) = %q, want the hook's %q", version, got, want)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
