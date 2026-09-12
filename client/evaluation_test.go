package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestEvaluationStatusValuesMatchHTTPContract(t *testing.T) {
	tests := []struct {
		name string
		got  EvaluationStatus
		want EvaluationStatus
	}{
		{name: "pending", got: EvaluationStatusPending, want: 0},
		{name: "running", got: EvaluationStatusRunning, want: 1},
		{name: "success", got: EvaluationStatusSuccess, want: 2},
		{name: "failed", got: EvaluationStatusFailed, want: 3},
		{name: "timed out", got: EvaluationStatusTimedOut, want: 4},
		{name: "interrupted", got: EvaluationStatusInterrupted, want: 5},
		{name: "canceled", got: EvaluationStatusCanceled, want: 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("status = %d, want %d", tt.got, tt.want)
			}
		})
	}
}

func TestEvaluationMethodSignaturesRemainCompatible(t *testing.T) {
	client := NewClient("http://example.test")
	if !evaluationMethodSignaturesAvailable(
		client.StartEvaluation,
		client.GetEvaluationResult,
		client.CancelEvaluation,
	) {
		t.Fatal("evaluation methods must remain available")
	}
}

func evaluationMethodSignaturesAvailable(
	start func(context.Context, *EvaluationRequest) (*EvaluationTask, error),
	get func(context.Context, string) (*EvaluationResult, error),
	cancel func(context.Context, string) (*EvaluationResult, error),
) bool {
	return start != nil && get != nil && cancel != nil
}

func TestCancelEvaluationUsesPostPathAndNestedContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/evaluation/evaluation-1/cancel" {
			t.Errorf("path = %s, want /api/v1/evaluation/evaluation-1/cancel", r.URL.Path)
		}
		task := evaluationTaskFixture(1)
		task["cancel_requested_at"] = "2026-08-28T09:30:00Z"
		writeEvaluationResponse(t, w, map[string]any{
			"task":   task,
			"params": map[string]any{"chat_model_id": "chat-1"},
		})
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).CancelEvaluation(context.Background(), "evaluation-1")
	if err != nil {
		t.Fatalf("CancelEvaluation() error = %v", err)
	}
	if result.Task == nil || result.Task.Status != EvaluationStatusRunning {
		t.Fatalf("task status = %+v, want running", result.Task)
	}
	if result.Task.CancelRequestedAt == nil {
		t.Fatal("cancel_requested_at was not decoded")
	}
	if got := result.Task.CancelRequestedAt.UTC().Format(time.RFC3339); got != "2026-08-28T09:30:00Z" {
		t.Fatalf("cancel_requested_at = %s, want 2026-08-28T09:30:00Z", got)
	}
}

func TestCancelEvaluationDecodesCanceledTerminalStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		task := evaluationTaskFixture(6)
		task["cancel_requested_at"] = "2026-08-28T09:30:00Z"
		task["end_time"] = "2026-08-28T09:31:00Z"
		task["err_msg"] = "evaluation task canceled"
		writeEvaluationResponse(t, w, map[string]any{
			"task":   task,
			"params": map[string]any{"chat_model_id": "chat-1"},
		})
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).CancelEvaluation(context.Background(), "evaluation-1")
	if err != nil {
		t.Fatalf("CancelEvaluation() error = %v", err)
	}
	if result.Task.Status != EvaluationStatusCanceled {
		t.Fatalf("task status = %d, want %d (Canceled)", result.Task.Status, EvaluationStatusCanceled)
	}
}

func TestCancelEvaluationRequiresTaskID(t *testing.T) {
	if _, err := NewClient("http://example.test").CancelEvaluation(context.Background(), ""); err == nil {
		t.Fatal("CancelEvaluation() with empty task ID must fail")
	}
}

func TestListEvaluationsSendsFiltersAndDecodesNestedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/evaluation/tasks" {
			t.Errorf("path = %s, want /api/v1/evaluation/tasks", r.URL.Path)
		}
		query := r.URL.Query()
		if query.Get("status") != "2" || query.Get("page_size") != "5" || query.Get("cursor") != "cur-1" {
			t.Errorf("query = %s, want status=2&page_size=5&cursor=cur-1", r.URL.RawQuery)
		}
		if query.Get("dataset_id") != "dataset" || query.Get("dataset_version_id") != "version" ||
			query.Get("model_id") != "chat" || len(query["label"]) != 2 {
			t.Errorf("extended query = %v", query)
		}
		writeEvaluationResponse(t, w, map[string]any{
			"items":       []any{evaluationTaskFixture(2)},
			"next_cursor": "cursor-next",
		})
	}))
	defer srv.Close()

	status := EvaluationStatusSuccess
	startedFrom := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	page, err := NewClient(srv.URL).ListEvaluations(context.Background(), &EvaluationListOptions{
		Status:           &status,
		DatasetID:        "dataset",
		DatasetVersionID: "version",
		ModelID:          "chat",
		StartedFrom:      &startedFrom,
		Labels:           []string{"baseline", "retrieval"},
		PageSize:         5,
		Cursor:           "cur-1",
	})
	if err != nil {
		t.Fatalf("ListEvaluations() error = %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != EvaluationStatusSuccess {
		t.Fatalf("items = %+v, want one success task", page.Items)
	}
	if page.NextCursor != "cursor-next" {
		t.Fatalf("next cursor = %q, want cursor-next", page.NextCursor)
	}
}

func TestReplaceEvaluationTaskLabelsDecodesStrictNestedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/evaluation/tasks/task-1/labels" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"data":{"task_id":"task-1","labels":["baseline"]}}`))
	}))
	defer srv.Close()

	labels, err := NewClient(srv.URL).ReplaceEvaluationTaskLabels(
		context.Background(),
		"task-1",
		[]string{" Baseline "},
	)
	if err != nil {
		t.Fatalf("ReplaceEvaluationTaskLabels() error = %v", err)
	}
	if len(labels) != 1 || labels[0] != "baseline" {
		t.Fatalf("labels = %#v", labels)
	}
}

func TestListEvaluationsRejectsMissingNestedData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).ListEvaluations(context.Background(), nil); err == nil {
		t.Fatal("ListEvaluations() must fail when the nested data object is missing")
	}
}

func TestListEvaluationsRejectsUnsuccessfulEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"data":    map[string]any{"items": []any{}, "next_cursor": ""},
		})
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL).ListEvaluations(context.Background(), nil); err == nil {
		t.Fatal("ListEvaluations() must fail when success is false")
	}
}

func TestDeleteEvaluationUsesDeletePathAndAccepts204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/v1/evaluation/evaluation-1" {
			t.Errorf("path = %s, want /api/v1/evaluation/evaluation-1", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := NewClient(srv.URL).DeleteEvaluation(context.Background(), "evaluation-1"); err != nil {
		t.Fatalf("DeleteEvaluation() error = %v", err)
	}
}

func TestDeleteEvaluationSurfacesActiveTaskConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "error": "active"})
	}))
	defer srv.Close()

	err := NewClient(srv.URL).DeleteEvaluation(context.Background(), "evaluation-active")
	if err == nil {
		t.Fatal("DeleteEvaluation() must fail for an active task")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("error = %v, want APIError with status 409", err)
	}
}

func TestDeleteEvaluationRequiresTaskID(t *testing.T) {
	if err := NewClient("http://example.test").DeleteEvaluation(context.Background(), ""); err == nil {
		t.Fatal("DeleteEvaluation() with empty task ID must fail")
	}
}

func TestStartEvaluationUsesServerRequestAndNestedTaskContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/evaluation" {
			t.Errorf("path = %s, want /api/v1/evaluation", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		want := map[string]any{
			"dataset_id":        "default",
			"knowledge_base_id": "kb-1",
			"chat_id":           "chat-1",
			"rerank_id":         "rerank-1",
		}
		if !reflect.DeepEqual(body, want) {
			t.Errorf("request body = %#v, want %#v", body, want)
		}

		writeEvaluationResponse(t, w, map[string]any{
			"task": evaluationTaskFixture(1),
			"params": map[string]any{
				"chat_model_id":   "chat-1",
				"rerank_model_id": "rerank-1",
			},
		})
	}))
	defer srv.Close()

	request := &EvaluationRequest{
		DatasetID:       "default",
		KnowledgeBaseID: "kb-1",
		ChatModelID:     "chat-1",
		RerankModelID:   "rerank-1",
	}

	client := NewClient(srv.URL)
	task, err := client.StartEvaluation(context.Background(), request)
	if err != nil {
		t.Fatalf("StartEvaluation() error = %v", err)
	}
	if task.Status != EvaluationStatusRunning {
		t.Fatalf("task.Status = %d, want %d", task.Status, EvaluationStatusRunning)
	}
	if task.ID != "evaluation-1" {
		t.Fatalf("task.ID = %q, want evaluation-1", task.ID)
	}
}

func TestGetEvaluationResultParsesNestedDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/v1/evaluation" {
			t.Errorf("path = %s, want /api/v1/evaluation", r.URL.Path)
		}
		if got := r.URL.Query().Get("task_id"); got != "evaluation-1" {
			t.Errorf("task_id = %q, want evaluation-1", got)
		}

		writeEvaluationResponse(t, w, map[string]any{
			"task": evaluationTaskFixture(2),
			"params": map[string]any{
				"chat_model_id": "chat-1",
			},
			"metric": map[string]any{
				"retrieval_metrics":  map[string]any{"precision": 0.5},
				"generation_metrics": map[string]any{"bleu1": 0.25},
			},
			"runtime_metrics": map[string]any{
				"cost": map[string]any{
					"call_count": 2, "accounting_complete_calls": 1, "unpriced_calls": 1,
					"usage_unreported_calls": 1, "started_calls": 0,
					"totals": []map[string]any{{"currency": "CNY", "cost_microunits": 0}},
				},
				"schema_version": 1,
				"started_at":     "2026-08-28T08:00:00Z",
				"durations":      map[string]any{"total_ms": 125},
				"samples":        map[string]any{"total": 1, "started": 1, "success": 1},
				"failure":        map[string]any{"numerator": 0, "denominator": 1},
				"tokens": map[string]any{
					"prompt_tokens": 10, "completion_tokens": 4, "total_tokens": 14,
					"reported_samples": 1, "unreported_samples": 0,
				},
			},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	result, err := client.GetEvaluationResult(context.Background(), "evaluation-1")
	if err != nil {
		t.Fatalf("GetEvaluationResult() error = %v", err)
	}

	if result.Task == nil {
		t.Fatal("EvaluationResult.Task must contain the nested task")
	}
	if got := result.Task.ID; got != "evaluation-1" {
		t.Fatalf("result.Task.ID = %q, want evaluation-1", got)
	}
	if result.Task.Status != EvaluationStatusSuccess {
		t.Fatalf("result.Task.Status = %d, want %d", result.Task.Status, EvaluationStatusSuccess)
	}
	if len(result.Params) == 0 {
		t.Fatal("EvaluationResult.Params must preserve the nested response")
	}
	if result.Metric == nil {
		t.Fatal("EvaluationResult.Metric must preserve the nested response")
	}
	if result.RuntimeMetrics == nil || result.RuntimeMetrics.Durations.TotalMs == nil ||
		*result.RuntimeMetrics.Durations.TotalMs != 125 {
		t.Fatalf("EvaluationResult.RuntimeMetrics = %#v, want total_ms=125", result.RuntimeMetrics)
	}
	if cost := result.RuntimeMetrics.Cost; cost == nil || cost.CallCount != 2 || cost.UnpricedCalls != 1 ||
		cost.UsageUnreportedCalls != 1 || len(cost.Totals) != 1 || cost.Totals[0].CostMicrounits != 0 {
		t.Fatalf("runtime accounting coverage or explicit zero cost lost: %#v", cost)
	}
}

func TestGetEvaluationResultParsesInterruptedTerminalTask(t *testing.T) {
	const endTime = "2026-08-28T08:05:06Z"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		task := evaluationTaskFixture(5)
		task["end_time"] = endTime
		task["err_msg"] = "evaluation worker interrupted"
		task["cleanup_errors"] = []string{"delete temporary knowledge base: unavailable"}
		writeEvaluationResponse(t, w, map[string]any{
			"task":   task,
			"params": map[string]any{},
		})
	}))
	defer srv.Close()

	result, err := NewClient(srv.URL).GetEvaluationResult(context.Background(), "evaluation-1")
	if err != nil {
		t.Fatalf("GetEvaluationResult() error = %v", err)
	}

	if result.Task.Status != EvaluationStatusInterrupted {
		t.Fatalf("result.Task.Status = %d, want %d", result.Task.Status, EvaluationStatusInterrupted)
	}
	if result.Task.EndTime == nil {
		t.Fatal("result.Task.EndTime must preserve the terminal timestamp")
	}
	wantEndTime, err := time.Parse(time.RFC3339, endTime)
	if err != nil {
		t.Fatalf("parse fixture end time: %v", err)
	}
	if !result.Task.EndTime.Equal(wantEndTime) {
		t.Fatalf("result.Task.EndTime = %s, want %s", result.Task.EndTime, wantEndTime)
	}
	wantCleanupErrors := []string{"delete temporary knowledge base: unavailable"}
	if !reflect.DeepEqual(result.Task.CleanupErrors, wantCleanupErrors) {
		t.Fatalf("result.Task.CleanupErrors = %#v, want %#v", result.Task.CleanupErrors, wantCleanupErrors)
	}
	if result.Task.ErrMsg != "evaluation worker interrupted" {
		t.Fatalf("result.Task.ErrMsg = %q, want interruption message", result.Task.ErrMsg)
	}
}

func TestGetEvaluationResultRejectsStringStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		task := evaluationTaskFixture(1)
		task["status"] = "running"
		writeEvaluationResponse(t, w, map[string]any{
			"task":   task,
			"params": map[string]any{},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).GetEvaluationResult(context.Background(), "evaluation-1")
	if err == nil {
		t.Fatal("GetEvaluationResult() must reject a string status")
	}
}

func TestEvaluationTaskRejectsUnknownNumericStatus(t *testing.T) {
	task := evaluationTaskFixture(99)
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}

	var decoded EvaluationTask
	if err := json.Unmarshal(payload, &decoded); err == nil {
		t.Fatal("EvaluationTask must reject an unknown numeric status")
	}
}

func TestGetEvaluationResultRejectsMissingNestedTask(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeEvaluationResponse(t, w, map[string]any{
			"params": map[string]any{},
		})
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).GetEvaluationResult(context.Background(), "evaluation-1")
	if err == nil {
		t.Fatal("GetEvaluationResult() must reject a response without data.task")
	}
}

func TestStartEvaluationRejectsDeprecatedEmbeddingModelField(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL).StartEvaluation(context.Background(), &EvaluationRequest{
		DatasetID:        "default",
		EmbeddingModelID: "embedding-1",
	})
	if !errors.Is(err, ErrEvaluationEmbeddingModelUnsupported) {
		t.Fatalf("StartEvaluation() error = %v, want explicit EmbeddingModelID error", err)
	}
	if called {
		t.Fatal("deprecated embedding field must fail before sending an HTTP request")
	}
}

func evaluationTaskFixture(status int) map[string]any {
	return map[string]any{
		"id":         "evaluation-1",
		"tenant_id":  7,
		"dataset_id": "default",
		"start_time": "2026-08-28T08:00:00Z",
		"status":     status,
		"total":      4,
		"finished":   2,
	}
}

func writeEvaluationResponse(t *testing.T, w http.ResponseWriter, data map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "data": data}); err != nil {
		t.Errorf("encode response: %v", err)
	}
}

func TestIsEvaluationSeedUnsupported(t *testing.T) {
	if IsEvaluationSeedUnsupported(errors.New("random")) {
		t.Fatal("plain errors must not classify as seed-unsupported")
	}
	if IsEvaluationSeedUnsupported(&APIError{StatusCode: 404, Body: "{}"}) {
		t.Fatal("404 must not classify as seed-unsupported")
	}
	if !IsEvaluationSeedUnsupported(&APIError{
		StatusCode: 422,
		Body:       `{"success":false,"error":{"code":1010,"message":"seed unsupported"}}`,
		Code:       1010,
	}) {
		t.Fatal("the evaluation 422 seed rejection must classify as seed-unsupported")
	}
	wrapped := fmt.Errorf("start evaluation: %w", &APIError{StatusCode: 422})
	if !IsEvaluationSeedUnsupported(wrapped) {
		t.Fatal("wrapped API errors must classify through errors.As")
	}
}
