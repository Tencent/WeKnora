package agent

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
)

const agentRetrievedImageRequirementMarker = "## Retrieved Image Output Requirement"

const agentRetrievedImageSystemRequirement = `

## Retrieved Image Output Requirement
Retrieved tool results for this turn contain Markdown images. Treat images attached to retrieved passages as relevant by default.
- Unless the user explicitly requests text-only output, or every retrieved image is clearly unrelated, the final answer MUST include at least one relevant Markdown image copied verbatim from the tool results.
- Preserve the complete Markdown image syntax and URL exactly; never invent, shorten, normalize, or replace the URL.
- Use ASCII half-width parentheses exactly as ![alt](url); never use full-width （ or ）.
- Place each image immediately after the paragraph it supports.
- When multiple retrieved images support different sections, distribute them across those sections instead of stopping after the first image.
- Before finishing, silently verify that the answer contains a Markdown image whenever this requirement applies.`

func stepContainsMarkdownImage(step types.AgentStep) bool {
	for _, toolCall := range step.ToolCalls {
		if toolCall.Result != nil &&
			toolCall.Result.Success &&
			searchutil.MarkdownImageRegex.MatchString(toolCall.Result.Output) {
			return true
		}
	}
	return false
}

func appendAgentRetrievedImageRequirement(messages []chat.Message) []chat.Message {
	for i := range messages {
		if messages[i].Role != "system" {
			continue
		}
		if !strings.Contains(messages[i].Content, agentRetrievedImageRequirementMarker) {
			messages[i].Content = strings.TrimRight(messages[i].Content, " \t\r\n") + agentRetrievedImageSystemRequirement
		}
		break
	}
	return messages
}
