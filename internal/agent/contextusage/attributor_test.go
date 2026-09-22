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
			Name:        agenttools.ToolKnowledgeSearch,
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

// The bug this rework exists to fix: the system prompt is frozen for the whole
// turn, but proportional calibration made it climb every round as the
// conversation grew.
func TestFixedBucketsDoNotMoveWhenTheConversationGrows(t *testing.T) {
	a := newTestAttributor(t)
	tools := testTools()

	first := a.Attribute(testMessages(), tools, sections(), 5000)

	grown := append(testMessages(), chat.Message{
		Role: "user", Content: strings.Repeat("a much longer follow-up question. ", 500),
	})
	second := a.Attribute(grown, tools, sections(), 10000)

	require.Equal(t, first.SystemPrompt, second.SystemPrompt)
	require.Equal(t, first.Memory, second.Memory)
	require.Equal(t, first.Skills, second.Skills)
	require.Equal(t, first.Tools, second.Tools)
	require.Equal(t, first.MCP, second.MCP)
	require.Greater(t, second.Conversation, first.Conversation,
		"the growth must land on the variable buckets")
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
	require.False(t, IsMCPToolSchema(agenttools.ToolKnowledgeSearch))
}
