package client

import (
	"encoding/json"
	"testing"
)

// TestFAQImportProgressUnmarshalsServerFields reproduces Tencent/WeKnora#5691:
// the server's GET /api/v1/faq/import/progress/{task_id} returns seven fields
// that client.FAQImportProgress used to drop silently on unmarshal.
func TestFAQImportProgressUnmarshalsServerFields(t *testing.T) {
	// Sample payload modelled on the real response quoted in the issue
	// (total/success/failed/added/next_entry_index) plus the remaining
	// server-side merge fields from internal/types/faq.go.
	const payload = `{
		"task_id": "task-1",
		"kb_id": "kb-1",
		"knowledge_id": "faq-1",
		"status": "completed",
		"progress": 100,
		"total": 342,
		"processed": 342,
		"success_count": 339,
		"failed_count": 0,
		"partial_failed_count": 3,
		"skipped_count": 0,
		"valid_entry_indices": [0, 1, 2],
		"merge_entry_indices": [3],
		"merged_count": 1,
		"added_count": 341,
		"merge_details": [
			{
				"index": 3, "standard_question": "q", "answer_changed": true,
				"new_similar_count": 2, "new_negative_count": 1
			}
		],
		"next_entry_index": 342,
		"import_mode": "append",
		"display_status": "open",
		"processing_time": 1234
	}`

	var got FAQImportProgress
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("Unmarshal FAQImportProgress: %v", err)
	}

	// Control: pre-existing fields must keep parsing exactly as before.
	if got.Total != 342 || got.SuccessCount != 339 || got.FailedCount != 0 {
		t.Fatalf("existing fields = total %d success %d failed %d, want 342/339/0",
			got.Total, got.SuccessCount, got.FailedCount)
	}

	if got.PartialFailedCount != 3 {
		t.Errorf("PartialFailedCount = %d, want 3 (partial failures must not read as full success)",
			got.PartialFailedCount)
	}
	if len(got.ValidEntryIndices) != 3 || got.ValidEntryIndices[2] != 2 {
		t.Errorf("ValidEntryIndices = %v, want [0 1 2]", got.ValidEntryIndices)
	}
	if len(got.MergeEntryIndices) != 1 || got.MergeEntryIndices[0] != 3 {
		t.Errorf("MergeEntryIndices = %v, want [3]", got.MergeEntryIndices)
	}
	if got.MergedCount != 1 {
		t.Errorf("MergedCount = %d, want 1", got.MergedCount)
	}
	if got.AddedCount != 341 {
		t.Errorf("AddedCount = %d, want 341", got.AddedCount)
	}
	if len(got.MergeDetails) != 1 {
		t.Fatalf("MergeDetails = %v, want 1 entry", got.MergeDetails)
	}
	d := got.MergeDetails[0]
	if d.Index != 3 || d.StandardQuestion != "q" || !d.AnswerChanged ||
		d.NewSimilarCount != 2 || d.NewNegativeCount != 1 {
		t.Errorf("MergeDetails[0] = %+v, want {Index:3 StandardQuestion:q "+
			"AnswerChanged:true NewSimilarCount:2 NewNegativeCount:1}", d)
	}
	if got.NextEntryIndex != 342 {
		t.Errorf("NextEntryIndex = %d, want 342", got.NextEntryIndex)
	}
}

// The issue's own sample response carries "partial_failed_count": null.
// Unmarshalling null into the new int field must not error (it reads as 0).
func TestFAQImportProgressNullPartialFailedCount(t *testing.T) {
	const payload = `{
		"task_id": "task-1", "status": "completed", "total": 342,
		"success_count": 342, "failed_count": 0, "partial_failed_count": null,
		"valid_entry_indices": [0, 1, 2], "added_count": 342, "next_entry_index": 0
	}`

	var got FAQImportProgress
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("Unmarshal FAQImportProgress with null partial_failed_count: %v", err)
	}
	if got.PartialFailedCount != 0 {
		t.Errorf("PartialFailedCount = %d, want 0 for null", got.PartialFailedCount)
	}
	if got.AddedCount != 342 || got.NextEntryIndex != 0 || len(got.ValidEntryIndices) != 3 {
		t.Errorf("fields = added %d next %d valid %v, want 342/0/[0 1 2]",
			got.AddedCount, got.NextEntryIndex, got.ValidEntryIndices)
	}
}
