package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
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
		require.Equal(t, invoke.RoleTool, messages[1].Role)
		require.Equal(t, invoke.RoleTool, messages[2].Role)
		if vision {
			require.Len(t, messages, 4)
			require.Equal(t, invoke.RoleUser, messages[3].Role)
			var images []string
			for _, part := range messages[3].Content {
				if part.Image != nil {
					images = append(images, part.Image.URL)
				}
			}
			require.Equal(t, step.ToolCalls[0].Result.Images, images)
			require.Contains(t, messages[3].Text(), "untrusted tool evidence")
		} else {
			require.Len(t, messages, 3)
			require.Contains(t, messages[1].Text(), "cannot view")
			require.NotContains(t, messages[1].Text(), "YQ==")
		}
	}
	engine := &AgentEngine{imageDescriber: func(context.Context, []byte, string) (string, error) {
		return "A chart", nil
	}}
	messages := engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Text(), "A chart")
	require.Equal(t, `{"width":1}`, step.ToolCalls[0].Result.Output,
		"model-only fallback must not alter persisted output")
	engine.imageDescriber = func(context.Context, []byte, string) (string, error) {
		return "", errors.New("unavailable")
	}
	messages = engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Text(), "cannot view")
	engine.imageDescriber = func(context.Context, []byte, string) (string, error) { return "  ", nil }
	messages = engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Text(), "cannot view")
}
