package storageurl

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

// ── Incomplete-reference detection ──
//
// Content is delivered to clients in chunks (SSE answer deltas, or the IM
// channel's 300ms flush batches). A storage reference may be split across two
// chunks, so a rewrite that only sees one chunk would leave a broken fragment.
// These helpers locate an incomplete pattern at the tail of a chunk so the
// caller can hold it back until the next chunk completes it.

// incompleteRefSuffixRe matches a storage reference that reaches the end of the
// string — it may continue in the next chunk.
var incompleteRefSuffixRe = regexp.MustCompile(
	`\b(?:resource|storage|local|minio|s3|cos|tos|oss|obs|ks3)://[^\s)\]>"]*$`,
)

// FindIncompleteRef returns the byte offset of a potentially truncated storage
// reference at the tail of s, or -1 if none.
func FindIncompleteRef(s string) int {
	loc := incompleteRefSuffixRe.FindStringIndex(s)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// incompleteMarkdownImageSuffixRe matches a Markdown image whose destination URL
// (the parenthesized part) is not yet closed — e.g. "![alt](minio://part" or
// "![alt](". Holding back only from "minio://" would flush "![alt](" to the
// client and break the image once the URL arrives in the next chunk.
//
// The destination must be whitespace-free, and FindIncompleteMarkdownImage caps
// its length: a URL contains neither whitespace nor a newline, and even a
// presigned URL stays well under the cap. Without those limits, prose that
// merely mentions an unclosed "…](" (an answer explaining Markdown syntax, or a
// code sample) would look forever-unclosed and stall the rest of the stream.
var incompleteMarkdownImageSuffixRe = regexp.MustCompile(`!\[[^\]]*\]\([^)\s]*$`)

// maxIncompleteImageBytes bounds how much trailing text may be treated as an
// unfinished Markdown image. Beyond it the text is not plausibly a link, so it
// is flushed instead of buffered.
const maxIncompleteImageBytes = 2048

// FindIncompleteMarkdownImage returns the byte offset of an unclosed
// `![alt](url` suffix at the end of s, or -1 if none.
func FindIncompleteMarkdownImage(s string) int {
	// Prefer pairing a trailing reference fragment with the nearest preceding
	// `![…](` so alt text may itself contain ']' (e.g. `![a[b]](minio://part`).
	if urlIdx := FindIncompleteRef(s); urlIdx >= 0 {
		if imgIdx := strings.LastIndex(s[:urlIdx], "!["); imgIdx >= 0 {
			if strings.Contains(s[imgIdx:urlIdx], "](") {
				return imgIdx
			}
		}
	}
	loc := incompleteMarkdownImageSuffixRe.FindStringIndex(s)
	if loc == nil || len(s)-loc[0] > maxIncompleteImageBytes {
		return -1
	}
	return loc[0]
}

// HoldbackCutoff returns the offset at which chunk stops being safe to flush,
// or len(chunk) when the whole chunk can be emitted.
func HoldbackCutoff(chunk string) int {
	cutoff := len(chunk)
	if idx := FindIncompleteMarkdownImage(chunk); idx >= 0 && idx < cutoff {
		return idx
	}
	if idx := FindIncompleteRef(chunk); idx >= 0 && idx < cutoff {
		return idx
	}
	return cutoff
}

// maxHeldBytes bounds the per-key holdback buffer. A storage reference plus its
// Markdown alt text is far shorter than this; the cap only stops a pathological
// stream (for example an unclosed "![" that never terminates) from buffering an
// entire answer.
const maxHeldBytes = 4096

// StreamRewriter rewrites storage references in a stream of content deltas.
//
// Each logical stream is identified by a key (WeKnora uses the SSE event id, the
// same key clients accumulate on). Push returns only the prefix that is safe to
// emit now, retaining any tail that may be an incomplete reference until the
// next Push for that key.
//
// Safe for concurrent use.
type StreamRewriter struct {
	rewriter *Rewriter

	mu   sync.Mutex
	held map[string]heldContent
}

// heldContent is a retained tail plus the metadata of the event it came from, so
// a tail released after its stream ended can be re-emitted as an equivalent
// event rather than a bare fragment.
type heldContent struct {
	content string
	meta    interface{}
}

// Held is one released tail: the rewritten content and the metadata carried by
// the last Push that contributed to it.
type Held struct {
	Content string
	Meta    interface{}
}

// NewStreamRewriter wraps rewriter with per-stream holdback state.
func NewStreamRewriter(rewriter *Rewriter) *StreamRewriter {
	return &StreamRewriter{rewriter: rewriter, held: make(map[string]heldContent)}
}

// Enabled reports whether this StreamRewriter can rewrite anything.
func (s *StreamRewriter) Enabled() bool {
	return s != nil && s.rewriter.Enabled()
}

// Rewriter exposes the underlying Rewriter for stream fields that arrive whole
// (references, metadata) and therefore need no holdback.
func (s *StreamRewriter) Rewriter() *Rewriter {
	if s == nil {
		return nil
	}
	return s.rewriter
}

// Push feeds the next chunk of the stream identified by key and returns the
// rewritten content that is ready to emit. Set flush on the stream's terminal
// chunk to release any held tail. meta is retained opaquely alongside the tail
// and handed back by FlushAll so a late release can carry the same metadata as
// the event it was cut from.
func (s *StreamRewriter) Push(ctx context.Context, key, chunk string, flush bool, meta interface{}) string {
	if !s.Enabled() {
		return chunk
	}

	s.mu.Lock()
	pending := s.held[key].content + chunk
	cutoff := len(pending)
	if !flush {
		cutoff = HoldbackCutoff(pending)
		// Never buffer without bound: release the excess even though it may
		// contain a partial reference, which is what an un-rewritten stream
		// would have shown anyway.
		if len(pending)-cutoff > maxHeldBytes {
			cutoff = runeStart(pending, len(pending)-maxHeldBytes)
		}
	}
	emit := pending[:cutoff]
	if remainder := pending[cutoff:]; remainder == "" {
		delete(s.held, key)
	} else {
		s.held[key] = heldContent{content: remainder, meta: meta}
	}
	s.mu.Unlock()

	return s.rewriter.String(ctx, emit)
}

// runeStart moves idx back to the nearest UTF-8 sequence boundary so a byte-based
// cut never splits a character in half. Pattern-derived cutoffs already land on
// ASCII delimiters; this only matters for the maxHeldBytes safety valve.
func runeStart(s string, idx int) int {
	for idx > 0 && !utf8.RuneStart(s[idx]) {
		idx--
	}
	return idx
}

// FlushAll releases every held tail, rewritten, keyed by stream. Callers use it
// when a stream ends without a terminal chunk (for example a client
// disconnect) so buffered content is not silently dropped.
func (s *StreamRewriter) FlushAll(ctx context.Context) map[string]Held {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	if len(s.held) == 0 {
		s.mu.Unlock()
		return nil
	}
	pending := s.held
	s.held = make(map[string]heldContent)
	s.mu.Unlock()

	out := make(map[string]Held, len(pending))
	for key, held := range pending {
		out[key] = Held{Content: s.rewriter.String(ctx, held.content), Meta: held.meta}
	}
	return out
}
