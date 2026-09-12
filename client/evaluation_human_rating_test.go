package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvaluationHumanRatingsListAndAppend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/evaluation/tasks/task-a/questions/7/ratings" {
			t.Errorf("request path = %s", request.URL.Path)
		}
		response.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet {
			_, _ = response.Write([]byte(`{
				"success": true,
				"data": {"items": [
					{"id":"rating-1","task_id":"task-a","sample_index":7,"revision":1,"score":4}
				]}
			}`))
			return
		}
		if request.Method != http.MethodPost {
			t.Errorf("request method = %s", request.Method)
		}
		var body AppendEvaluationHumanRatingRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Score != 5 || body.RubricKey != "answer-quality" {
			t.Errorf("request = %+v", body)
		}
		response.WriteHeader(http.StatusCreated)
		_, _ = response.Write([]byte(`{
			"success": true,
			"data": {"id":"rating-2","task_id":"task-a","sample_index":7,"revision":2,"score":5}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	items, err := client.ListEvaluationHumanRatings(context.Background(), "task-a", 7)
	if err != nil || len(items) != 1 || items[0].Revision != 1 {
		t.Fatalf("ListEvaluationHumanRatings() items=%+v error=%v", items, err)
	}
	created, err := client.AppendEvaluationHumanRating(
		context.Background(), "task-a", 7, AppendEvaluationHumanRatingRequest{
			RubricKey: "answer-quality", RubricVersion: "1.0.0",
			RubricSnapshot: json.RawMessage(`{"title":"Answer quality"}`), Score: 5,
		},
	)
	if err != nil || created.Revision != 2 {
		t.Fatalf("AppendEvaluationHumanRating() created=%+v error=%v", created, err)
	}
}
