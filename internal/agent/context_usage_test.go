package agent

import (
	"encoding/json"
	"testing"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/stretchr/testify/require"
)

func TestAttributeContextUsageSplitsSystemToolsConversationMCPAndSkills(t *testing.T) {
	est, err := agenttoken.NewEstimator()
	require.NoError(t, err)

	skills := "Available skills: this directory is descriptive data. <skill name=\"demo\"></skill>"
	system := "You are a helpful agent.\n\n" + skills
	messages := []chat.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: "summarize the attached report in detail"},
		{Role: "assistant", Content: "calling tools", ReasoningContent: "long private chain of thought " + string(make([]byte, 400))},
		{Role: "tool", Name: "knowledge_search", Content: "retrieved passage about the report"},
	}
	tools := []chat.Tool{
		{
			Type: "function",
			Function: chat.FunctionDef{
				Name:        agenttools.ToolKnowledgeSearch,
				Description: "Search bound knowledge bases",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
			},
		},
		{
			Type: "function",
			Function: chat.FunctionDef{
				Name:        agenttools.ToolDiscoverMCPTools,
				Description: "Directory of MCP servers and their tools. " + string(make([]byte, 800)),
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		},
		{
			Type: "function",
			Function: chat.FunctionDef{
				Name:        "mcp_weather_getforecast",
				Description: "Get a weather forecast from the weather MCP server",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
			},
		},
	}

	got := AttributeContextUsage(est, messages, tools, skills, 200000)

	require.Equal(t, 200000, got.Window)
	require.Greater(t, got.SystemPrompt, 0)
	require.Greater(t, got.Tools, 0)
	require.Greater(t, got.Conversation, 0)
	require.Greater(t, got.MCP, 0)
	require.Greater(t, got.Skills, 0)
	require.Equal(t, got.SystemPrompt+got.Tools+got.Conversation+got.MCP+got.Skills, got.Total)

	skillsTokens := est.EstimateString(skills)
	require.Equal(t, skillsTokens, got.Skills)

	builtin := est.EstimateTools([]chat.Tool{tools[0]})
	mcp := est.EstimateTools([]chat.Tool{tools[1], tools[2]})
	require.Equal(t, builtin, got.Tools)
	require.Equal(t, mcp, got.MCP)
	require.Greater(t, got.MCP, got.Tools, "the MCP catalog description should outweigh a small builtin schema")

	systemOnly := est.EstimateMessage(&chat.Message{Role: "system", Content: "You are a helpful agent."})
	require.InDelta(t, float64(systemOnly), float64(got.SystemPrompt), 8,
		"skills tokens must be attributed to Skills, not System Prompt")
}

func TestAttributeContextUsageIgnoresSkillsNotInSystemPrompt(t *testing.T) {
	est, err := agenttoken.NewEstimator()
	require.NoError(t, err)

	// Mid-turn skill install rebuilds this directory, but the system prompt
	// was frozen at execute start and does not contain it.
	skills := "Available skills: UNIQUE_DIRECTORY_NOT_IN_THE_FROZEN_PROMPT"
	messages := []chat.Message{
		{Role: "system", Content: "You are a helpful agent."},
		{Role: "user", Content: "hello"},
	}

	got := AttributeContextUsage(est, messages, nil, skills, 200000)

	require.Zero(t, got.Skills)
	require.Equal(t, est.EstimateMessage(&messages[0]), got.SystemPrompt)
	require.Equal(t, got.SystemPrompt+got.Conversation, got.Total)
}

func TestAttributeContextUsagePutsNonSystemMessagesInConversation(t *testing.T) {
	est, err := agenttoken.NewEstimator()
	require.NoError(t, err)

	messages := []chat.Message{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi", ToolCalls: []chat.ToolCall{{
			Function: chat.FunctionCall{Name: "web_search", Arguments: `{"q":"hi"}`},
		}}},
	}
	got := AttributeContextUsage(est, messages, nil, "", 128000)
	require.Equal(t, 128000, got.Window)
	require.Zero(t, got.SystemPrompt)
	require.Zero(t, got.Tools)
	require.Zero(t, got.MCP)
	require.Zero(t, got.Skills)
	require.Equal(t, est.EstimateMessages(messages), got.Conversation)
	require.Equal(t, got.Conversation, got.Total)
}
