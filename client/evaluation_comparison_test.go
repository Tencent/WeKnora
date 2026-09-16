package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareEvaluationRunsRequiresCompleteSuccessfulEnvelope(t *testing.T) {
	for _, fixture := range []struct {
		name string
		body string
	}{
		{name: "success false", body: `{"success":false,"data":{"runs":[],"parameters":[],"metrics":[]}}`},
		{name: "missing data", body: `{"success":true}`},
		{name: "missing collections", body: `{"success":true,"data":{"schema_version":1}}`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				_, _ = response.Write([]byte(fixture.body))
			}))
			defer server.Close()
			client := NewClient(server.URL)
			_, err := client.CompareEvaluationRuns(context.Background(), EvaluationComparisonRequest{
				TaskIDs: []string{"task-a", "task-b"},
			})
			if err == nil {
				t.Fatal("CompareEvaluationRuns() error = nil")
			}
		})
	}
}

func TestCompareEvaluationRunsSendsRequestAndDecodesData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("request method = %s, want POST", request.Method)
		}
		if request.URL.Path != "/api/v1/evaluation/comparisons" {
			t.Errorf("request path = %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"success":true,
			"data":{"schema_version":1,"baseline_task_id":"task-a","runs":[],"parameters":[],"metrics":[]}
		}`))
	}))
	defer server.Close()
	client := NewClient(server.URL)
	result, err := client.CompareEvaluationRuns(context.Background(), EvaluationComparisonRequest{
		TaskIDs: []string{"task-a", "task-b"},
	})
	if err != nil {
		t.Fatalf("CompareEvaluationRuns() error = %v", err)
	}
	if result.BaselineTaskID != "task-a" {
		t.Fatalf("baseline_task_id = %s", result.BaselineTaskID)
	}
}
