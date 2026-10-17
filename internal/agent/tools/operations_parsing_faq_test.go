package tools

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func faqBatchDispatcher() *fakeDispatcher {
	return &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge-bases/kb1": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"kb1","name":"Support FAQ"}}`),
		},
		"GET /api/v1/knowledge-bases/kb1/faq/entries/1": {
			Status: 200,
			Body:   []byte(`{"data":{"id":1,"standard_question":"How to log in?"}}`),
		},
		"GET /api/v1/knowledge-bases/kb1/faq/entries/2": {
			Status: 200,
			Body:   []byte(`{"data":{"id":2,"standard_question":"How to reset password?"}}`),
		},
		"PATCH /api/v1/knowledge-bases/kb1/faq/entries": {Status: 200, Body: []byte(`{"success":true}`)},
	}}
}

func TestFAQBatchUpdateConfirmsEachQuestionAndPreservesPayload(t *testing.T) {
	for _, approved := range []bool{true, false} {
		d := faqBatchDispatcher()
		gate := &fakeApprover{decision: approval.Decision{Approved: approved, PendingID: "p1"}}
		res, err := newRunTool(
			d,
			gate,
		).Execute(
			interactiveCtx(),
			runArgs(
				"faq.batch_update",
				map[string]any{
					"knowledge_base_id": "kb1",
					"entry_ids": []int{
						1,
						2,
					},
					"is_enabled": false,
					"tag_seq_id": 0,
				},
			),
		)
		require.NoError(t, err)
		assert.Equal(t, approved, res.Success, res.Error)
		require.Len(t, gate.requests, 1)
		desc := gate.requests[0].Description
		for _, text := range []string{
			"Support FAQ",
			"How to log in?",
			"How to reset password?",
			"Enabled: false",
			"Tag seq_id (0 clears): 0",
		} {
			assert.Contains(t, desc, text)
		}
		if approved {
			require.Len(t, d.calls, 4)
			assert.JSONEq(t, `{"entry_ids":[1,2],"is_enabled":false,"tag_id":0}`, string(d.calls[3].call.Body))
			out, _ := json.Marshal(res.Data)
			assert.JSONEq(t, `{
  "data_change": {
    "operation": "faq.batch_update",
    "area": "faq",
    "ids": {
      "knowledge_base_id": [
        "kb1"
      ]
    }
  }
}`, string(out))
		} else {
			require.Len(t, d.calls, 3)
			assert.Empty(t, res.Data)
		}
	}
}

func TestFAQBatchUpdateFailsBeforeApprovalForUnreadableEntry(t *testing.T) {
	d := faqBatchDispatcher()
	d.responses["GET /api/v1/knowledge-bases/kb1/faq/entries/2"] = &operations.Response{
		Status: http.StatusForbidden,
		Body:   []byte(`{"error":"forbidden"}`),
	}
	gate := &fakeApprover{}
	res, err := newRunTool(
		d,
		gate,
	).Execute(
		interactiveCtx(),
		runArgs(
			"faq.batch_update",
			map[string]any{
				"knowledge_base_id": "kb1",
				"entry_ids": []int{
					1,
					2,
				},
				"is_enabled": true,
			},
		),
	)
	require.NoError(t, err)
	assert.False(t, res.Success)
	assert.Empty(t, gate.requests)
	for _, call := range d.calls {
		assert.Equal(t, http.MethodGet, call.call.Method)
	}
}

func TestFAQBatchUpdateHonorsReadOnlyAndAutoWrite(t *testing.T) {
	for _, level := range []types.AssistantAccessLevel{types.AssistantAccessReadOnly, types.AssistantAccessAutoWrite} {
		d := faqBatchDispatcher()
		gate := &fakeApprover{}
		tool := newRunTool(d, gate)
		tool.rt.Access.Level = level
		res, err := tool.Execute(
			interactiveCtx(),
			runArgs(
				"faq.batch_update",
				map[string]any{"knowledge_base_id": "kb1", "entry_ids": []int{1}, "is_recommended": false},
			),
		)
		require.NoError(t, err)
		assert.Empty(t, gate.requests)
		if level == types.AssistantAccessReadOnly {
			assert.False(t, res.Success)
			assert.Empty(t, d.calls)
		} else {
			require.True(t, res.Success, res.Error)
			require.Len(t, d.calls, 1)
		}
	}
}

func TestParsingApprovalShowsChangesAndDoesNotReparse(t *testing.T) {
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/knowledge-bases/kb1":   {Status: 200, Body: []byte(`{"data":{"id":"kb1","name":"Docs"}}`)},
		"PATCH /api/v1/knowledge-bases/kb1": {Status: 200, Body: []byte(`{"data":{"id":"kb1","name":"Docs"}}`)},
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	res, err := newRunTool(
		d,
		gate,
	).Execute(
		interactiveCtx(),
		runArgs(
			"knowledge_base.update",
			map[string]any{
				"knowledge_base_id":   "kb1",
				"enable_parent_child": true,
				"parent_chunk_size":   4096,
				"child_chunk_size":    384,
				"parser_file_types": []string{
					"pdf",
				},
				"parser_engine":               "builtin",
				"question_generation_enabled": true,
				"question_count":              5,
			},
		),
	)
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	for _, text := range []string{
		"Parent/child chunking: true",
		"Parent chunk size: 4096",
		"File types: pdf",
		"builtin",
		"Questions per chunk: 5",
		"not automatically reparsed",
	} {
		assert.Contains(t, gate.requests[0].Description, text)
	}
	require.Len(t, d.calls, 2)
	assert.Equal(t, "/api/v1/knowledge-bases/kb1", d.calls[1].call.Path)
}
