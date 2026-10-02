package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStartEvaluationMatchesServerContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/evaluation" {
			t.Fatalf("path = %s, want /api/v1/evaluation", r.URL.Path)
		}

		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["knowledge_base_id"] != "kb-reference" {
			t.Fatalf("knowledge_base_id = %v, want kb-reference", body["knowledge_base_id"])
		}
		if _, exists := body["embedding_id"]; exists {
			t.Fatal("deprecated embedding_id must not be serialized")
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"task": {
					"id": "evaluation-1",
					"tenant_id": 7,
					"dataset_id": "default",
					"start_time": "2026-08-31T10:00:00Z",
					"status": 1,
					"total": 10,
					"finished": 4
				},
				"params": {"chat_model_id": "chat-1"}
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("sk-test"))
	task, err := c.StartEvaluation(context.Background(), &EvaluationRequest{
		DatasetID:        "default",
		KnowledgeBaseID:  "kb-reference",
		ChatModelID:      "chat-1",
		RerankModelID:    "rerank-1",
		EmbeddingModelID: "legacy-embedding",
	})
	if err != nil {
		t.Fatalf("StartEvaluation() error = %v", err)
	}
	if task.ID != "evaluation-1" {
		t.Fatalf("task.ID = %q, want evaluation-1", task.ID)
	}
	if task.Status != EvaluationStatusRunning {
		t.Fatalf("task.Status = %q, want running", task.Status)
	}
	if task.Progress != 40 {
		t.Fatalf("task.Progress = %d, want 40", task.Progress)
	}
}

func TestGetEvaluationResultMatchesServerContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if got := r.URL.Query().Get("task_id"); got != "evaluation-1" {
			t.Fatalf("task_id = %q, want evaluation-1", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success": true,
			"data": {
				"task": {
					"id": "evaluation-1",
					"tenant_id": 7,
					"dataset_id": "default",
					"start_time": "2026-08-31T10:00:00Z",
					"status": 2,
					"total": 10,
					"finished": 10
				},
				"params": {"chat_model_id": "chat-1"},
				"metric": {
					"retrieval_metrics": {"precision": 0.8, "recall": 0.9, "ndcg3": 0.7, "ndcg10": 0.75, "mrr": 0.85, "map": 0.72},
					"generation_metrics": {"bleu1": 0.6, "bleu2": 0.5, "bleu4": 0.4, "rouge1": 0.7, "rouge2": 0.55, "rougel": 0.65}
				}
			}
		}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, WithAPIKey("sk-test"))
	result, err := c.GetEvaluationResult(context.Background(), "evaluation-1")
	if err != nil {
		t.Fatalf("GetEvaluationResult() error = %v", err)
	}
	if result.Task == nil || result.Task.Status != EvaluationStatusSuccess {
		t.Fatalf("result.Task = %#v, want success task", result.Task)
	}
	if result.Metric == nil || result.Metric.RetrievalMetrics.Recall != 0.9 {
		t.Fatalf("result.Metric = %#v, want recall 0.9", result.Metric)
	}
	if result.TaskID != "evaluation-1" || result.Progress != 100 {
		t.Fatalf("compatibility fields = task_id %q progress %d", result.TaskID, result.Progress)
	}
	if result.Metrics["rougel"] != 0.65 {
		t.Fatalf("compatibility rougel = %v, want 0.65", result.Metrics["rougel"])
	}
}
