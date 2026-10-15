package tools

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestDataSourceSyncHonorsAssistantAccess(t *testing.T) {
	for _, level := range []types.AssistantAccessLevel{
		types.AssistantAccessReadOnly, types.AssistantAccessConfirm,
		types.AssistantAccessAutoWrite, types.AssistantAccessFullAuto,
	} {
		t.Run(string(level), func(t *testing.T) {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/datasource/d1":       {Status: 200, Body: []byte(`{"id":"d1","name":"Docs"}`)},
				"POST /api/v1/datasource/d1/sync": {Status: 200, Body: []byte(`{"id":"log1","status":"running"}`)},
			}}
			gate := &fakeApprover{decision: approval.Decision{Approved: false}}
			tool := newRunTool(d, gate)
			tool.rt.Access.Level = level
			result, err := tool.Execute(interactiveCtx(),
				runArgs("data_source.sync", map[string]any{"data_source_id": "d1"}))
			require.NoError(t, err)
			require.Equal(t, level == types.AssistantAccessFullAuto, result.Success)
			writes := 0
			for _, call := range d.calls {
				if call.call.Method == "POST" {
					writes++
				}
			}
			if level == types.AssistantAccessFullAuto {
				require.Equal(t, 1, writes)
				require.Empty(t, gate.requests)
			} else {
				require.Zero(t, writes)
				if level != types.AssistantAccessReadOnly {
					require.Len(t, gate.requests, 1)
					require.Contains(t, gate.requests[0].Description, "Docs")
				}
			}
		})
	}
}

func TestInfrastructureErrorsAreNotReturnedToModelOrApproval(t *testing.T) {
	for _, op := range []string{"data_source.get", "data_source.sync"} {
		t.Run(op, func(t *testing.T) {
			d := &fakeDispatcher{responses: map[string]*operations.Response{
				"GET /api/v1/datasource/d1": {Status: 500, Body: []byte(`{"error":"token=SECRET"}`)},
			}}
			gate := &fakeApprover{}
			tool := newRunTool(d, gate)
			result, err := tool.Execute(interactiveCtx(), runArgs(op, map[string]any{"data_source_id": "d1"}))
			require.NoError(t, err)
			require.False(t, result.Success)
			require.NotContains(t, result.Error, "SECRET")
			require.Empty(t, gate.requests)
		})
	}
}
