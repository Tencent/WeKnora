package tools

// StripThinkBlocks removes top-level <think>…</think> blocks from LLM output
// content. Some models (DeepSeek, Qwen, etc.) embed chain-of-thought reasoning
// inside <think> tags in the content field. These should be stripped before:
//   - Displaying content to users
//   - Storing content in agent state / context manager
//   - Emitting content via EventBus
//
// Think-tag markup inside fenced code blocks (``` / ~~~) or inline code spans
// is literal document content and is preserved (#3132) — the same rule the
// streaming ThinkStreamSplitter and the frontend parser apply. An unterminated
// trailing <think> block is treated as reasoning and dropped as well.
//
// Returns the cleaned string, or empty string if input is empty or becomes empty.
func StripThinkBlocks(content string) string {
	if content == "" {
		return ""
	}
	sp := NewThinkStreamSplitter()
	_, answer := sp.Feed(content)
	_, tail := sp.Flush()
	return trimWhitespace(answer + tail)
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
