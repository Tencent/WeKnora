package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// calibrationRun drives calibrateEstimator the way runReActIteration does:
// each request is the transcript so far, and the provider reports a prompt
// count for it.
type calibrationRun struct {
	engine   *AgentEngine
	state    *types.AgentState
	messages []chat.Message
	tools    []chat.Tool
}

func newCalibrationRun(t *testing.T, opts ...testEngineOption) *calibrationRun {
	return &calibrationRun{
		engine: newTestEngine(t, &mockChat{}, opts...),
		state:  &types.AgentState{},
		messages: []chat.Message{
			{Role: "system", Content: "you are an agent"},
			{Role: "user", Content: "summarize the contract"},
		},
	}
}

// send reports prompt as the provider's count for the transcript as it stands.
func (r *calibrationRun) send(prompt int) {
	r.engine.lastSentMsgCount = len(r.messages)
	r.engine.calibrateEstimator(context.Background(), 1, r.messages, r.tools,
		types.TokenUsage{PromptTokens: prompt, TotalTokens: prompt}, r.state)
}

// appendRound adds a reply and a tool result and returns their raw estimate.
func (r *calibrationRun) appendRound(result string) int {
	appended := []chat.Message{
		{Role: "assistant", ToolCalls: []chat.ToolCall{{
			ID: "call", Type: "function",
			Function: chat.FunctionCall{Name: "search_knowledge", Arguments: `{"query":"条款"}`},
		}}},
		{Role: "tool", Name: "search_knowledge", ToolCallID: "call", Content: result},
	}
	r.messages = append(r.messages, appended...)
	estimated := 0
	for i := range appended {
		estimated += r.engine.rawEstimator.EstimateMessage(&appended[i])
	}
	return estimated
}

var chineseResult = strings.Repeat("合同第十二条约定，乙方应在验收合格后三十日内支付剩余款项。", 40)

// The scale is the provider's growth between two requests over the estimate
// of what was appended, and it is carried to the next turn on the usage.
func TestCalibrationLearnsTheScaleOfAppendedConversation(t *testing.T) {
	r := newCalibrationRun(t)
	r.send(500)
	appended := r.appendRound(chineseResult)
	r.send(500 + appended*6/10)

	require.InDelta(t, 0.6, r.engine.tokenEstimator.Scale(), 0.01)
	require.InDelta(t, 0.6, r.state.TurnUsage.ContextTokenScale, 0.01)
	require.Equal(t, 1.0, r.engine.rawEstimator.Scale(), "the measuring estimator stays raw")
}

// Tool schemas ride in every request, so they cancel out of the delta. A
// provider that renders them at half their raw size must not pull the
// conversation scale down with them: that would under-count conversation.
func TestCalibrationIgnoresHowToolSchemasAreRendered(t *testing.T) {
	r := newCalibrationRun(t)
	for i := range 40 {
		r.tools = append(r.tools, chat.Tool{Type: "function", Function: chat.FunctionDef{
			Name:        fmt.Sprintf("tool_%d", i),
			Description: strings.Repeat("does something useful. ", 40),
			Parameters:  []byte(`{"type":"object"}`),
		}})
	}
	schemas := r.engine.rawEstimator.EstimateTools(r.tools)
	base := r.engine.rawEstimator.EstimateMessages(r.messages)

	r.send(base + schemas/2)
	appended := r.appendRound(strings.Repeat("the payment is due within thirty days. ", 80))
	r.send(base + schemas/2 + appended)

	require.InDelta(t, 1.0, r.engine.tokenEstimator.Scale(), 0.01)
}

// Compaction or trimming rewrites messages the previous request sent, so the
// growth between the two requests no longer prices what was appended.
func TestCalibrationSkipsARequestAfterTheContextWasRewritten(t *testing.T) {
	r := newCalibrationRun(t)
	r.send(500)
	appended := r.appendRound(chineseResult)
	r.engine.contextRewrites++
	r.send(500 + appended*6/10)

	require.Equal(t, 1.0, r.engine.tokenEstimator.Scale())
	require.Zero(t, r.state.TurnUsage.ContextTokenScale, "nothing measured, nothing to carry forward")

	// The rewritten request is the new baseline for the next comparison.
	next := r.appendRound(chineseResult)
	r.send(500 + appended*6/10 + next*6/10)
	require.InDelta(t, 0.6, r.engine.tokenEstimator.Scale(), 0.01)
}

// A sample no tokenizer could produce is a usage report that does not mean
// what the delta assumes, such as a provider counting only uncached input.
func TestCalibrationSkipsImplausibleSamples(t *testing.T) {
	r := newCalibrationRun(t)
	r.send(5000)
	appended := r.appendRound(chineseResult)
	r.send(5000 + appended/10)

	require.Equal(t, 1.0, r.engine.tokenEstimator.Scale())
	require.Zero(t, r.state.TurnUsage.ContextTokenScale)
}

// Images are estimated at a fixed cost while providers bill them by tiles; a
// sample containing one would measure image pricing, not text.
func TestCalibrationSkipsSamplesWithImages(t *testing.T) {
	r := newCalibrationRun(t)
	r.send(500)
	appended := r.appendRound(chineseResult)
	r.messages = append(r.messages,
		chat.Message{Role: "user", Content: "see image", Images: []string{"https://x/a.png"}})
	r.send(500 + appended*6/10 + 800)

	require.Equal(t, 1.0, r.engine.tokenEstimator.Scale())
}

// Too little appended text is mostly per-message overhead; it must not
// replace the prior on its own.
func TestCalibrationWaitsForEnoughText(t *testing.T) {
	r := newCalibrationRun(t)
	r.send(500)
	appended := r.appendRound("ok")
	r.send(500 + appended/2)

	require.Equal(t, 1.0, r.engine.tokenEstimator.Scale())
}

// The previous turn's scale is where this turn starts, and compaction shares
// the estimator, so its cut point and summarizer budget use the same scale.
func TestEngineStartsFromThePreviousTurnsScale(t *testing.T) {
	engine := newTestEngine(t, &mockChat{}, func(cfg *types.AgentConfig) {
		cfg.ContextTokenScale = 0.6
	})
	require.InDelta(t, 0.6, engine.tokenEstimator.Scale(), 1e-9)

	text := chat.Message{Role: "user", Content: chineseResult}
	require.Less(t, engine.tokenEstimator.EstimateMessage(&text), engine.rawEstimator.EstimateMessage(&text))
}

// bigResultTool returns a long result, enough appended text to calibrate on.
type bigResultTool struct{ countingTool }

func (t *bigResultTool) Execute(context.Context, json.RawMessage) (*types.ToolResult, error) {
	return &types.ToolResult{Success: true, Output: chineseResult}, nil
}

// Some providers report a prompt count and no total. Calibration needs only
// the prompt count, and the scale it measures reaches the turn's usage even
// though no total was ever reported.
func TestExecuteCalibratesFromPromptCountsWithoutATotal(t *testing.T) {
	tool := &bigResultTool{countingTool: *newCountingTool("read_contract")}
	model := &mockChat{responses: []mockResponse{
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer,
			ToolCalls: []types.LLMToolCall{{
				ID: "c1", Type: "function",
				Function: types.FunctionCall{Name: "read_contract", Arguments: `{}`},
			}},
			Done: true, FinishReason: "tool_calls",
			Usage: &types.TokenUsage{PromptTokens: 1000},
		}}},
		{chunks: []types.StreamResponse{{
			ResponseType: types.ResponseTypeAnswer, Content: "付款期限为验收后三十日。",
			Done: true, FinishReason: "stop",
			Usage: &types.TokenUsage{PromptTokens: 1000 + 700},
		}}},
	}}
	engine := newTestEngine(t, model)
	engine.toolRegistry = agenttools.NewToolRegistry()
	engine.toolRegistry.RegisterTool(tool)

	state, err := engine.Execute(context.Background(), "session", "message", "付款期限是多久", nil)
	require.NoError(t, err)
	require.InDelta(t, 0.6, state.TurnUsage.ContextTokenScale, 0.1)
	usage := turnUsage(state)
	require.NotNil(t, usage, "a scale is persisted even when no total was reported")
	require.InDelta(t, 0.6, usage.ContextTokenScale, 0.1)
}

// A turn that measures nothing (one request, no tool call) passes on the scale
// it started from, so the newest turn always carries the latest known scale.
func TestExecuteCarriesTheStartingScaleForward(t *testing.T) {
	model := &mockChat{responses: []mockResponse{{chunks: []types.StreamResponse{{
		ResponseType: types.ResponseTypeAnswer, Content: "你好", Done: true, FinishReason: "stop",
	}}}}}
	engine := newTestEngine(t, model, func(cfg *types.AgentConfig) { cfg.ContextTokenScale = 0.7 })
	engine.toolRegistry = agenttools.NewToolRegistry()

	state, err := engine.Execute(context.Background(), "session", "message", "你好", nil)
	require.NoError(t, err)
	usage := turnUsage(state)
	require.NotNil(t, usage)
	require.InDelta(t, 0.7, usage.ContextTokenScale, 1e-9)
}
