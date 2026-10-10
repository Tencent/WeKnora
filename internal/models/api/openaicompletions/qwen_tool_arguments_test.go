package openaicompletions

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func replayedToolCall(arguments string) []api.Message {
	return []api.Message{{
		Role: "assistant",
		ToolCalls: []api.ToolCall{{
			ID: "call_weather",
			Function: api.FunctionCall{
				Name:      "get_weather",
				Arguments: arguments,
			},
		}},
	}}
}

func replayedArguments(t *testing.T, body map[string]any) any {
	t.Helper()
	toolCalls := body["messages"].([]any)[0].(map[string]any)["tool_calls"].([]any)
	return toolCalls[0].(map[string]any)["function"].(map[string]any)["arguments"]
}

func TestToolCallArguments_DefaultOpenAIString(t *testing.T) {
	c := newClient(t, nil)
	body := bodyJSON(t, c, replayedToolCall(`{"city":"Paris"}`), nil, false)
	assert.Equal(t, `{"city":"Paris"}`, replayedArguments(t, body))
}

func TestToolCallArguments_QwenCompatibilityObject(t *testing.T) {
	c := newClient(t, func(c *Config) {
		c.Settings.ToolCallArgumentsAsObject = true
	})
	body := bodyJSON(t, c, replayedToolCall(`{"city":"Paris","days":2}`), nil, false)
	assert.Equal(t, map[string]any{"city": "Paris", "days": float64(2)}, replayedArguments(t, body))
}

func TestToolCallArguments_QwenCompatibilityRejectsInvalidValues(t *testing.T) {
	c := newClient(t, func(c *Config) {
		c.Settings.ToolCallArgumentsAsObject = true
	})
	for _, tc := range []struct {
		name      string
		arguments string
		want      string
	}{
		{name: "empty", arguments: "", want: "arguments are empty"},
		{name: "whitespace", arguments: "  \n", want: "arguments are empty"},
		{name: "malformed", arguments: `{"city":`, want: "arguments are invalid JSON"},
		{name: "null", arguments: "null", want: "arguments must decode to a JSON object"},
		{name: "array", arguments: `[]`, want: "arguments must decode to a JSON object"},
		{name: "scalar", arguments: `"Paris"`, want: "arguments must decode to a JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.BuildRequestBody(replayedToolCall(tc.arguments), nil, false)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.want)
			assert.ErrorContains(t, err, "call_weather")
		})
	}
}
