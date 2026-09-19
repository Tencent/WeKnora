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
// Think-tag markup inside fenced code blocks (``` / ~~~) or inline code spans
// is literal document content, NOT a block boundary: a long markdown answer
// that merely MENTIONS the tags must not get its head routed into the thinking
// card (#3132). This mirrors the frontend parser in
// frontend/src/utils/thinkBlocks.ts so the live stream and the re-parsed
// history render the same way. Real think blocks live at the top level, so
// skipping code regions cannot misplace reasoning; inside a think block only
// the close tag matters.
//
// Tag/fence boundaries that straddle two chunks are handled by buffering the
// trailing text that could still become part of a marker until the next Feed
// call. Call Flush at end-of-stream to drain any buffered remainder. The
// splitter is NOT safe for concurrent use; create one per stream.
type ThinkStreamSplitter struct {
	inThink bool
	// fenceChar / fenceLen describe the fenced code block currently open at
	// the top level (fenceLen == 0 when none). Tags inside it are literal
	// answer text.
	fenceChar byte
	fenceLen  int
	// spanRun is the backtick-run length of the inline code span currently
	// open at the top level (0 when none). Tags inside it are literal answer
	// text until a run of exactly spanRun backticks closes it.
	spanRun int
	// pending holds text that cannot yet be classified because it may be (or
	// contain the start of) a tag, fence line or backtick run spanning into
	// the next chunk.
	pending string
	// lineStart reports whether pending[0] sits at a line start (fences are
	// only recognised there, and pending is cut mid-line whenever text is
	// held back).
	lineStart bool
}

// NewThinkStreamSplitter returns a splitter ready to receive content chunks.
func NewThinkStreamSplitter() *ThinkStreamSplitter {
	return &ThinkStreamSplitter{lineStart: true}
}

// Feed consumes one content chunk and returns the portions that are now
// unambiguously thinking text and answer text respectively. Either return value
// may be empty. Bytes that could still be part of a marker spanning into the
// next chunk are buffered internally and surface on a later Feed or on Flush.
func (sp *ThinkStreamSplitter) Feed(s string) (thinkOut, answerOut string) {
	if s == "" {
		return "", ""
	}
	sp.pending += s
	return sp.classify(false)
}

// Flush drains any buffered remainder at end-of-stream. An unterminated <think>
// block is treated as thinking text; an unterminated fence or code span keeps
// consuming as answer text, matching how a markdown renderer treats it.
func (sp *ThinkStreamSplitter) Flush() (thinkOut, answerOut string) {
	t, a := sp.classify(true)
	sp.pending = ""
	return t, a
}

// emitFunc receives classified segments in order.
type emitFunc func(isThink bool, text string)

func (sp *ThinkStreamSplitter) classify(final bool) (string, string) {
	var think, answer strings.Builder
	var out emitFunc = func(isThink bool, text string) {
		if isThink {
			think.WriteString(text)
		} else {
			answer.WriteString(text)
		}
	}
	for sp.pending != "" {
		before := len(sp.pending)
		switch {
		case sp.inThink:
			sp.drainInThink(out, final)
		case sp.fenceLen > 0:
			sp.drainInFence(out, final)
		case sp.spanRun > 0:
			sp.drainInSpan(out, final)
		default:
			sp.drainTopLevel(out, final)
		}
		if len(sp.pending) == before {
			break // waiting for more input
		}
	}
	return think.String(), answer.String()
}

// consume advances pending by n bytes without emitting them (used to drop the
// think tags themselves), keeping line-start bookkeeping in sync.
func (sp *ThinkStreamSplitter) consume(n int) {
	if n <= 0 {
		return
	}
	if n >= len(sp.pending) {
		sp.pending = ""
		return
	}
	sp.lineStart = sp.pending[n-1] == '\n'
	sp.pending = sp.pending[n:]
}

// cut emits pending[:n] as answer text and advances pending.
func (sp *ThinkStreamSplitter) cut(out emitFunc, n int) {
	if n <= 0 {
		return
	}
	out(false, sp.pending[:n])
	sp.consume(n)
}

// cutThink emits pending[:n] as thinking text and advances pending.
func (sp *ThinkStreamSplitter) cutThink(out emitFunc, n int) {
	if n <= 0 {
		return
	}
	out(true, sp.pending[:n])
	sp.consume(n)
}

// drainInThink classifies pending while inside a real think block: only the
// close tag matters (fences and backticks in reasoning are just text).
func (sp *ThinkStreamSplitter) drainInThink(out emitFunc, final bool) {
	idx := strings.Index(sp.pending, thinkCloseTag)
	if idx >= 0 {
		sp.cutThink(out, idx)
		sp.consume(len(thinkCloseTag))
		sp.inThink = false
		return
	}
	if final {
		sp.cutThink(out, len(sp.pending))
		return
	}
	k := partialTagSuffixLen(sp.pending)
	sp.cutThink(out, len(sp.pending)-k)
}

// drainInFence classifies pending while inside a fenced code block at the top
// level: everything is literal answer until the closing fence line.
func (sp *ThinkStreamSplitter) drainInFence(out emitFunc, final bool) {
	for sp.pending != "" {
		if !sp.lineStart {
			// Mid-line fragment: literal answer up to the next line start.
			nl := strings.IndexByte(sp.pending, '\n')
			if nl < 0 {
				if final {
					sp.cut(out, len(sp.pending))
				}
				return
			}
			sp.cut(out, nl+1)
			continue
		}
		nl := strings.IndexByte(sp.pending, '\n')
		if nl >= 0 {
			if isFenceCloseLine(sp.pending[:nl+1], sp.fenceChar, sp.fenceLen) {
				sp.fenceChar, sp.fenceLen = 0, 0
			}
			sp.cut(out, nl+1)
			if sp.fenceLen == 0 {
				return // fence closed; re-dispatch
			}
			continue
		}
		// Incomplete last line: hold it only while it could still become the
		// closing fence.
		if final || !couldBeFenceCloseLine(sp.pending, sp.fenceChar) {
			sp.cut(out, len(sp.pending))
		}
		return
	}
}

// drainInSpan classifies pending while inside an inline code span at the top
// level: everything is literal answer until a run of exactly spanRun backticks
// closes the span. pending starts just after the opening run.
func (sp *ThinkStreamSplitter) drainInSpan(out emitFunc, final bool) {
	at := findBacktickRunIn(sp.pending, 0, sp.spanRun)
	if at >= 0 && (at+sp.spanRun < len(sp.pending) || final) {
		sp.cut(out, at+sp.spanRun)
		sp.spanRun = 0
		return
	}
	if final {
		// Unterminated span consumes the rest as literal answer.
		sp.cut(out, len(sp.pending))
		sp.spanRun = 0
		return
	}
	if tail := trailingBacktickRun(sp.pending); tail > 0 {
		// A trailing run may still grow into the closing run: hold it.
		sp.cut(out, len(sp.pending)-tail)
		return
	}
	sp.cut(out, len(sp.pending))
}

// drainTopLevel classifies pending while at the top level (outside think
// blocks, fences and code spans).
func (sp *ThinkStreamSplitter) drainTopLevel(out emitFunc, final bool) {
	p := sp.pending
	i := 0
	for i < len(p) {
		lineStart := (i == 0 && sp.lineStart) || (i > 0 && p[i-1] == '\n')
		if lineStart && !final && strings.IndexByte(p[i:], '\n') < 0 && couldBeFenceOpenLine(p[i:]) {
			// A fence opener needs its full line (the info string may still
			// arrive): hold the incomplete line.
			sp.cut(out, i)
			return
		}
		c := p[i]
		if c == '`' {
			run := backtickRunLen(p, i)
			if i+run == len(p) && !final {
				sp.cut(out, i) // the run may still grow; wait
				return
			}
			if lineStart && run >= 3 {
				// Fenced code block opener at a line start.
				nl := strings.IndexByte(p[i:], '\n')
				if nl >= 0 {
					sp.cut(out, i+nl+1)
					sp.fenceChar, sp.fenceLen = p[i], run
					return
				}
				if !final {
					sp.cut(out, i) // wait for the full opener line
					return
				}
				// final: unterminated fence consumes the rest as answer.
				sp.cut(out, len(p))
				return
			}
			// Inline code span opener with an exact backtick run.
			if closeEnd, ok := spanCloseComplete(p, i+run, run); ok {
				i = closeEnd
				continue
			}
			if !final {
				// The span may close in a later chunk: consume the opener and
				// keep the span open across Feeds.
				sp.cut(out, i+run)
				sp.spanRun = run
				return
			}
			// final: unterminated span consumes the rest as literal answer.
			sp.cut(out, len(p))
			return
		}
		if strings.HasPrefix(p[i:], thinkOpenTag) {
			sp.cut(out, i)
			sp.consume(len(thinkOpenTag))
			sp.inThink = true
			return
		}
		if strings.HasPrefix(p[i:], thinkCloseTag) {
			// Stray close tag without a block to close stays literal answer.
			i += len(thinkCloseTag)
			continue
		}
		if c == '<' && !final && isPartialTagPrefix(p[i:]) {
			sp.cut(out, i) // a tag may be completed by the next chunk
			return
		}
		i++
	}
	sp.cut(out, len(p))
}

// isPartialTagPrefix reports whether s (the tail of the current buffer) is a
// proper prefix of one of the think tags, i.e. a tag the next chunk may
// complete.
func isPartialTagPrefix(s string) bool {
	if s == "" || s[0] != '<' {
		return false
	}
	if len(s) < len(thinkOpenTag) && strings.HasPrefix(thinkOpenTag, s) {
		return true
	}
	return len(s) < len(thinkCloseTag) && strings.HasPrefix(thinkCloseTag, s)
}

// partialTagSuffixLen returns the length of the trailing suffix of s that is a
// proper prefix of either think tag (0 when none).
func partialTagSuffixLen(s string) int {
	maxK := len(thinkCloseTag) - 1
	if maxK > len(s) {
		maxK = len(s)
	}
	for k := maxK; k >= 1; k-- {
		suffix := s[len(s)-k:]
		if strings.HasPrefix(thinkCloseTag, suffix) || strings.HasPrefix(thinkOpenTag, suffix) {
			return k
		}
	}
	return 0
}

// backtickRunLen returns the length of the backtick run starting at s[idx].
func backtickRunLen(s string, idx int) int {
	n := 0
	for idx+n < len(s) && s[idx+n] == '`' {
		n++
	}
	return n
}

// trailingBacktickRun returns the length of the run of backticks ending at the
// end of s (0 when s does not end with a backtick).
func trailingBacktickRun(s string) int {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '`'; i-- {
		n++
	}
	return n
}

// findBacktickRunIn returns the index of the first run of EXACTLY run backticks
// in s at or after index from, or -1.
func findBacktickRunIn(s string, from, run int) int {
	i := from
	for i < len(s) {
		at := strings.IndexByte(s[i:], '`')
		if at < 0 {
			return -1
		}
		at += i
		if backtickRunLen(s, at) == run {
			return at
		}
		i = at + 1
	}
	return -1
}

// spanCloseComplete reports whether the span opened with a run of `run`
// backticks closes within s at or after index from, returning the index just
// after the closing run. The closing run must be complete, i.e. followed by a
// non-backtick byte; a run reaching the end of s is inconclusive because a
// later chunk may extend it.
func spanCloseComplete(s string, from, run int) (int, bool) {
	at := findBacktickRunIn(s, from, run)
	if at < 0 || at+run >= len(s) {
		return 0, false
	}
	return at + run, true
}

// couldBeFenceOpenLine reports whether the incomplete line s could still turn
// into a fenced block opener (up to 3 leading spaces, then only '`' or '~' so
// far).
func couldBeFenceOpenLine(s string) bool {
	i := 0
	spaces := 0
	for i < len(s) && s[i] == ' ' && spaces < 3 {
		i++
		spaces++
	}
	if i >= len(s) {
		return true // only spaces so far
	}
	for _, c := range s[i:] {
		if c != '`' && c != '~' {
			return false
		}
	}
	return true
}

// isFenceCloseLine reports whether the complete line is a closing fence for a
// block opened with char×openLen: up to 3 leading spaces, a run of at least
// openLen of the same character, and nothing else but trailing spaces/tabs.
func isFenceCloseLine(line string, char byte, openLen int) bool {
	i := 0
	spaces := 0
	for i < len(line) && line[i] == ' ' && spaces < 3 {
		i++
		spaces++
	}
	run := 0
	for i+run < len(line) && line[i+run] == char {
		run++
	}
	if run < openLen {
		return false
	}
	for j := i + run; j < len(line); j++ {
		if line[j] != ' ' && line[j] != '\t' && line[j] != '\n' && line[j] != '\r' {
			return false
		}
	}
	return true
}

// couldBeFenceCloseLine reports whether the incomplete line s could still turn
// into a closing fence for a block opened with char.
func couldBeFenceCloseLine(s string, char byte) bool {
	i := 0
	spaces := 0
	for i < len(s) && s[i] == ' ' && spaces < 3 {
		i++
		spaces++
	}
	if i >= len(s) {
		return true
	}
	for j := i; j < len(s); j++ {
		c := s[j]
		if c != char && c != ' ' && c != '\t' {
			return false
		}
	}
	return true
}
