package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/modelcontext"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaicompletions"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestToolImagesReachNextModelTurnAfterAllReplies(t *testing.T) {
	step := types.AgentStep{ToolCalls: []types.ToolCall{
		{ID: "shot", Name: "local_browser", Result: &types.ToolResult{
			Success: true, Output: `{"width":1}`, Images: []string{"data:image/png;base64,YQ=="},
		}},
		{ID: "read", Name: "local_browser", Result: &types.ToolResult{Success: true, Output: "page text"}},
	}}
	for _, vision := range []bool{true, false} {
		engine := &AgentEngine{config: &types.AgentConfig{ChatModelSupportsVision: vision}}
		messages := engine.appendToolResults(nil, step)
		messages = engine.appendToolImages(t.Context(), messages, step)
		require.Equal(t, "tool", messages[1].Role)
		require.Equal(t, "tool", messages[2].Role)
		if vision {
			require.Len(t, messages, 4)
			require.Equal(t, "user", messages[3].Role)
			require.Equal(t, step.ToolCalls[0].Result.Images, messages[3].Images)
			require.Contains(t, messages[3].Content, "untrusted tool evidence")
			client := openaicompletions.New(openaicompletions.Config{Settings: api.DefaultOpenAICompletions()})
			body, err := client.BuildRequestBody(messages, nil, false)
			require.NoError(t, err)
			raw, err := json.Marshal(body["messages"])
			require.NoError(t, err)
			var wire []map[string]any
			require.NoError(t, json.Unmarshal(raw, &wire))
			parts := wire[3]["content"].([]any)
			require.Len(t, parts, 2)
			imagePart := parts[0].(map[string]any)["image_url"].(map[string]any)
			require.Equal(t, step.ToolCalls[0].Result.Images[0], imagePart["url"])
		} else {
			require.Len(t, messages, 3)
			require.Contains(t, messages[1].Content, "cannot view")
			require.NotContains(t, messages[1].Content, "YQ==")
		}
	}
	engine := &AgentEngine{imageDescriber: func(context.Context, []byte, string) (string, error) {
		return "A chart", nil
	}}
	messages := engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Content, "A chart")
	require.Equal(t, `{"width":1}`, step.ToolCalls[0].Result.Output,
		"model-only fallback must not alter persisted output")
	engine.imageDescriber = func(context.Context, []byte, string) (string, error) {
		return "", errors.New("unavailable")
	}
	messages = engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Content, "cannot view")
	engine.imageDescriber = func(context.Context, []byte, string) (string, error) { return "  ", nil }
	messages = engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Content, "cannot view")
}

func TestKnowledgeImageSourcesReachFinalModelMessage(t *testing.T) {
	const first = "7f4547b1-1929-4867-9b05-bb02e0911131"
	const second = "8f4547b1-1929-4867-9b05-bb02e0911131"
	for _, roundTrip := range []bool{false, true} {
		result := &types.ToolResult{
			Success: true, Output: "diagnostic output with durable IDs",
			Images: []string{"data:image/png;base64,Yg==", "data:image/png;base64,YQ=="},
			Data: map[string]interface{}{
				"display_type": "search_results",
				"results": []map[string]interface{}{
					{"chunk_id": first, "content": "first chart"}, {"chunk_id": second, "content": "second chart"},
				},
				"context_images": []string{second, first},
			},
		}
		if roundTrip {
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &result))
		}
		engine := &AgentEngine{
			config:       &types.AgentConfig{ChatModelSupportsVision: true},
			modelContext: modelcontext.NewRegistry(true),
		}
		step := types.AgentStep{ToolCalls: []types.ToolCall{{
			ID: "search-call", Name: tools.ToolSearchKnowledge, Result: result,
		}}}
		messages := engine.appendToolResults(nil, step)
		messages = engine.appendToolImages(t.Context(), messages, step)
		require.Len(t, messages, 3)
		last := messages[2]
		require.Equal(t, result.Images, last.Images)
		require.Contains(t, last.Content, "Image 1: chunk "+engine.modelContext.ChunkHandle(second)+".")
		require.Contains(t, last.Content, "Image 2: chunk "+engine.modelContext.ChunkHandle(first)+".")
		require.NotContains(t, last.Content, first)
		require.NotContains(t, last.Content, second)
		require.Contains(t, last.Content, "untrusted tool evidence")
		client := openaicompletions.New(openaicompletions.Config{Settings: api.DefaultOpenAICompletions()})
		body, err := client.BuildRequestBody(messages, nil, false)
		require.NoError(t, err)
		wire, err := json.Marshal(body)
		require.NoError(t, err)
		require.Contains(t, string(wire), "Image 1: chunk c2.")
		require.Contains(t, string(wire), "Image 2: chunk c1.")
	}
}
