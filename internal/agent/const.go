package agent

import (
	"fmt"
	"strings"
	"time"

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

	// defaultToolExecTimeout is the default maximum time for a single tool execution.
	// Prevents long-running tools (web_fetch, database_query) from hanging indefinitely.
	defaultToolExecTimeout = 60 * time.Second

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

// getLLMCallTimeout returns the configured LLM call timeout, falling back to default.
func (e *AgentEngine) getLLMCallTimeout() time.Duration {
	if e.config.LLMCallTimeout > 0 {
		return time.Duration(e.config.LLMCallTimeout) * time.Second
	}
	return defaultLLMCallTimeout
}

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}
