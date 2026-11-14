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
	inThink  bool
	closeTag string // exact matching closer, including a provider nonce if present
	// pending holds trailing bytes from a previous Feed that may be the start
	// of a (possibly split) <think>/</think> tag and therefore cannot yet be
	// classified as think or answer text.
	pending string
	// Only a nonce-qualified orphan closer is strong enough evidence to retract
	// an already emitted prefix. Keep a bounded copy for live reasoning display.
	seenMarker   bool
	answerPrefix strings.Builder
	prefixLimit  int
	answerReset  bool
	markdown     thinkMarkdownState
}

// NewThinkStreamSplitter returns a splitter ready to receive content chunks.
func NewThinkStreamSplitter() *ThinkStreamSplitter {
	return &ThinkStreamSplitter{prefixLimit: 16 * 1024}
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
	writeAnswer := func(text string) {
		answer.WriteString(text)
		for i := range text {
			sp.markdown.consume(text[i])
		}
		if !sp.seenMarker && sp.answerPrefix.Len() < sp.prefixLimit {
			n := min(len(text), sp.prefixLimit-sp.answerPrefix.Len())
			// Never split a UTF-8 rune in the bounded reasoning preview.
			for n > 0 && n < len(text) && text[n]&0xc0 == 0x80 {
				n--
			}
			sp.answerPrefix.WriteString(text[:n])
		}
	}
	for {
		if sp.inThink {
			if idx := strings.Index(sp.pending, sp.closeTag); idx >= 0 {
				think.WriteString(sp.pending[:idx])
				sp.pending = sp.pending[idx+len(sp.closeTag):]
				sp.inThink = false
				continue
			}
			safe, hold := holdBackPartialTag(sp.pending, sp.closeTag)
			think.WriteString(safe)
			sp.pending = hold
			return think.String(), answer.String()
		}

		if idx, size, closer, orphan := findThinkMarker(sp.pending, sp.markdown, !sp.seenMarker); idx >= 0 {
			writeAnswer(sp.pending[:idx])
			sp.pending = sp.pending[idx:]
			if size == 0 {
				return think.String(), answer.String()
			}
			sp.pending = sp.pending[size:]
			if orphan {
				think.WriteString(sp.answerPrefix.String())
				answer.Reset()
				sp.answerReset = true
				sp.markdown = thinkMarkdownState{}
			} else {
				sp.inThink = true
				sp.closeTag = closer
			}
			sp.seenMarker = true
			sp.answerPrefix.Reset()
			continue
		}
		writeAnswer(sp.pending)
		sp.pending = ""
		return think.String(), answer.String()
	}
}

// TakeAnswerReset reports that a nonce-qualified orphan closer proved the
// previously emitted prefix was reasoning. Consumers must replace that answer
// segment before appending the answer returned by the same Feed call.
func (sp *ThinkStreamSplitter) TakeAnswerReset() bool {
	reset := sp.answerReset
	sp.answerReset = false
	return reset
}

// Track Markdown code delimiters across chunks, so literal tag examples are
// not provider protocol. A tag escaped with a backslash is literal too.
type thinkMarkdownState struct {
	ticks, run        int
	marker, runMarker byte
	escaped           bool
}

func (m *thinkMarkdownState) finishRun() {
	if m.run == 0 {
		return
	}
	if m.ticks == 0 && (m.runMarker == '\x60' || m.run >= 3) {
		m.ticks, m.marker = m.run, m.runMarker
	} else if m.marker == m.runMarker && (m.run == m.ticks || (m.ticks >= 3 && m.run >= m.ticks)) {
		m.ticks, m.marker = 0, 0
	}
	m.run = 0
}

func (m *thinkMarkdownState) consume(c byte) bool {
	if m.escaped {
		m.escaped = false
		return true
	}
	if c == '\x60' || c == '~' {
		if m.run > 0 && m.runMarker != c {
			m.finishRun()
		}
		m.runMarker = c
		m.run++
		return true
	}
	m.finishRun()
	if c == '\\' && m.ticks == 0 {
		m.escaped = true
		return true
	}
	return m.ticks != 0
}

func findThinkMarker(
	s string,
	markdown thinkMarkdownState,
	allowOrphan bool,
) (index, size int, closer string, orphan bool) {
	for i := 0; i < len(s); i++ {
		literal := markdown.consume(s[i])
		if s[i] != '<' || literal {
			continue
		}
		tail := s[i:]
		if idx, n, closingTag := findThinkOpen(tail); idx == 0 {
			return i, n, closingTag, false
		}
		if !allowOrphan {
			continue
		}
		const prefix = "</think:"
		if strings.HasPrefix(prefix, tail) {
			return i, 0, "", true
		}
		if !strings.HasPrefix(tail, prefix) {
			continue
		}
		nonce := tail[len(prefix):]
		n := 0
		for n < len(nonce) && n < 33 && isThinkNonceHex(nonce[n]) {
			n++
		}
		if n == len(nonce) && n <= 32 {
			return i, 0, "", true
		}
		if n >= 6 && n <= 32 && nonce[n] == '>' {
			return i, len(prefix) + n + 1, "", true
		}
	}
	return -1, 0, "", false
}

// findThinkOpen recognizes standard tags and the <think:hexnonce> variant
// emitted by compatible endpoints. A size of zero means a potentially valid
// tag spans the next chunk. Invalid lookalikes remain ordinary answer text.
func findThinkOpen(s string) (index, size int, closer string) {
	const prefix = "<think"
	for offset := 0; offset < len(s); {
		rel := strings.IndexByte(s[offset:], '<')
		if rel < 0 {
			break
		}
		i := offset + rel
		tail := s[i:]
		if strings.HasPrefix(prefix, tail) {
			return i, 0, ""
		}
		if strings.HasPrefix(tail, thinkOpenTag) {
			return i, len(thinkOpenTag), thinkCloseTag
		}
		if strings.HasPrefix(tail, prefix+":") {
			nonce := tail[len(prefix)+1:]
			n := 0
			for n < len(nonce) && n < 33 && isThinkNonceHex(nonce[n]) {
				n++
			}
			if n == len(nonce) && n <= 32 {
				return i, 0, ""
			}
			if n >= 6 && n <= 32 && nonce[n] == '>' {
				return i, len(prefix) + 1 + n + 1, "</think:" + nonce[:n] + ">"
			}
		}
		offset = i + 1
	}
	return -1, 0, ""
}

func isThinkNonceHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
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
