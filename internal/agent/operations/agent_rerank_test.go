package operations

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentRerankConfiguration(t *testing.T) {
	update, ok := Builtin().Get("agent.update")
	require.True(t, ok)
	for _, id := range []string{"rerank-1", ""} {
		call, err := update.Build(map[string]json.RawMessage{
			"agent_id": raw("agent-1"), "rerank_model_id": raw(id),
		})
		require.NoError(t, err)
		assert.Equal(t, http.MethodPatch, call.Method)
		assert.Equal(t, "/api/v1/agents/agent-1", call.URI())
		assert.JSONEq(t, string(raw(map[string]string{"rerank_model_id": id})), string(call.Body))
	}
	_, err := update.Build(map[string]json.RawMessage{
		"agent_id": raw("agent-1"), "rerank_model_id": raw(42),
	})
	require.Error(t, err)

	// Both empty and configured values must reach the assistant, so it can
	// distinguish an incomplete setup from a saved choice without credentials.
	for _, id := range []string{"", "rerank-1"} {
		for _, operation := range []string{"agent.create", "agent.update", "agent.get", "agent.list"} {
			t.Run(operation+"/"+id, func(t *testing.T) {
				op, ok := Builtin().Get(operation)
				require.True(t, ok)
				agent := map[string]any{"id": "agent-1", "config": map[string]any{
					"model_id": "chat-1", "rerank_model_id": id, "api_key": "secret",
				}}
				var data any = agent
				if operation == "agent.list" {
					data = []any{agent}
				}
				out := projectJSON(t, op, string(raw(map[string]any{"data": data})))
				assert.Contains(t, out, `"rerank_model_id":`+string(raw(id)))
				assert.NotContains(t, out, "secret")
			})
		}
	}
}

func TestAgentCanDiscoverRerankModels(t *testing.T) {
	op, ok := Builtin().Get("model.list")
	require.True(t, ok)
	out := projectJSON(t, op, `{"data":[{
        "id":"rerank-1","name":"Ranker","type":"Rerank","parameters":{"api_key":"secret"}
    }]}`)
	assert.Contains(t, out, `"model_id":"rerank-1"`)
	assert.Contains(t, out, `"type":"Rerank"`)
	assert.NotContains(t, out, "secret")
}
