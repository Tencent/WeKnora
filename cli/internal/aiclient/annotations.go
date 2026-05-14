package aiclient

import (
	"strings"

	"github.com/spf13/cobra"
)

// AIAgentHelpKey is the cobra Annotation key used to attach an AI-agent-specific
// help blurb to a command. The blurb is appended to --help output only when
// agent mode is active.
const AIAgentHelpKey = "ai_agent_help"

// FormatAgentGuidance returns the agent-targeted help text registered on cmd,
// or "" if none. Render it after the standard help when DetectAIAgent() != ""
// (an AI coding agent env var is set).
func FormatAgentGuidance(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	v, ok := cmd.Annotations[AIAgentHelpKey]
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

// SetAgentHelp is a small helper that initializes Annotations and stores the
// blurb under AIAgentHelpKey. Use it in NewCmdX constructors before returning.
func SetAgentHelp(cmd *cobra.Command, blurb string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[AIAgentHelpKey] = strings.TrimSpace(blurb)
}
