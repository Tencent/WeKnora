package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
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
		require.Contains(t, string(wire), "Image 1: chunk chunk://2.")
		require.Contains(t, string(wire), "Image 2: chunk chunk://1.")
	}
}

func TestParallelToolImagesShareOneLabelledMessage(t *testing.T) {
	var calls []types.ToolCall
	for i := 1; i <= 3; i++ {
		calls = append(calls, types.ToolCall{
			ID: fmt.Sprintf("img-%d", i), Name: tools.ToolReadFile,
			Result: &types.ToolResult{
				Success: true, Output: "image", Images: []string{fmt.Sprintf("data:image/png;base64,%d", i)},
			},
		})
	}
	step := types.AgentStep{ToolCalls: calls}
	engine := &AgentEngine{config: &types.AgentConfig{ChatModelSupportsVision: true}}

	messages := tools.SanitizeMessages(engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step))

	last := messages[len(messages)-1]
	require.Equal(t, "user", last.Role)
	require.Equal(t,
		[]string{"data:image/png;base64,1", "data:image/png;base64,2", "data:image/png;base64,3"}, last.Images)
	require.Contains(t, last.Content, "[attached images 2-2] Images returned by tool read_file (call img-2)")
}

func TestParallelToolImagesAreDescribedTogetherWithImageNumbers(t *testing.T) {
	step := types.AgentStep{ToolCalls: []types.ToolCall{
		{ID: "pair", Name: tools.ToolReadFile, Result: &types.ToolResult{
			Success: true, Output: "images 3-4",
			Images: []string{"data:image/png;base64,Mw==", "data:image/png;base64,NA=="},
			Data:   map[string]interface{}{"image_numbers": []int{3, 4}},
		}},
		{ID: "single", Name: tools.ToolReadFile, Result: &types.ToolResult{
			Success: true, Output: "image 7",
			Images: []string{"data:image/png;base64,Nw=="},
			Data:   map[string]interface{}{"image_numbers": []int{7}},
		}},
	}}
	var described atomic.Int32
	engine := &AgentEngine{config: &types.AgentConfig{}}
	engine.SetImageDescriber(func(_ context.Context, data []byte, _ string) (string, error) {
		described.Add(1)
		if string(data) == "4" {
			return "", errors.New("vlm timeout")
		}
		return "figure " + string(data), nil
	})

	messages := engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Len(t, messages, 3)
	require.EqualValues(t, 3, described.Load())
	require.Contains(t, messages[1].Content, "[image #3] figure 3\n[image #4] could not be described")
	require.Contains(t, messages[2].Content, "[image #7] figure 7")

	engine.SetImageDescriber(func(context.Context, []byte, string) (string, error) {
		return "", errors.New("down")
	})
	messages = engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Content, "cannot view")
	require.Contains(t, messages[2].Content, "cannot view")
}

func TestToolImagesDescribedDuringCallAreReusedAndShown(t *testing.T) {
	result := &types.ToolResult{
		Success: true, Output: "image 2",
		Images: []string{"data:image/png;base64,Mg=="},
		Data:   map[string]interface{}{"display_type": "image_view", "image_numbers": []int{2}},
	}
	var described atomic.Int32
	engine := &AgentEngine{config: &types.AgentConfig{}}
	engine.SetImageDescriber(func(context.Context, []byte, string) (string, error) {
		described.Add(1)
		return "a bar chart", nil
	})

	engine.describeToolImages(t.Context(), result)
	require.Equal(t, []string{"a bar chart"}, result.Data["image_descriptions"])
	step := types.AgentStep{ToolCalls: []types.ToolCall{{ID: "img", Name: tools.ToolReadFile, Result: result}}}
	messages := engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Content, "[image #2] a bar chart")
	require.EqualValues(t, 1, described.Load(), "descriptions from the tool call must not be regenerated")

	visionResult := &types.ToolResult{Success: true, Images: result.Images, Data: map[string]interface{}{}}
	engine.config.ChatModelSupportsVision = true
	engine.describeToolImages(t.Context(), visionResult)
	require.Nil(t, visionResult.ImageDescriptions)
	require.NotContains(t, visionResult.Data, "image_descriptions")
}

func TestToolImagePromptFollowsRequestedDetailAndFocus(t *testing.T) {
	var prompts []string
	engine := &AgentEngine{config: &types.AgentConfig{}}
	engine.SetImageDescriber(func(_ context.Context, _ []byte, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		return "ok", nil
	})
	image := []string{"data:image/png;base64,YQ=="}

	engine.describeToolImages(t.Context(), &types.ToolResult{Success: true, Images: image})
	engine.describeToolImages(t.Context(), &types.ToolResult{
		Success: true, Images: image, ImageDetail: types.ImageDetailFull, ImageFocus: "the Q3 revenue",
	})
	require.Len(t, prompts, 2)
	require.Equal(t, toolImageBriefPrompt, prompts[0])
	require.True(t, strings.HasPrefix(prompts[1], toolImageFullPrompt))
	require.Contains(t, prompts[1], "looking for: the Q3 revenue")
	require.Len(t, []rune(toolImagePrompt("", strings.Repeat("x", 1000))),
		len([]rune(toolImageBriefPrompt))+len("\nThe assistant is looking for: ")+toolImageFocusRunes+
			len("\nAddress that first, saying plainly if the image does not show it."))
}

func TestAttachmentImagesReachVisionOrVLMWithoutPersistingBytes(t *testing.T) {
	step := types.AgentStep{ToolCalls: []types.ToolCall{{
		ID: "attachment-images", Name: tools.ToolReadFile,
		Result: &types.ToolResult{
			Success: true, Output: "Attachment images 1-1",
			Images: []string{"data:image/png;base64,YQ=="},
			Data:   map[string]interface{}{"path": "attachment://att_1/images/1", "total_images": 1},
		},
	}}}
	engine := &AgentEngine{config: &types.AgentConfig{ChatModelSupportsVision: true}}
	messages := engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Len(t, messages, 3)
	require.Equal(t, step.ToolCalls[0].Result.Images, messages[2].Images)
	engine.config.ChatModelSupportsVision = false
	engine.SetImageDescriber(func(_ context.Context, data []byte, _ string) (string, error) {
		require.Equal(t, []byte("a"), data)
		return "A rising revenue chart", nil
	})
	messages = engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Len(t, messages, 2)
	require.Contains(t, messages[1].Content, "A rising revenue chart")
	stored := tools.SanitizeAgentStepsForStorage([]types.AgentStep{step})
	require.Empty(t, stored[0].ToolCalls[0].Result.Images)
	require.NotEmpty(t, step.ToolCalls[0].Result.Images, "storage sanitization must not mutate live input")
}

func TestToolImagesReuseApplicationDescriptions(t *testing.T) {
	engine := newTestEngine(t, &mockChat{})
	engine.SetImageDescriber(func(context.Context, []byte, string) (string, error) {
		t.Fatal("attachment description must not run twice")
		return "", nil
	})
	result := &types.ToolResult{
		Success: true, Images: []string{"data:image/png;base64,aGVsbG8="},
		ImageDescriptions: []string{"saved observation"}, Data: map[string]interface{}{},
	}
	engine.describeToolImages(context.Background(), result)
	require.Equal(t, []string{"saved observation"}, result.Data["image_descriptions"])
	result.ImageDescriptions = []string{""} // A failed application attempt also must not double its timeout.
	engine.describeToolImages(context.Background(), result)
	require.Equal(t, []string{""}, result.Data["image_descriptions"])
}

func TestChartDescriptionsPreservePairsAndRequestVerification(t *testing.T) {
	const rows = "华东 | Q1: 102 | Q2: 148 | 万元\n华南 | Q1: 94 | Q2: 116 | 万元"
	engine := &AgentEngine{config: &types.AgentConfig{}}
	engine.SetImageDescriber(func(_ context.Context, _ []byte, prompt string) (string, error) {
		require.Contains(t, prompt, "category")
		require.Contains(t, prompt, "series/legend and unit")
		require.Contains(t, prompt, "axis tick labels")
		require.Contains(t, prompt, "looking for: compare Q1 and Q2 by region")
		return rows, nil
	})
	result := &types.ToolResult{
		Success: true, Images: []string{"data:image/png;base64,YQ=="},
		ImageDetail: types.ImageDetailFull, ImageFocus: "compare Q1 and Q2 by region",
		Data: map[string]interface{}{"image_numbers": []int{2}},
	}
	engine.describeToolImages(t.Context(), result)
	require.Equal(t, []string{rows}, result.Data["image_descriptions"])
	step := types.AgentStep{ToolCalls: []types.ToolCall{{ID: "chart", Name: tools.ToolReadFile, Result: result}}}
	messages := engine.appendToolImages(t.Context(), engine.appendToolResults(nil, step), step)
	require.Contains(t, messages[1].Content, "[image #2] "+rows)
	require.Contains(t, messages[1].Content, "totals disagree, re-read")
	require.Contains(t, messages[1].Content, "before calculating or attributing an inconsistency to the document")
}
