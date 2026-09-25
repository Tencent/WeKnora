package client

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type memoryExportRoundTripFunc func(*http.Request) (*http.Response, error)

func (f memoryExportRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestExportMemoryDecodesLearningProfile(t *testing.T) {
	transport := memoryExportRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/memory/export" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(`{
				"success": true,
				"total": 0,
				"truncated": false,
				"data": [],
				"learning_profile": {
					"schema_version": 1,
					"knowledge_node": "wiki_page",
					"evidence_kind": "answer_source_use",
					"max_score_without_assessment": 80,
					"documents_truncated": false,
					"documents": [{"knowledge_id": "doc-1", "hits": 2}]
				}
			}`)),
			Request: r,
		}, nil
	})

	export, err := NewClient("https://example.invalid", WithTransport(transport)).ExportMemory(context.Background())
	if err != nil {
		t.Fatalf("ExportMemory() error = %v", err)
	}
	if export.LearningProfile == nil || len(export.LearningProfile.Documents) != 1 {
		t.Fatalf("learning profile not decoded: %#v", export.LearningProfile)
	}
	if got := export.LearningProfile.Documents[0].KnowledgeID; got != "doc-1" {
		t.Fatalf("KnowledgeID = %q, want doc-1", got)
	}
}
