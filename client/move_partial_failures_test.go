package client

import (
	"encoding/json"
	"testing"
)

func TestMoveProgressPartialFailure(t *testing.T) {
	var p KnowledgeMoveProgress
	if err := json.Unmarshal([]byte(`{
  "task_id": "move",
  "processed": 3,
  "total": 3,
  "failed": 1,
  "source_kb_id": "from",
  "target_kb_id": "to",
  "updated_at": 1700000000
}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.Processed-p.Failed != 2 || p.SourceKBID != "from" || p.TargetKBID != "to" || p.UpdatedAt != 1700000000 {
		t.Fatalf("unexpected move progress: %+v", p)
	}
}
