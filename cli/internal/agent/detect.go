// Package agent handles AI agent integration: --agent flag sugar, env detection,
// and per-command help annotations.
package agent

import "os"

// AIAgentName identifies the detected coding agent invoking the CLI. Empty
// string means no agent detected (or detection is disabled).
type AIAgentName string

// aiAgentEnvs maps environment variable presence to a coding agent name.
// Modeled on Stripe's pkg/useragent/useragent.go DetectAIAgent.
var aiAgentEnvs = []struct {
	env  string
	name AIAgentName
}{
	{"CLAUDECODE", "claude-code"},
	{"CURSOR_AGENT", "cursor"},
	{"CODEX_PROMPT_HOSTNAME", "codex"},
	{"AIDER_PROMPT", "aider"},
	{"CONTINUE_GLOBAL_DIR", "continue"},
	{"OPENCODE_RUNNING", "opencode"},
	{"GEMINICODER_PROFILE", "gemini-coder"},
}

// DetectAIAgent returns the first known agent name whose env var is set to a
// non-empty value, or "" if none are present. Detection is suppressed when
// WEKNORA_NO_AGENT_AUTODETECT is truthy. Tests substitute via t.Setenv.
func DetectAIAgent() AIAgentName {
	if v := os.Getenv("WEKNORA_NO_AGENT_AUTODETECT"); v != "" && v != "0" && v != "false" {
		return ""
	}
	for _, a := range aiAgentEnvs {
		if os.Getenv(a.env) != "" {
			return a.name
		}
	}
	return ""
}
