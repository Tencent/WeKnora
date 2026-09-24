package contextusage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func newTestAttributor(t *testing.T) *Attributor {
	t.Helper()
	est, err := token.NewEstimator()
	require.NoError(t, err)
	return New(est, 200000, 180000)
}

func testTools() []chat.Tool {
	return []chat.Tool{
		{Type: "function", Function: chat.FunctionDef{
			Name:        agenttools.ToolSearchKnowledge,
			Description: "Search bound knowledge bases",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		}},
		{Type: "function", Function: chat.FunctionDef{
			Name:        agenttools.ToolDiscoverMCPTools,
			Description: "Directory of MCP servers and their tools. " + strings.Repeat("x", 800),
			Parameters:  json.RawMessage(`{"type":"object"}`),
		}},
		{Type: "function", Function: chat.FunctionDef{
			Name:        "mcp_weather_getforecast",
			Description: "Get a weather forecast from the weather MCP server",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
	}
}

func testMessages() []chat.Message {
	return []chat.Message{
		{Role: "system", Content: "You are a helpful agent.\n\nMEMORY_SECTION\n\nSKILLS_SECTION"},
		{Role: "user", Content: "summarize the attached report in detail"},
		{Role: "assistant", Content: "calling tools", ReasoningContent: strings.Repeat("thinking hard. ", 100)},
		{Role: "tool", Name: "knowledge_search", Content: strings.Repeat("retrieved passage. ", 200)},
	}
}

func sections() map[string]int {
	return map[string]int{SectionMemory: 5, SectionSkills: 7}
}

func sum(u types.ContextUsage) int {
	return u.SystemPrompt + u.Memory + u.Skills + u.Tools + u.MCP +
		u.Conversation + u.Reasoning + u.ToolResults
}

func TestAttributeSplitsEightBuckets(t *testing.T) {
	a := newTestAttributor(t)

	got := a.Attribute(testMessages(), testTools(), sections(), 0)

	require.Equal(t, 200000, got.Window)
	require.Equal(t, 180000, got.Threshold)
	require.True(t, got.Estimated, "no prompt_tokens means the numbers are a guess")
	require.Greater(t, got.SystemPrompt, 0)
	require.Equal(t, 5, got.Memory)
	require.Equal(t, 7, got.Skills)
	require.Greater(t, got.Tools, 0)
	require.Greater(t, got.MCP, got.Tools, "the MCP catalog description outweighs a small builtin schema")
	require.Greater(t, got.Conversation, 0)
	require.Greater(t, got.Reasoning, 0)
	require.Greater(t, got.ToolResults, 0)
	require.Equal(t, sum(got), got.Total)
}

func TestAttributeChargesReasoningAndToolResultsSeparately(t *testing.T) {
	a := newTestAttributor(t)
	est, err := token.NewEstimator()
	require.NoError(t, err)

	msgs := testMessages()
	got := a.Attribute(msgs, nil, nil, 0)

	require.Equal(t, est.EstimateString(msgs[2].ReasoningContent), got.Reasoning)
	require.Equal(t, est.EstimateMessage(&msgs[3]), got.ToolResults)
	require.Zero(t, got.Memory, "no section table means nothing to charge")
	require.Zero(t, got.Skills)
}

// A growth too small to calibrate must not move the fixed prefix. The old
// proportional scaler climbed every round; the lock exists to stop that.
func TestFixedBucketsDoNotMoveOnAGrowthTooSmallToCalibrate(t *testing.T) {
	a := newTestAttributor(t)
	tools := testTools()

	first := a.Attribute(testMessages(), tools, sections(), 5000)
	nudged := append(testMessages(), chat.Message{Role: "user", Content: "ok"})
	second := a.Attribute(nudged, tools, sections(), 5100)

	require.Equal(t, 1.0, a.kFixed)
	require.Equal(t, first.SystemPrompt, second.SystemPrompt)
	require.Equal(t, first.Memory, second.Memory)
	require.Equal(t, first.Skills, second.Skills)
	require.Equal(t, first.Tools, second.Tools)
	require.Equal(t, first.MCP, second.MCP)
}

// kFixed is measured on the second priced round. It has to reach the buckets
// on that same round — a turn's prefix usually never changes, so deferring
// the scale until the next fingerprint change means it is never reported.
// Once applied, more conversation must not move the prefix again.
func TestCalibratedFixedScaleReachesTheBucketsAndThenHolds(t *testing.T) {
	a := newTestAttributor(t)
	tools := testTools()

	first := a.Attribute(testMessages(), tools, sections(), 5000)
	grown := append(testMessages(), chat.Message{
		Role: "user", Content: strings.Repeat("a much longer follow-up question. ", 500),
	})
	second := a.Attribute(grown, tools, sections(), 20000)

	require.NotEqual(t, 1.0, a.kFixed, "a large delta measures the prefix scale")
	require.Equal(t, scale(first.SystemPrompt, a.kFixed), second.SystemPrompt)
	require.Equal(t, scale(first.Memory, a.kFixed), second.Memory)
	require.Equal(t, scale(first.Skills, a.kFixed), second.Skills)
	require.Equal(t, scale(first.Tools, a.kFixed), second.Tools)
	require.Equal(t, scale(first.MCP, a.kFixed), second.MCP)
	require.Equal(t, 20000, second.Total)
	require.Equal(t, 20000, sum(second))

	grownAgain := append(grown, chat.Message{
		Role: "user", Content: strings.Repeat("still growing. ", 500),
	})
	third := a.Attribute(grownAgain, tools, sections(), 40000)
	require.Equal(t, second.SystemPrompt, third.SystemPrompt)
	require.Equal(t, second.Memory, third.Memory)
	require.Equal(t, second.Skills, third.Skills)
	require.Equal(t, second.Tools, third.Tools)
	require.Equal(t, second.MCP, third.MCP)
	require.Greater(t, third.Conversation, second.Conversation)
	require.Equal(t, 40000, sum(third))
}

// A short dialogue must not be blamed for a gap that is larger than any
// plausible tokenizer error on that dialogue. The gap belongs to the prefix.
func TestShortConversationDoesNotAbsorbAPrefixGap(t *testing.T) {
	a := newTestAttributor(t)

	msgs := []chat.Message{
		{Role: "system", Content: strings.Repeat("你是一个智能助手，请严格遵守以下规则。", 60)},
		{Role: "user", Content: "你好"},
	}
	fixedEst, varEst := a.estimate(msgs, nil, nil)
	require.Greater(t, fixedEst.sum(), varEst.sum())
	require.Less(t, varEst.sum(), minCalibrationDelta)

	prompt := 2*fixedEst.sum() + varEst.sum()
	got := a.Attribute(msgs, nil, nil, prompt)

	variableCap := scale(varEst.sum(), maxVarScale)
	require.LessOrEqual(t, got.Conversation+got.Reasoning+got.ToolResults, variableCap)
	require.Greater(t, got.SystemPrompt, fixedEst.SystemPrompt,
		"the implausible gap is prefix error, so it stays on the system prompt")
	require.Equal(t, prompt, got.Total)
	require.Equal(t, prompt, sum(got))
}

// Adding tool schemas between rounds makes the prompt delta a mix of new
// schemas and new dialogue. That sample must not become the turn's frozen scale.
func TestCalibrationSkipsWhenTheFixedPrefixChanged(t *testing.T) {
	a := newTestAttributor(t)
	base := testMessages()
	a.Attribute(base, testTools(), sections(), 8000)

	grown := append(append([]chat.Message{}, base...), chat.Message{
		Role: "user", Content: strings.Repeat("a long follow-up question. ", 2000),
	})
	a.Attribute(grown, nil, sections(), 30000)
	require.Equal(t, 1.0, a.kFixed)
	require.False(t, a.calibrated)

	grownAgain := append(grown, chat.Message{
		Role: "tool", Content: strings.Repeat("more tool output. ", 2000),
	})
	a.Attribute(grownAgain, nil, sections(), 60000)
	require.True(t, a.calibrated)
	require.NotEqual(t, 1.0, a.kFixed)
}

// Memory and skills are both slices of one system message. When their section
// estimates do not fit, each keeps its share instead of skills going to zero.
func TestMemoryAndSkillsShareATightSystemBudget(t *testing.T) {
	a := newTestAttributor(t)
	est, err := token.NewEstimator()
	require.NoError(t, err)

	msgs := []chat.Message{
		{Role: "system", Content: "hi"},
		{Role: "user", Content: "yo"},
	}
	sys := est.EstimateMessage(&msgs[0])
	got := a.Attribute(msgs, nil, map[string]int{SectionMemory: sys, SectionSkills: sys}, 0)

	require.Greater(t, got.Memory, 0)
	require.Greater(t, got.Skills, 0)
	require.Equal(t, sys, got.SystemPrompt+got.Memory+got.Skills)
}

func TestTotalAlwaysEqualsPromptTokens(t *testing.T) {
	a := newTestAttributor(t)

	for _, prompt := range []int{1000, 7500, 250} {
		got := a.Attribute(testMessages(), testTools(), sections(), prompt)
		require.Equal(t, prompt, got.Total)
		require.Equal(t, prompt, sum(got), "buckets must reconcile with the provider count")
		require.False(t, got.Estimated)
	}
}

func TestFixedEstimateShrinksWhenItOvershootsTheRequest(t *testing.T) {
	a := newTestAttributor(t)

	got := a.Attribute(testMessages(), testTools(), sections(), 50)

	require.Equal(t, 50, got.Total)
	require.Equal(t, 50, sum(got))
	require.Zero(t, got.Conversation)
	require.Zero(t, got.Reasoning)
	require.Zero(t, got.ToolResults)
	require.Greater(t, got.SystemPrompt, 0)
}

// When memory+skills consume the whole system message, SystemPrompt is 0 and
// cannot absorb a negative rounding remainder after scaling. Shrink must still
// leave the eight buckets summing to promptTokens.
func TestShrinkedFixedBucketsSumToPromptWhenSystemPromptCannotAbsorb(t *testing.T) {
	a := newTestAttributor(t)
	est, err := token.NewEstimator()
	require.NoError(t, err)

	msgs := []chat.Message{
		{Role: "system", Content: "You are a helpful agent."},
		{Role: "user", Content: "hi"},
	}
	sys := est.EstimateMessage(&msgs[0])
	secs := map[string]int{SectionMemory: sys, SectionSkills: 0}
	tools := testTools()

	estimated := a.Attribute(msgs, tools, secs, 0)
	require.Zero(t, estimated.SystemPrompt)
	require.Greater(t, estimated.Tools+estimated.MCP, 0)
	require.Greater(t, estimated.Memory+estimated.Skills+estimated.Tools+estimated.MCP, 12)

	a.kFixed = 1.4 // poisoned scale; shrink path must clear it
	got := a.Attribute(msgs, tools, secs, 12)

	require.Equal(t, 12, got.Total)
	require.Equal(t, 12, sum(got), "buckets must reconcile after shrink")
	require.Zero(t, got.Conversation)
	require.Zero(t, got.Reasoning)
	require.Zero(t, got.ToolResults)
	require.GreaterOrEqual(t, got.SystemPrompt, 0)
	require.GreaterOrEqual(t, got.Memory, 0)
	require.GreaterOrEqual(t, got.Skills, 0)
	require.GreaterOrEqual(t, got.Tools, 0)
	require.GreaterOrEqual(t, got.MCP, 0)
	require.Equal(t, 1.0, a.kFixed, "overshoot must reset kFixed so the lock cannot poison the next round")

	// Changed fingerprint: unlocked fixed estimate, not a 1.4× scale of the prior lock.
	next := a.Attribute(msgs, nil, secs, 0)
	require.Zero(t, next.Tools)
	require.Zero(t, next.MCP)
	require.Equal(t, 1.0, a.kFixed)
}

func TestShrinkToAbsorbsRoundingDeficitFromLargestBucket(t *testing.T) {
	// Two equal positive buckets round up past budget; SystemPrompt is 0 so the
	// old clamp left sum > budget.
	got := shrinkTo(fixed{Tools: 100, MCP: 100}, 3)
	require.Equal(t, 3, got.sum())
	require.GreaterOrEqual(t, got.SystemPrompt, 0)
	require.GreaterOrEqual(t, got.Tools, 0)
	require.GreaterOrEqual(t, got.MCP, 0)
	require.Zero(t, got.SystemPrompt)
}

func TestToolChangeRelocksTheFixedBuckets(t *testing.T) {
	a := newTestAttributor(t)

	withTools := a.Attribute(testMessages(), testTools(), sections(), 5000)
	withoutTools := a.Attribute(testMessages(), nil, sections(), 5000)

	require.Greater(t, withTools.Tools, 0)
	require.Zero(t, withoutTools.Tools, "dropping the schemas must drop the bucket")
	require.Zero(t, withoutTools.MCP)
	require.Greater(t, withoutTools.Conversation, withTools.Conversation,
		"the freed budget belongs to the variable buckets")
}

func TestRecalibrateReusesTheLastEstimate(t *testing.T) {
	a := newTestAttributor(t)

	estimated := a.Attribute(testMessages(), testTools(), sections(), 0)
	require.True(t, estimated.Estimated)

	measured := a.Recalibrate(6000)

	require.False(t, measured.Estimated)
	require.Equal(t, 6000, measured.Total)
	require.Equal(t, 6000, sum(measured))
	require.Equal(t, estimated.Tools, measured.Tools,
		"kFixed is still 1.0 on the first round, so the schema estimate carries through")
}

func TestVariableScaleCalibrationIgnoresTinyDeltas(t *testing.T) {
	a := newTestAttributor(t)
	tools := testTools()

	a.Attribute(testMessages(), tools, sections(), 5000)
	nudged := append(testMessages(), chat.Message{Role: "user", Content: "ok"})
	a.Attribute(nudged, tools, sections(), 5100)

	require.Equal(t, 1.0, a.kFixed, "a delta under the floor must not move the scale")
}

func TestVariableScaleCalibrationRunsOnALargeDelta(t *testing.T) {
	a := newTestAttributor(t)
	tools := testTools()

	a.Attribute(testMessages(), tools, sections(), 5000)
	grown := append(testMessages(), chat.Message{
		Role: "user", Content: strings.Repeat("a long follow-up question. ", 2000),
	})
	a.Attribute(grown, tools, sections(), 9000)

	require.NotEqual(t, 1.0, a.kFixed, "a large delta measures the tokenizer bias")
	require.GreaterOrEqual(t, a.kFixed, minFixedScale)
	require.LessOrEqual(t, a.kFixed, maxFixedScale)
}

func TestIsMCPToolSchema(t *testing.T) {
	require.True(t, IsMCPToolSchema(agenttools.ToolDiscoverMCPTools))
	require.True(t, IsMCPToolSchema(agenttools.ToolCallMCPTool))
	require.True(t, IsMCPToolSchema("mcp_weather_getforecast"))
	require.False(t, IsMCPToolSchema(agenttools.ToolSearchKnowledge))
}
