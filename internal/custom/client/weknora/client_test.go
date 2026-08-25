package weknora

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
