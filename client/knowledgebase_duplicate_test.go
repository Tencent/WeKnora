package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDuplicateKnowledgeBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/knowledge-bases/kb-1/duplicate" {
			t.Fatalf("path = %s, want /api/v1/knowledge-bases/kb-1/duplicate", r.URL.Path)
		}
		if r.ContentLength > 0 {
			t.Fatalf("expected empty request body, got content-length=%d", r.ContentLength)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"data": map[string]interface{}{
				"source_id": "kb-1",
				"target_id": "kb-2",
				"message":   "Knowledge base duplicate created",
				"knowledge_base": map[string]interface{}{
					"id":   "kb-2",
					"name": "Source Copy",
					"type": "document",
				},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("sk-test"))
	resp, err := c.DuplicateKnowledgeBase(context.Background(), "kb-1")
	if err != nil {
		t.Fatalf("DuplicateKnowledgeBase() error = %v", err)
	}
	if resp.SourceID != "kb-1" {
		t.Fatalf("SourceID = %q, want kb-1", resp.SourceID)
	}
	if resp.TargetID != "kb-2" {
		t.Fatalf("TargetID = %q, want kb-2", resp.TargetID)
	}
	if resp.KnowledgeBase.ID != "kb-2" {
		t.Fatalf("KnowledgeBase.ID = %q, want kb-2", resp.KnowledgeBase.ID)
	}
	if resp.KnowledgeBase.Name != "Source Copy" {
		t.Fatalf("KnowledgeBase.Name = %q, want Source Copy", resp.KnowledgeBase.Name)
	}
}
