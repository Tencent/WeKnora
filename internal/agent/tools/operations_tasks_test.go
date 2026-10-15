package tools

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/stretchr/testify/require"
)

func TestAsyncTaskDescriptorsAreBoundToOperation(t *testing.T) {
	for _, tc := range []struct{ op, kind, field string }{
		{"knowledge.move", "move", "task_id"},
		{"evaluation.start", "evaluation", "task_id"},
		{"data_source.sync", "sync", "sync_log_id"},
		{"knowledge.delete", "unknown", "task_id"},
		{"knowledge.reparse", "parse", "knowledge_id"},
		{"faq.import_selected", "faq_import", "task_id"},
	} {
		p := operations.Projected{Data: map[string]any{tc.field: "id", "url": "https://untrusted.example"}}
		require.Equal(t, []operationTask{{tc.kind, "id"}}, operationTasks(operations.Operation{ID: tc.op}, nil, p))
	}
	require.Empty(t, operationTasks(operations.Operation{ID: "agent.update"}, nil, operations.Projected{}))
	require.Empty(t, operationTasks(operations.Operation{ID: "knowledge.create_manual"}, nil,
		operations.Projected{Data: map[string]any{"knowledge_id": "draft", "parse_status": "uninitialized"}}))
	tasks := operationTasks(operations.Operation{ID: "knowledge.batch_reparse"}, map[string]json.RawMessage{
		"knowledge_ids": json.RawMessage(`["a","b"]`),
	}, operations.Projected{})
	require.Equal(t, []operationTask{{"parse", "a"}, {"parse", "b"}}, tasks)
}
