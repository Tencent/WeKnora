package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListEvaluationMetricsDecodesRegistryCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/evaluation/metrics" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {"items": [{
				"key":"retrieval.ndcg","version":"1.0.0","kind":"retrieval",
				"description":"NDCG","default_config":{"k":3},"config_schema":{"type":"object"}
			}]}
		}`))
	}))
	defer server.Close()

	definitions, err := NewClient(server.URL).ListEvaluationMetrics(context.Background())
	if err != nil {
		t.Fatalf("ListEvaluationMetrics() error = %v", err)
	}
	if len(definitions) != 1 || definitions[0].Key != "retrieval.ndcg" {
		t.Fatalf("definitions = %#v", definitions)
	}
}
