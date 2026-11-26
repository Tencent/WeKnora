package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/compaction"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/browserskill"
	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/models/api"
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

	// defaultLLMStallTimeout is how long a single LLM stream may produce no
	// output before it is cancelled. It is a stall budget, not a total budget:
	// a round that streams a whole file body inside a write_sandbox_file call
	// runs for minutes while emitting continuously, and killing it on elapsed
	// time throws away the half-assembled call. The overall ceiling belongs to
	// the provider transport (WEKNORA_LLM_STREAM_TIMEOUT_SECONDS).
	// Can be overridden via AgentConfig.LLMCallTimeout.
	defaultLLMStallTimeout = 120 * time.Second

	// defaultToolExecTimeout is the default maximum time for a single tool execution.
	// Prevents long-running tools (for example database_query) from hanging indefinitely.
	defaultToolExecTimeout = 60 * time.Second
	// shellExecToolTimeout is slightly longer than shell_exec's own hard
	// 600-second command timeout so the tool can return a structured timeout
	// result instead of being cancelled first by the generic agent wrapper.
	shellExecToolTimeout = 10*time.Minute + 5*time.Second
	// webFetchToolSlack is how much longer the agent wrapper waits than the
	// shared page-fetch budget. Same 5s headroom as shell_exec, so web_fetch can
	// return its own timeout result instead of being cancelled first.
	webFetchToolSlack = 5 * time.Second
	// workWaitToolSlack is added to work_wait's own wait so the final detailed
	// read still fits after the wait elapses (D3).
	workWaitToolSlack = 30 * time.Second

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
	// "Identical" includes an identical lack of content: consecutive empty
	// rounds are the clearest stuck loop there is.
	maxRepeatedResponseRounds = 2

	// maxConsecutiveFailedToolCalls is the number of identical failed tool
	// calls allowed before the model is asked to switch tools or strategies.
	maxConsecutiveFailedToolCalls = 3

	// maxConsecutiveIdenticalToolCalls bounds identical *successful* calls that
	// produced no observable output. A command that ran cleanly (success=true)
	// but returned nothing is indistinguishable from "ran but found nothing":
	// the model keeps re-issuing the same call, which makes no progress and can
	// grow the prompt without bound. After this many repeats the model is nudged
	// once; after twice this many the turn is stopped.
	maxConsecutiveIdenticalToolCalls = 3

	// maxConsecutiveLengthRounds is how many rounds in a row may be cut off at
	// the completion-token cap before the loop gives up. A truncated answer
	// already ends the turn (analyzeResponse Case 2), so reaching this limit
	// means round after round is truncating inside tool-call arguments: the
	// model is told to re-issue the call, writes an even longer one, and hits
	// the cap again. Without this the turn burns its whole round budget.
	maxConsecutiveLengthRounds = 3
)

// truncatedAnswerFallback is delivered when every attempt at this turn was cut
// off at the completion cap and none of them produced answer text — there is
// nothing partial to hand over, so say what happened instead of finishing with
// an empty message.
const truncatedAnswerFallback = "Sorry, this answer kept hitting the model's per-response output limit " +
	"before any text was produced. Try narrowing the question, or raise the agent's " +
	"max_completion_tokens setting."

// stalledAnswerFallback is delivered when a no-progress guard stopped the turn
// and the round that tripped it produced no text.
const stalledAnswerFallback = "I'm sorry, I was unable to generate a response. Please try again."

// Empty model output is a runtime recovery condition, not new user steering.
const emptyResponseRetryNotice = "<runtime_event>\n" +
	"{\"source\":\"runtime\",\"type\":\"empty_response_retry\",\"message\":\"The" +
	" previous model response contained no usable answer or tool calls. Continue the " +
	"existing task from the current state. Tools and waiting remain available; " +
	"provide a final response when the task is ready to conclude.\"}\n</runtime_event>"

func toolExecutionTimeout(toolName string, arguments ...string) time.Duration {
	if toolName == "local_browser" && len(arguments) > 0 {
		var input struct {
			Method string `json:"method"`
		}
		if json.Unmarshal([]byte(arguments[0]), &input) == nil && browserskill.IsHumanStep(input.Method) {
			return browserskill.HumanStepTimeout
		}
	}
	if toolName == agenttools.ToolWorkWait {
		raw := ""
		if len(arguments) > 0 {
			raw = arguments[0]
		}
		return time.Duration(agenttools.WorkWaitSeconds(raw))*time.Second + workWaitToolSlack
	}
	switch toolName {
	case "shell_exec":
		return shellExecToolTimeout
	case "web_fetch":
		return webfetch.PageFetchTimeout() + webFetchToolSlack
	}
	return defaultToolExecTimeout
}

// transientErrorMarkers are substrings that indicate a transient (retryable)
// error in a message that lost its type on the way here: stream failures reach
// the agent as text carried on an error chunk.
var transientErrorMarkers = []string{
	"rate limit",
	"overloaded", "timeout", "timed out",
	"connection", "server error", "temporarily unavailable",
	// A broken or silent stream is worth one more attempt: the round produced
	// no usable turn, and the alternative is ending the conversation on a
	// partial response.
	"deadline exceeded", "stalled", "unexpected eof",
	strings.ToLower(types.StreamEndedEarlyError),
}

// transientStatusPattern matches a retryable HTTP status as a whole number, so
// "max_tokens 5000" or a request id with 429 inside it does not read as one.
var transientStatusPattern = regexp.MustCompile(`\b(408|429|500|502|503|504|529)\b`)

// isTransientError checks whether an error is likely transient and worth retrying.
func isTransientError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	// Typed errors decide on their own: a 400 whose body happens to mention
	// "timeout" or "500" is still the same bad request on every retry.
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		code := httpErr.StatusCode
		return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests ||
			code >= http.StatusInternalServerError
	}
	var transportErr *api.TransportError
	if errors.As(err, &transportErr) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	errStr := strings.ToLower(err.Error())
	if transientStatusPattern.MatchString(errStr) {
		return true
	}
	for _, marker := range transientErrorMarkers {
		if strings.Contains(errStr, marker) {
			return true
		}
	}
	return false
}

// maxLLMRetryAfter caps how long a vendor's Retry-After may hold a turn
// between two attempts. A longer wait is not worth keeping the user on a
// silent turn for; the retry then goes out at the cap and, if the vendor is
// still limiting, the turn fails the usual way.
const maxLLMRetryAfter = 30 * time.Second

// llmRetryDelay is the wait before retry number attempt (1-based). It is the
// linear backoff, raised to the vendor's Retry-After when a 429/503 reply
// carried one, so a rate-limited request is not re-sent before the provider
// said it would accept it.
func llmRetryDelay(err error, attempt int) time.Duration {
	delay := time.Duration(attempt) * time.Second
	var httpErr *api.HTTPError
	if errors.As(err, &httpErr) {
		if wait := min(httpErr.RetryAfter(), maxLLMRetryAfter); wait > delay {
			delay = wait
		}
	}
	return delay
}

// getLLMStallTimeout returns how long an LLM stream may go silent before it is
// cancelled, from AgentConfig.LLMCallTimeout or the default.
func (e *AgentEngine) getLLMStallTimeout() time.Duration {
	if e.config.LLMCallTimeout > 0 {
		return time.Duration(e.config.LLMCallTimeout) * time.Second
	}
	return defaultLLMStallTimeout
}

// contextSafetyTokens is slack between what we estimate the request costs and
// what the provider will actually count. Token estimation is approximate and
// providers add their own scaffolding; without a margin the request that
// exactly fits by our arithmetic is the one that gets rejected.
const contextSafetyTokens = 4096

// getCompletionTokenBudget is the single completion budget for each ReAct LLM
// round. The chat layer maps it to max_tokens or max_completion_tokens per
// provider. Unset without a sandbox is 4096; unset with a sandbox
// (write_sandbox_file / edit_sandbox_file) is 24576.
func (e *AgentEngine) getCompletionTokenBudget() int {
	return completionTokenBudgetFor(e.config)
}

func completionTokenBudgetFor(cfg *types.AgentConfig) int {
	configured := 0
	sandboxID := ""
	modelLimit := 0
	if cfg != nil {
		configured = cfg.MaxCompletionTokens
		sandboxID = cfg.SandboxConfigID
		modelLimit = cfg.ModelMaxOutputTokens
	}
	budget := types.AgentRoundMaxCompletionTokensFor(configured, sandboxID)
	// Providers reject a request for more output than the model can emit.
	if modelLimit > 0 {
		budget = min(budget, modelLimit)
	}
	return budget
}

// contextReserveTokens is the part of the window that history may not occupy,
// because the next response has to fit there. Sizing it from the round's own
// completion budget is what a fixed reserve gets wrong: an agent allowed to
// emit 24576 tokens needs at least that much free, or the request is accepted
// and the reply is truncated.
func (e *AgentEngine) contextReserveTokens() int {
	return reserveTokensFor(e.config)
}

func reserveTokensFor(cfg *types.AgentConfig) int {
	return max(completionTokenBudgetFor(cfg)+contextSafetyTokens, compaction.DefaultReserveTokens)
}

// HistoryTokenBudget is how much stored history one run of cfg may load: the
// whole context window, deliberately more than the compaction threshold.
//
// The loader drops the oldest turns that do not fit, and what it drops is
// lost: it is neither replayed nor summarized. With a budget equal to the
// threshold the loader always trimmed first, so whenever one turn was larger
// than the system prompt plus the new question the request never crossed the
// threshold, nothing was summarized, and the session became a sliding window
// that never got a checkpoint. Loading up to the window leaves the overflow
// to the first round's compaction instead, which summarizes it and persists a
// checkpoint. The compactor bounds its own summarizer input, so a history this
// large cannot make the summarization request itself overflow.
func HistoryTokenBudget(cfg *types.AgentConfig) int {
	if cfg != nil && cfg.MaxContextTokens > 0 {
		return cfg.MaxContextTokens
	}
	return types.DefaultMaxContextTokens
}

// clampCompletionBudgetToContext shrinks the round's completion budget to what
// is actually left in the window. Asking for more output than the window can
// hold is a request the provider rejects outright, which reads to the agent as
// an unexplained failure.
func (e *AgentEngine) clampCompletionBudgetToContext(currentTokens int) int {
	budget := e.getCompletionTokenBudget()
	if e.config == nil || e.config.MaxContextTokens <= 0 {
		return budget
	}
	available := e.config.MaxContextTokens - currentTokens - contextSafetyTokens
	return max(min(budget, available), 1)
}

// generateEventID generates a unique event ID with type suffix for better traceability
func generateEventID(suffix string) string {
	return fmt.Sprintf("%s-%s", uuid.New().String()[:8], suffix)
}
