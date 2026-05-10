// Package agent handles AI agent integration: env-based detection (used to
// trigger AGENT-targeted help text) and per-command help annotations.
//
// v0.2: removed the omnibus `--agent` flag + ApplyAgentSugar mode-switch
// (audited as over-design — see docs/superpowers/specs/2026-05-09 ADR-3).
// Mainstream CLIs (gh / kubectl / aws / docker / flyctl) deliberately don't
// have a single mode-switch flag; per-command --json + TTY auto-detect cover
// 95% of cases. WeKnora now follows that convention.
//
// What remains here: a small env-detect for known coding agents, used purely
// to render the AGENT-targeted help section (no behavior change). Patterned
// after Stripe's DetectAIAgent (https://github.com/stripe/stripe-cli/tree/master/pkg/useragent),
// which Stripe uses only for User-Agent telemetry tagging.
package agent

import "os"

// AIAgentName identifies the detected coding agent invoking the CLI. Empty
// string means no agent detected (or detection is disabled).
type AIAgentName string

// aiAgentEnvs maps environment variable presence to a coding agent name.
// Cropped to the entries the Stripe CLI also recognizes (verified against
// stripe-cli/pkg/useragent/useragent.go). The earlier 7-entry list (CODEX_*,
// AIDER_PROMPT, CONTINUE_GLOBAL_DIR, OPENCODE_RUNNING, GEMINICODER_PROFILE)
// did not have official agent docs backing those env names; removed in v0.2
// to avoid maintaining an unverified hardcoded list. New entries should
// document the source URL.
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
