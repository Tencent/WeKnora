package agent

import (
	"os"

	"github.com/spf13/cobra"
)

// agentSugarFlags lists the flags ApplyAgentSugar sets to their agent-friendly
// defaults when triggered. Each entry is no-op'd at runtime if the leaf hasn't
// registered the flag (e.g. version doesn't have --no-progress).
var agentSugarFlags = []struct{ name, value string }{
	{"json", "true"},
	{"no-interactive", "true"},
	{"no-progress", "true"},
}

// ApplyAgentSugar evaluates --agent / WEKNORA_AGENT / env-based AI agent
// detection, then sets json / no-interactive / no-progress to their
// agent-friendly defaults unless the user already set them.
func ApplyAgentSugar(cmd *cobra.Command) {
	if !ShouldUseAgentMode(cmd) {
		return
	}
	for _, kv := range agentSugarFlags {
		if f := cmd.Flags().Lookup(kv.name); f != nil && !cmd.Flags().Changed(kv.name) {
			_ = cmd.Flags().Set(kv.name, kv.value)
		}
	}
}

// ShouldUseAgentMode returns true if any of the three triggers are active:
// (1) --agent flag, (2) WEKNORA_AGENT=1 env, (3) a known agent env var is set.
func ShouldUseAgentMode(cmd *cobra.Command) bool {
	if cmd != nil {
		if v, err := cmd.Flags().GetBool("agent"); err == nil && v {
			return true
		}
	}
	if truthy(os.Getenv("WEKNORA_AGENT")) {
		return true
	}
	return DetectAIAgent() != ""
}

// truthy treats any non-empty value other than "0" / "false" / "FALSE" as
// truthy. This is wider than strconv.ParseBool by design — users set
// WEKNORA_AGENT=1, =true, or =yes interchangeably across CI shells.
func truthy(v string) bool {
	switch v {
	case "", "0", "false", "FALSE":
		return false
	default:
		return true
	}
}
