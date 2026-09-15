package agent

import (
	"strings"

	agenttoken "github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// AttributeContextUsage estimates how the next (or last) LLM request spends
// its prompt budget: system text, builtin tool schemas, conversation,
// MCP schemas, and the skills directory injected into the system prompt.
func AttributeContextUsage(
	est *agenttoken.Estimator,
	messages []chat.Message,
	tools []chat.Tool,
	skillsContent string,
	window int,
) types.ContextUsage {
	usage := types.ContextUsage{Window: window}
	if usage.Window <= 0 {
		usage.Window = types.DefaultMaxContextTokens
	}
	if est == nil {
		return usage
	}

	var builtin, mcp []chat.Tool
	for _, tool := range tools {
		if isMCPToolSchema(tool.Function.Name) {
			mcp = append(mcp, tool)
			continue
		}
		builtin = append(builtin, tool)
	}
	usage.Tools = est.EstimateTools(builtin)
	usage.MCP = est.EstimateTools(mcp)

	skillsNeedle := strings.TrimSpace(skillsContent)
	skillsTokens := 0
	if skillsNeedle != "" {
		skillsTokens = est.EstimateString(skillsContent)
	}

	messageSum := 0
	skillsCharged := false
	for i := range messages {
		msg := &messages[i]
		msgTokens := est.EstimateMessage(msg)
		messageSum += msgTokens
		if msg.Role == "system" {
			sys := msgTokens
			if !skillsCharged && skillsTokens > 0 && strings.Contains(msg.Content, skillsNeedle) {
				sys -= skillsTokens
				if sys < 0 {
					sys = 0
				}
				usage.Skills = skillsTokens
				skillsCharged = true
			}
			usage.SystemPrompt += sys
			continue
		}
		usage.Conversation += msgTokens
	}
	if len(messages) > 0 {
		if tail := est.EstimateMessages(messages) - messageSum; tail > 0 {
			usage.Conversation += tail
		}
	}
	usage.RecalcTotal()
	return usage
}

// snapshotContextUsage records the prompt mix of the request that is about
// to be sent (or was just sent). promptTokens calibrates buckets to the
// provider's count when one is available; 0 leaves the estimate as-is.
func (e *AgentEngine) snapshotContextUsage(
	state *types.AgentState,
	messages []chat.Message,
	tools []chat.Tool,
	promptTokens int,
) {
	if e == nil || state == nil {
		return
	}
	state.ContextUsage = AttributeContextUsage(
		e.tokenEstimator, messages, tools, e.skillsPromptContent(), e.contextWindowTokens(),
	)
	state.ContextUsage.Calibrate(promptTokens)
}

func isMCPToolSchema(name string) bool {
	switch name {
	case agenttools.ToolDiscoverMCPTools, agenttools.ToolCallMCPTool:
		return true
	}
	return strings.HasPrefix(name, "mcp_")
}
