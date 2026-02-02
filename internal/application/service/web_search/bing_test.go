package web_search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setBingEnv(apiKey string) {
	os.Setenv("BING_SEARCH_API_KEY", apiKey)
}

func unsetBingEnv() {
	os.Unsetenv("BING_SEARCH_API_KEY")
}

func TestNewBingProvider(t *testing.T) {
	setBingEnv("test-api-key")
	defer unsetBingEnv()

	provider, err := NewBingProvider()
	require.NoError(t, err)
	assert.NotNil(t, provider)
}

func TestBingProvider_Search(t *testing.T) {
	mockResponse := map[string]interface{}{
		"_type": "SearchResponse",
		"webPages": map[string]interface{}{
			"webSearchUrl":          "https://www.bing.com/search?q=test",
			"totalEstimatedMatches": 1000,
			"value": []map[string]interface{}{
				{
					"id":               "result-1",
					"name":             "Test Result 1",
					"url":              "https://example.com/1",
					"isFamilyFriendly": true,
					"displayUrl":       "example.com/1",
					"snippet":          "This is a test snippet 1",
					"dateLastCrawled":  time.Now().Format(time.RFC3339),
				},
				{
					"id":               "result-2",
					"name":             "Test Result 2",
					"url":              "https://example.com/2",
					"isFamilyFriendly": true,
					"displayUrl":       "example.com/2",
					"snippet":          "This is a test snippet 2",
					"dateLastCrawled":  time.Now().Format(time.RFC3339),
				},
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		if r.Header.Get("Ocp-Apim-Subscription-Key") != "test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		query := r.URL.Query().Get("q")
		if query == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	provider := &BingProvider{
		client:  server.Client(),
		baseURL: server.URL,
		apiKey:  "test-api-key",
	}

	t.Run("Successful search", func(t *testing.T) {
		ctx := context.Background()
		results, err := provider.Search(ctx, "test query", 10, true)
		require.NoError(t, err)
		assert.Len(t, results, 2)
		assert.Equal(t, "Test Result 1", results[0].Title)
		assert.Equal(t, "https://example.com/1", results[0].URL)
		assert.Equal(t, "bing", results[0].Source)
	})

	t.Run("Empty query", func(t *testing.T) {
		ctx := context.Background()
		results, err := provider.Search(ctx, "", 10, true)
		assert.Error(t, err)
		assert.Nil(t, results)
		assert.Contains(t, err.Error(), "query is empty")
	})
}

func TestBingProvider_Search_Error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	provider := &BingProvider{
		client:  server.Client(),
		baseURL: server.URL,
		apiKey:  "test-api-key",
	}

	t.Run("Server error", func(t *testing.T) {
		ctx := context.Background()
		results, err := provider.Search(ctx, "test query", 10, true)
		assert.Error(t, err)
		assert.Nil(t, results)
	})
}

func TestBingProvider_Search_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	provider := &BingProvider{
		client:  server.Client(),
		baseURL: server.URL,
		apiKey:  "test-api-key",
	}

	t.Run("Invalid JSON response", func(t *testing.T) {
		ctx := context.Background()
		results, err := provider.Search(ctx, "test query", 10, true)
		assert.Error(t, err)
		assert.Nil(t, results)
		assert.Contains(t, err.Error(), "failed to unmarshal response")
	})
}
