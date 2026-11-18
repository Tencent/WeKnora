package tools

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func modelCopyDispatcher() *fakeDispatcher {
	return &fakeDispatcher{responses: map[string]*operations.Response{
		"GET /api/v1/models/source": {
			Status: 200,
			Body:   []byte(`{"data":{"id":"source","name":"upstream-model","display_name":"Production"}}`),
		},
		"POST /api/v1/models/source/copy": {Status: 201, Body: []byte(`{
  "data": {
    "id": "copy",
    "name": "upstream-model",
    "display_name": "Experiment",
    "status": "active",
    "is_default": false,
    "parameters": {
      "api_key": "hidden-key",
      "app_secret": "hidden-secret",
      "extra_config": {
        "secret_key": "hidden-extra"
      }
    }
  }
}`)},
	}}
}

func TestModelCopyApprovalsPolicyAndResult(t *testing.T) {
	for _, level := range []types.AssistantAccessLevel{
		"",
		types.AssistantAccessAutoWrite,
		types.AssistantAccessReadOnly,
	} {
		for _, approved := range []bool{false, true} {
			d := modelCopyDispatcher()
			gate := &fakeApprover{decision: approval.Decision{Approved: approved, PendingID: "copy-approval"}}
			tool := newRunTool(d, gate)
			tool.rt.Access.Level = level
			result, err := tool.Execute(
				interactiveCtx(),
				runArgs("model.copy", map[string]any{"model_id": "source", "display_name": "Experiment"}),
			)
			require.NoError(t, err)
			if level == types.AssistantAccessReadOnly {
				assert.False(t, result.Success)
				assert.Empty(t, d.calls)
				assert.Empty(t, gate.requests)
				continue
			}
			if level == types.AssistantAccessAutoWrite {
				assert.True(t, result.Success, result.Error)
				assert.Empty(t, gate.requests)
			} else {
				require.Len(t, gate.requests, 1)
				for _, text := range []string{
					"upstream-model",
					"Experiment",
					"credentials on the backend",
					"background download",
				} {
					assert.Contains(t, gate.requests[0].Description, text)
				}
				assert.Equal(t, approved, result.Success, result.Error)
			}
			if !result.Success {
				assert.Empty(t, result.Data)
				for _, call := range d.calls {
					assert.Equal(t, "GET", call.call.Method)
				}
				continue
			}
			assert.JSONEq(t, `{"display_name":"Experiment"}`, string(d.calls[len(d.calls)-1].call.Body))
			assert.Contains(t, result.Output, `"model_id":"copy"`)
			assert.NotContains(t, result.Output, "hidden-")
			change, err := json.Marshal(result.Data)
			require.NoError(t, err)
			assert.Contains(t, string(change), `"operation":"model.copy"`)
			assert.Contains(t, string(change), `"area":"model"`)
		}
	}
}

func TestModelCopyRejectsUnreadableSourceAndNonAdmin(t *testing.T) {
	for _, nonAdmin := range []bool{false, true} {
		d := modelCopyDispatcher()
		d.responses["GET /api/v1/models/source"] = &operations.Response{
			Status: 404,
			Body:   []byte(`{"error":"Model not found"}`),
		}
		gate := &fakeApprover{}
		tool := newRunTool(d, gate)
		if nonAdmin {
			tool.rt.Access = operations.Access{Role: types.TenantRoleContributor, EnforceRole: true}
		}
		result, err := tool.Execute(
			interactiveCtx(),
			runArgs("model.copy", map[string]any{"model_id": "source", "display_name": "Experiment"}),
		)
		require.NoError(t, err)
		assert.False(t, result.Success)
		assert.Empty(t, gate.requests)
		if nonAdmin {
			assert.Empty(t, d.calls)
		}
		for _, call := range d.calls {
			assert.Equal(t, "GET", call.call.Method)
		}
	}
}
