package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartEvaluationParsesV1Projection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/evaluation" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var request EvaluationRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.KnowledgeBaseID != "kb-1" {
			t.Fatalf("knowledge_base_id = %q", request.KnowledgeBaseID)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"task":{"id":"run-1","dataset_id":"dataset-1","start_time":"2026-06-15T08:00:00Z","status":1,"total":4,"finished":1},"params":{"chat_model_id":"chat-1","rerank_model_id":"rerank-1"}}}`))
	}))
	defer server.Close()

	task, err := NewClient(server.URL).StartEvaluation(context.Background(), &EvaluationRequest{DatasetID: "dataset-1", KnowledgeBaseID: "kb-1", ChatModelID: "chat-1", RerankModelID: "rerank-1"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "run-1" || task.Status != "running" || task.Progress != 25 {
		t.Fatalf("unexpected task projection: %#v", task)
	}
	if task.ChatID != "chat-1" || task.RerankID != "rerank-1" {
		t.Fatalf("unexpected parameter projection: %#v", task)
	}
}

func TestGetEvaluationResultParsesV1Projection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/evaluation" || r.URL.Query().Get("task_id") != "run-1" {
			t.Fatalf("unexpected request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"success":true,"data":{"task":{"id":"run-1","dataset_id":"dataset-1","start_time":"2026-06-15T08:00:00Z","status":2,"total":4,"finished":4},"params":{},"metric":{"retrieval_metrics":{"precision":0.75,"recall":0.5},"generation_metrics":{"bleu1":0.25}}}}`))
	}))
	defer server.Close()

	result, err := NewClient(server.URL).GetEvaluationResult(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "run-1" || result.Status != "completed" || result.Progress != 100 || result.TotalSamples != 4 {
		t.Fatalf("unexpected result projection: %#v", result)
	}
	if result.Metrics["precision"] != 0.75 || result.Metrics["bleu1"] != 0.25 {
		t.Fatalf("unexpected metric projection: %#v", result.Metrics)
	}
}
