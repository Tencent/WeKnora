// Package aiclient handles AI agent integration: env-based detection
// (used to trigger AGENT-targeted help text) and per-command help
// annotations. See cli/AGENTS.md for the full agent contract.
package aiclient

import "os"

// AIAgentName identifies the detected coding agent invoking the CLI. Empty
// string means no agent detected (or detection is disabled).
type AIAgentName string

// aiAgentEnvs maps environment variable presence to a coding agent name.
// The earlier 7-entry list (CODEX_*, AIDER_PROMPT, CONTINUE_GLOBAL_DIR,
// OPENCODE_RUNNING, GEMINICODER_PROFILE) did not have official agent docs
// backing those env names; removed in v0.2 to avoid maintaining an
// unverified hardcoded list. New entries should document the source URL.
var aiAgentEnvs = []struct {
	env  string
	name AIAgentName
}{
	{"CLAUDECODE", "claude-code"},
	{"CURSOR_AGENT", "cursor"},
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
