package tools

import "regexp"

// thinkBlockRe matches <think>…</think> blocks that some models embed in content.
// Uses (?s) flag so . matches newlines.
var thinkBlockRe = regexp.MustCompile(`(?s)<think>.*?</think>`)

// unterminatedThinkRe matches a <think> that never closes, through end of
// input. Applied after thinkBlockRe, so any <think> still present opens a
// block the output was cut off inside.
var unterminatedThinkRe = regexp.MustCompile(`(?s)<think>.*$`)

// StripThinkBlocks removes <think>…</think> blocks from LLM output content.
// Some models (DeepSeek, Qwen, etc.) embed chain-of-thought reasoning inside
// <think> tags in the content field. These should be stripped before:
//   - Displaying content to users
//   - Storing content in agent state / context manager
//   - Emitting content via EventBus
//
// Returns the cleaned string, or empty string if input is empty or becomes empty.
func StripThinkBlocks(content string) string {
	if content == "" {
		return ""
	}
	cleaned := thinkBlockRe.ReplaceAllString(content, "")
	// A block the completion cap cut off mid-reasoning never gets its closing
	// tag, so the pair regex leaves it whole. Everything from the dangling
	// <think> to the end is reasoning: the streaming splitter routes exactly
	// that text to the thought area (ThinkStreamSplitter.Flush), and a caller
	// asking "is there an answer here?" must get the same answer as the
	// splitter, not a chain of thought with a tag glued to its front.
	cleaned = unterminatedThinkRe.ReplaceAllString(cleaned, "")
	// Trim leading/trailing whitespace that may remain after removal
	result := trimWhitespace(cleaned)
	return result
}

// trimWhitespace trims leading and trailing whitespace without importing strings.
func trimWhitespace(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
