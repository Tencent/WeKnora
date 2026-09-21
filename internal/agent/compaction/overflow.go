package compaction

import (
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Context overflow is worth detecting precisely because the recovery is
// specific: compact and retry once. Misread as a generic failure it ends the
// turn; misread as a transient error it retries the same oversized request
// until the attempts run out. Patterns are collected from real provider
// responses.
var overflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)prompt is too long`),                                  // Anthropic
	regexp.MustCompile(`(?i)request_too_large`),                                   // Anthropic (HTTP 413)
	regexp.MustCompile(`(?i)input is too long for requested model`),               // Amazon Bedrock
	regexp.MustCompile(`(?i)exceeds the context window`),                          // OpenAI
	regexp.MustCompile(`(?i)exceeds (the )?(model'?s )?maximum context length`),   // OpenAI-compatible proxies
	regexp.MustCompile(`(?i)input token count.*exceeds the maximum`),              // Google Gemini
	regexp.MustCompile(`(?i)maximum prompt length is \d+`),                        // xAI Grok
	regexp.MustCompile(`(?i)reduce the length of the messages`),                   // Groq
	regexp.MustCompile(`(?i)maximum context length is \d+ tokens`),                // OpenRouter
	regexp.MustCompile(`(?i)exceeds (the )?maximum allowed input length`),         // OpenRouter / Poolside
	regexp.MustCompile(`(?i)is longer than the model'?s context length`),          // Together AI
	regexp.MustCompile(`(?i)exceeds the limit of \d+`),                            // GitHub Copilot
	regexp.MustCompile(`(?i)exceeds the available context size`),                  // llama.cpp
	regexp.MustCompile(`(?i)greater than the context length`),                     // LM Studio
	regexp.MustCompile(`(?i)context window exceeds limit`),                        // MiniMax
	regexp.MustCompile(`(?i)exceeded model token limit`),                          // Kimi
	regexp.MustCompile(`(?i)too large for model with \d+ maximum context length`), // Mistral
	regexp.MustCompile(`(?i)but the configured context size is`),                  // DS4
	regexp.MustCompile(`(?i)model_context_window_exceeded`),                       // z.ai
	regexp.MustCompile(`(?i)prompt too long; exceeded (max )?context length`),     // Ollama
	regexp.MustCompile(`(?i)range of input length should be`),                     // DashScope / Qwen
	regexp.MustCompile(`(?i)context[_ ]length[_ ]exceeded`),                       // generic
	regexp.MustCompile(`(?i)too many tokens`),                                     // generic
	regexp.MustCompile(`(?i)token limit exceeded`),                                // generic
}

// nonOverflowPatterns are errors that mention token counts while meaning
// something else. Bedrock throttling reads "ThrottlingException: Too many
// tokens, please wait before trying again", which matches an overflow pattern
// and is not one. Matched anywhere in the message rather than anchored, since
// WeKnora sees raw provider errors rather than a pre-normalized prefix.
var nonOverflowPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)throttling`),
	regexp.MustCompile(`(?i)service unavailable`),
	regexp.MustCompile(`(?i)rate limit`),
	regexp.MustCompile(`(?i)too many requests`),
}

// IsOverflowError reports whether a failed LLM call failed because the request
// did not fit in the context window.
func IsOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, p := range nonOverflowPatterns {
		if p.MatchString(msg) {
			return false
		}
	}
	for _, p := range overflowPatterns {
		if p.MatchString(msg) {
			return true
		}
	}
	return false
}

// ResponseHitContextLimit reports whether a *successful* response was shaped by
// a full context window rather than by the completion budget we asked for.
// Three shapes:
//
//   - The provider accepted an oversized request silently and reported a prompt
//     larger than the window (z.ai).
//   - The provider truncated the input to fill the window exactly, leaving no
//     room to generate, and stopped on length with no output (Xiaomi MiMo).
//   - The provider stopped on length having emitted less than we allowed, so
//     our own cap cannot be what stopped it (the general recoverable case).
//
// completionBudget is the max_tokens we requested, before any context-based
// clamping; passing the clamped value would hide exactly the case this detects.
func ResponseHitContextLimit(
	resp *types.ChatResponse, contextWindow, completionBudget int,
) bool {
	if resp == nil {
		return false
	}
	prompt := resp.Usage.PromptTokens
	completion := resp.Usage.CompletionTokens
	lengthStop := isLengthStop(resp.FinishReason)

	if contextWindow > 0 && prompt > contextWindow {
		return true
	}
	if contextWindow > 0 && lengthStop && completion == 0 && prompt > 0 &&
		prompt >= contextWindow*99/100 {
		return true
	}
	// Without usage there is nothing to compare against; treating that as
	// overflow would retry every ordinary truncation.
	return lengthStop && completionBudget > 0 && completion > 0 && completion < completionBudget
}

func isLengthStop(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens":
		return true
	default:
		return false
	}
}
