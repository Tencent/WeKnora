package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const (
	// DefaultAgentTemperature is the default temperature for the agent
	DefaultAgentTemperature = 0.7
	// DefaultAgentMaxIterations is the default maximum number of iterations for the agent
	DefaultAgentMaxIterations = 20
	// DefaultUseCustomSystemPrompt is the default whether to use custom system prompt for the agent
	DefaultUseCustomSystemPrompt = false

	// defaultLLMCallTimeout is the default maximum time allowed for a single LLM call.
	// This prevents a single slow call from consuming the entire pipeline deadline.
	// Can be overridden via AgentConfig.LLMCallTimeout.
	defaultLLMCallTimeout = 120 * time.Second

	// minLLMCallTimeout / maxLLMCallTimeout bound the effective per-call timeout
	// when AgentConfig.LLMCallTimeout is set explicitly. Values below min can never
	// survive network + first-token latency and almost always indicate operator
	// error; values above max defeat the purpose of the per-call guard (a hung
	// provider connection would pin a worker for hours). The max matches the
	// frontend editor's upper bound (AgentEditorModal.vue, :max="3600").
	minLLMCallTimeout = 10 * time.Second
	maxLLMCallTimeout = 3600 * time.Second

	// defaultToolExecTimeout is the default maximum time for a single tool execution.
	// Prevents long-running tools (web_fetch, database_query) from hanging indefinitely.
	defaultToolExecTimeout = 60 * time.Second
	// shellExecToolTimeout is slightly longer than shell_exec's own hard
	// 600-second command timeout so the tool can return a structured timeout
	// result instead of being cancelled first by the generic agent wrapper.
	shellExecToolTimeout = 10*time.Minute + 5*time.Second

	// maxLLMRetries is the maximum number of retries for transient LLM errors.
	maxLLMRetries = 2

	// maxEmptyResponseRetries is the maximum number of retries when the LLM
	// returns an empty content with a natural stop (no tool calls). This guards
	// against the agent completing with an empty answer when the LLM fails to
	// produce content (e.g., thinking-only loops without KB).
	// Trade-off: each retry costs ~2s of LLM latency; 2 retries = max 4s extra.
	maxEmptyResponseRetries = 2

	// maxRepeatedResponseRounds is the maximum number of consecutive rounds
	// where the LLM returns identical content without any tool calls before
	// the loop is forcibly terminated. This catches stuck loops caused by
	// unhandled finish reasons (e.g., content_filter not caught elsewhere).
	maxRepeatedResponseRounds = 2
)

func toolExecutionTimeout(toolName string) time.Duration {
	if toolName == "shell_exec" {
		return shellExecToolTimeout
	}
	return defaultToolExecTimeout
}

// transientErrorMarkers are substrings that indicate a transient (retryable) error.
var transientErrorMarkers = []string{
	"429", "rate limit",
	"500", "502", "503", "504",
	"overloaded", "timeout", "timed out",
	"connection", "server error", "temporarily unavailable",
}

// isTransientError checks whether an error is likely transient and worth retrying.
func isTransientError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	for _, marker := range transientErrorMarkers {
		if strings.Contains(errStr, marker) {
			return true
		}
	}
	return false
}

// getLLMCallTimeout returns the configured LLM call timeout, falling back to
// default. Explicitly configured values are clamped into
// [minLLMCallTimeout, maxLLMCallTimeout] so a misconfiguration can neither
// break every call (too short) nor silently disable the guard (too long);
// values <= 0 keep the "use default" semantics. The comparison happens in
// the seconds domain to avoid time.Duration overflow on absurd inputs.
func (e *AgentEngine) getLLMCallTimeout() time.Duration {
	if e.config == nil || e.config.LLMCallTimeout <= 0 {
		return defaultLLMCallTimeout
	}
	sec := e.config.LLMCallTimeout
	if sec < int(minLLMCallTimeout/time.Second) {
		return minLLMCallTimeout
	}
	if sec > int(maxLLMCallTimeout/time.Second) {
		return maxLLMCallTimeout
	}
	return time.Duration(sec) * time.Second
}

// getCompletionTokenBudget is the max_tokens / max_completion_tokens sent on
// each ReAct LLM round. Unset without a sandbox is 4096; unset with a
// sandbox (write_sandbox_file / edit_sandbox_file) is 24576.
func (e *AgentEngine) getCompletionTokenBudget() int {
	configured := 0
	sandboxID := ""
	if e.config != nil {
		configured = e.config.MaxCompletionTokens
		sandboxID = e.config.SandboxConfigID
	}
	return types.AgentRoundMaxCompletionTokensFor(configured, sandboxID)
}

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}
