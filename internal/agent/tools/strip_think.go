package tools

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
	_, answer := SplitThinkContent(content)
	return trimWhitespace(answer)
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

// SplitThinkContent reconstructs complete reasoning and answer from one response.
// Unlike the bounded live preview, persisted reasoning is never truncated.
func SplitThinkContent(content string) (reasoning, answer string) {
	sp := NewThinkStreamSplitter()
	sp.prefixLimit = len(content)
	reasoning, answer = sp.Feed(content)
	thoughtTail, answerTail := sp.Flush()
	return reasoning + thoughtTail, answer + answerTail
}
