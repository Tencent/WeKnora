package tools

import "strings"

// thinkOpenTag / thinkCloseTag are the inline reasoning markers some models
// (DeepSeek, Qwen, …) embed directly in their `content` field instead of the
// separate `reasoning_content` channel.
const (
	thinkOpenTag  = "<think>"
	thinkCloseTag = "</think>"
)

// ThinkStreamSplitter incrementally separates inline <think>…</think> reasoning
// from user-facing answer text as content arrives chunk-by-chunk. It is the
// streaming counterpart to StripThinkBlocks: where StripThinkBlocks operates on
// a fully-accumulated string, this splitter routes each chunk live so the
// thinking portion can stream into the "thought" UI area and the answer portion
// into the "final answer" area without waiting for the whole response.
//
// Tag boundaries that straddle two chunks (e.g. "<thi" at the end of one chunk
// and "nk>" at the start of the next) are handled by buffering the trailing
// bytes that could still become a tag prefix until the next Feed call. Call
// Flush at end-of-stream to drain any buffered remainder.
//
// The splitter is NOT safe for concurrent use; create one per stream.
type ThinkStreamSplitter struct {
	inThink bool
	// pending holds trailing bytes from a previous Feed that may be the start
	// of a (possibly split) <think>/</think> tag and therefore cannot yet be
	// classified as think or answer text.
	pending string
}

// NewThinkStreamSplitter returns a splitter ready to receive content chunks.
func NewThinkStreamSplitter() *ThinkStreamSplitter {
	return &ThinkStreamSplitter{}
}

// Feed consumes one content chunk and returns the portions that are now
// unambiguously thinking text and answer text respectively. Either return value
// may be empty. Bytes that could still be part of a tag spanning into the next
// chunk are buffered internally and surface on a later Feed or on Flush.
func (sp *ThinkStreamSplitter) Feed(s string) (thinkOut, answerOut string) {
	if s == "" {
		return "", ""
	}
	sp.pending += s

	var think, answer strings.Builder
	for {
		if sp.inThink {
			if idx := strings.Index(sp.pending, thinkCloseTag); idx >= 0 {
				think.WriteString(sp.pending[:idx])
				sp.pending = sp.pending[idx+len(thinkCloseTag):]
				sp.inThink = false
				continue
			}
			safe, hold := holdBackPartialTag(sp.pending, thinkCloseTag)
			think.WriteString(safe)
			sp.pending = hold
			return think.String(), answer.String()
		}

		if idx := strings.Index(sp.pending, thinkOpenTag); idx >= 0 {
			answer.WriteString(sp.pending[:idx])
			sp.pending = sp.pending[idx+len(thinkOpenTag):]
			sp.inThink = true
			continue
		}
		safe, hold := holdBackPartialTag(sp.pending, thinkOpenTag)
		answer.WriteString(safe)
		sp.pending = hold
		return think.String(), answer.String()
	}
}

// Flush drains any buffered remainder at end-of-stream. An unterminated <think>
// block is treated as thinking text; anything else is answer text.
func (sp *ThinkStreamSplitter) Flush() (thinkOut, answerOut string) {
	rest := sp.pending
	sp.pending = ""
	if rest == "" {
		return "", ""
	}
	if sp.inThink {
		return rest, ""
	}
	return "", rest
}

// holdBackPartialTag splits s into the part that is safe to emit now and a
// trailing suffix that is a proper prefix of tag (and so might complete into a
// real tag on the next chunk). When s ends with no such prefix, the whole
// string is safe and hold is empty.
func holdBackPartialTag(s, tag string) (safe, hold string) {
	maxK := len(tag) - 1
	if maxK > len(s) {
		maxK = len(s)
	}
	for k := maxK; k >= 1; k-- {
		if strings.HasSuffix(s, tag[:k]) {
			return s[:len(s)-k], s[len(s)-k:]
		}
	}
	return s, ""
}
