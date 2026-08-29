package weknora

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

func TestCreateManualKnowledgeUsesPublicEndpointAndAPIKey(t *testing.T) {
	t.Helper()
	var got ManualKnowledgeInput
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		if r.URL.Path != "/api/v1/knowledge-bases/kb-1/knowledge/manual" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "secret" {
			t.Fatalf("X-API-Key = %q", r.Header.Get("X-API-Key"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"id":"knowledge-1","knowledge_base_id":"kb-1"}}`))
	}))
	defer server.Close()

	client := New(config.WeKnoraConfig{BaseURL: server.URL + "/", APIKey: "secret", KBID: "kb-1"})
	result, err := client.CreateManualKnowledge(context.Background(), "", ManualKnowledgeInput{
		Title: "transcript/video-1/000000", Content: "原文", Status: "publish", Channel: "api",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "knowledge-1" || result.KnowledgeBaseID != "kb-1" {
		t.Fatalf("result = %#v", result)
	}
	if got.Title != "transcript/video-1/000000" || got.Content != "原文" || got.Status != "publish" || got.Channel != "api" {
		t.Fatalf("request = %#v", got)
	}
}

func TestCreateManualKnowledgeRejectsEmptyResponseID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{}}`))
	}))
	defer server.Close()

	client := New(config.WeKnoraConfig{BaseURL: server.URL, KBID: "kb-1"})
	_, err := client.CreateManualKnowledge(context.Background(), "", ManualKnowledgeInput{Title: "title", Content: "content"})
	if err == nil {
		t.Fatal("expected empty knowledge id error")
	}
}

func TestHybridSearchScopesToKnowledgeAndRequiresSuccessfulResponse(t *testing.T) {
	client := New(config.WeKnoraConfig{BaseURL: "http://weknora.test", KBID: "kb-1"})
	client.http = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/knowledge-bases/kb-1/hybrid-search" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request SearchParams
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.QueryText != "视频定位信息" || len(request.KnowledgeIDs) != 1 || request.KnowledgeIDs[0] != "knowledge-1" {
			t.Fatalf("search request = %#v", request)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":[{"id":"chunk-1","knowledge_id":"knowledge-1","content":"原文"}]}`)),
			Header:     make(http.Header),
		}, nil
	})}
	searchable, err := client.IsKnowledgeSearchable(context.Background(), "", "knowledge-1")
	if err != nil {
		t.Fatal(err)
	}
	if !searchable {
		t.Fatal("knowledge should be searchable")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
