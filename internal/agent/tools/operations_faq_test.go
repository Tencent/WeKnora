package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const faqEntryPath = "/api/v1/knowledge-bases/kb1/faq/entries/7"

func faqEntryResponse(t *testing.T, answers []string) *operations.Response {
	t.Helper()
	body, err := json.Marshal(map[string]any{"data": map[string]any{
		"id": 7, "standard_question": "Question", "similar_questions": []string{},
		"negative_questions": []string{}, "answers": answers, "answer_strategy": "all", "tag_id": 0,
	}})
	require.NoError(t, err)
	return &operations.Response{Status: http.StatusOK, Body: body}
}

func faqUpdateParams(answer string) map[string]any {
	return map[string]any{
		"knowledge_base_id": "kb1", "entry_id": 7, "standard_question": "Renamed",
		"similar_questions": []string{}, "negative_questions": []string{},
		"answers": []string{answer}, "answer_strategy": "all", "tag_seq_id": 0,
	}
}

func TestRunOperationFAQUpdatePreservesLongAnswerAfterApproval(t *testing.T) {
	answer := strings.Repeat("文", 8000)
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET " + faqEntryPath: faqEntryResponse(t, []string{answer}),
		"PUT " + faqEntryPath: faqEntryResponse(t, []string{answer}),
	}}
	gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
	ctx, cancel := context.WithCancel(interactiveCtx())
	defer cancel()
	meta, _ := ToolExecFromContext(ctx)
	meta.ApprovalCtx = context.Background()
	gate.onRequest = cancel // The user can approve after the original tool budget expires.
	tool := newRunTool(d, gate)
	read, err := tool.Execute(ctx, runArgs("faq.get_entry", map[string]any{"knowledge_base_id": "kb1", "entry_id": 7}))
	require.NoError(t, err)
	require.True(t, read.Success, read.Error)
	var output struct {
		Result struct {
			Data struct{ Answers []string }
		}
	}
	require.NoError(t, json.Unmarshal([]byte(read.Output), &output))
	require.Equal(t, []string{answer}, output.Result.Data.Answers)
	res, err := tool.Execute(ctx, runArgs("faq.update_entry", faqUpdateParams(output.Result.Data.Answers[0])))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, gate.requests, 1)
	require.Len(t, d.calls, 4) // Read, pre-approval read, fresh read, update.
	assert.Equal(t, http.MethodPut, d.calls[3].call.Method)
	var body map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(d.calls[3].call.Body, &body))
	var answers []string
	require.NoError(t, json.Unmarshal(body["answers"], &answers))
	assert.Equal(t, []string{answer}, answers)
	assert.JSONEq(t, `[]`, string(body["similar_questions"]))
	assert.JSONEq(t, `[]`, string(body["negative_questions"]))
	assert.Equal(t, []string{"p1"}, gate.executed)
}

func TestRunOperationFAQUpdateRefusesTruncatedTarget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		answers []string
	}{
		{"long answer", []string{strings.Repeat("a", 8001)}},
		{"too many answers", make([]string, 101)},
	} {
		levels := []types.AssistantAccessLevel{types.AssistantAccessConfirm, types.AssistantAccessAutoWrite}
		for _, level := range levels {
			t.Run(tc.name+"/"+string(level), func(t *testing.T) {
				d := &fakeDispatcher{responses: map[string]*operations.Response{
					"GET " + faqEntryPath: faqEntryResponse(t, tc.answers),
				}}
				gate := &fakeApprover{decision: approval.Decision{Approved: true}}
				args := runArgs("faq.update_entry", faqUpdateParams("Answer"))
				tool := newRunTool(d, gate)
				tool.rt.Access.Level = level
				res, err := tool.Execute(interactiveCtx(), args)
				require.NoError(t, err)
				assert.False(t, res.Success)
				assert.Contains(t, res.Error, "current content was truncated")
				assert.Empty(t, gate.requests, "unsafe replacement must not even request approval")
				require.Len(t, d.calls, 1)
				assert.Equal(t, http.MethodGet, d.calls[0].call.Method)
			})
		}
	}
}

func TestRunOperationFAQAutoWriteChecksCompleteTarget(t *testing.T) {
	answer := strings.Repeat("文", 8000)
	d := &fakeDispatcher{responses: map[string]*operations.Response{
		"GET " + faqEntryPath: faqEntryResponse(t, []string{answer}),
		"PUT " + faqEntryPath: faqEntryResponse(t, []string{answer}),
	}}
	gate := &fakeApprover{}
	tool := newRunTool(d, gate)
	tool.rt.Access.Level = types.AssistantAccessAutoWrite
	res, err := tool.Execute(interactiveCtx(), runArgs("faq.update_entry", faqUpdateParams(answer)))
	require.NoError(t, err)
	require.True(t, res.Success, res.Error)
	require.Len(t, d.calls, 2, "read the complete target before replacing it")
	assert.Equal(t, http.MethodGet, d.calls[0].call.Method)
	assert.Equal(t, http.MethodPut, d.calls[1].call.Method)
	assert.Empty(t, gate.requests, "the integrity check must not request approval")
}

func TestRunOperationFAQUpdateRechecksApprovedTarget(t *testing.T) {
	for _, scenario := range []string{"resumed", "edited target", "grew during approval"} {
		t.Run(scenario, func(t *testing.T) {
			long := faqEntryResponse(t, []string{strings.Repeat("a", 8001)})
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET " + faqEntryPath: faqEntryResponse(t, []string{"Answer"}),
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: true, PendingID: "p1"}}
			ctx := interactiveCtx()
			switch scenario {
			case "resumed":
				meta, _ := ToolExecFromContext(ctx)
				meta.Mode = types.RunModeUnattended
				meta.Answered = &types.AgentPrompt{Kind: types.AgentPromptKindToolApproval}
				d.responses["GET "+faqEntryPath] = long
			case "edited target":
				params := faqUpdateParams("Answer")
				params["entry_id"] = 8
				gate.decision.ModifiedArgs = runArgs("faq.update_entry", params)
				d.responses["GET /api/v1/knowledge-bases/kb1/faq/entries/8"] = long
			case "grew during approval":
				gate.onRequest = func() { d.responses["GET "+faqEntryPath] = long }
			}
			res, err := newRunTool(d, gate).Execute(ctx, runArgs("faq.update_entry", faqUpdateParams("Answer")))
			require.NoError(t, err)
			assert.False(t, res.Success)
			assert.Contains(t, res.Error, "current content was truncated")
			assert.Equal(t, []string{"p1"}, gate.executed)
			for _, call := range d.calls {
				assert.Equal(t, http.MethodGet, call.call.Method, "must never dispatch the replacement")
			}
		})
	}
}
