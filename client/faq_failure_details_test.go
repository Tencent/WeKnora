package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestFAQImportFailureDetails(t *testing.T) {
	for _, partial := range []bool{true, false} {
		t.Run(fmt.Sprint(partial), func(t *testing.T) {
			entry := map[string]any{
				"index": 2, "reason": "validation failed", "standard_question": "question",
				"failure_type": "post_validation", "is_partial_failure": partial,
				"removed_similar_questions":  []string{"duplicate question"},
				"removed_negative_questions": []string{"invalid negative"},
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/faq/import/progress/task-1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{
					"task_id": "task-1", "failed_entries": []any{entry},
				}})
			}))
			defer srv.Close()
			progress, err := NewClient(srv.URL).GetFAQImportProgress(context.Background(), "task-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(progress.FailedEntries) != 1 {
				t.Fatalf("failed entries: %#v", progress.FailedEntries)
			}
			// Compare the wire representation so the regression also runs against the old SDK.
			data, err := json.Marshal(progress.FailedEntries[0])
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"failure_type", "removed_similar_questions", "removed_negative_questions"} {
				wantJSON, _ := json.Marshal(entry[key])
				gotJSON, _ := json.Marshal(got[key])
				if !reflect.DeepEqual(gotJSON, wantJSON) {
					t.Errorf("%s = %s, want %s", key, gotJSON, wantJSON)
				}
			}
			if got["is_partial_failure"] != partial && (got["is_partial_failure"] != nil || partial) {
				t.Errorf("is_partial_failure = %v, want %v", got["is_partial_failure"], partial)
			}
		})
	}
}

func TestFAQFailureDetailsOlderServer(t *testing.T) {
	var entry FAQFailedEntry
	if err := json.Unmarshal([]byte(`{"index":1,"reason":"invalid","standard_question":"q"}`), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.FailureType != "" || entry.IsPartialFailure ||
		entry.RemovedSimilarQuestions != nil || entry.RemovedNegativeQuestions != nil {
		t.Fatalf("absent details should have zero values: %#v", entry)
	}
	if entry.Index != 1 || entry.Reason != "invalid" || entry.StandardQuestion != "q" {
		t.Fatalf("existing fields changed: %#v", entry)
	}
}
