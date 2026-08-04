package storageurl

import (
	"context"
	"regexp"
	"strings"
	"sync"
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
var incompleteMarkdownImageSuffixRe = regexp.MustCompile(`!\[[^\]]*\]\([^)]*$`)

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
	if loc == nil {
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
	held map[string]string
}

// NewStreamRewriter wraps rewriter with per-stream holdback state.
func NewStreamRewriter(rewriter *Rewriter) *StreamRewriter {
	return &StreamRewriter{rewriter: rewriter, held: make(map[string]string)}
}

// Enabled reports whether this StreamRewriter can rewrite anything.
func (s *StreamRewriter) Enabled() bool {
	return s != nil && s.rewriter.Enabled()
}

// Push feeds the next chunk of the stream identified by key and returns the
// rewritten content that is ready to emit. Set flush on the stream's terminal
// chunk to release any held tail.
func (s *StreamRewriter) Push(ctx context.Context, key, chunk string, flush bool) string {
	if !s.Enabled() {
		return chunk
	}

	s.mu.Lock()
	pending := s.held[key] + chunk
	cutoff := len(pending)
	if !flush {
		cutoff = HoldbackCutoff(pending)
		// Never buffer without bound: release the excess even though it may
		// contain a partial reference, which is what an un-rewritten stream
		// would have shown anyway.
		if len(pending)-cutoff > maxHeldBytes {
			cutoff = len(pending) - maxHeldBytes
		}
	}
	emit := pending[:cutoff]
	if remainder := pending[cutoff:]; remainder == "" {
		delete(s.held, key)
	} else {
		s.held[key] = remainder
	}
	s.mu.Unlock()

	return s.rewriter.String(ctx, emit)
}

// FlushAll releases every held tail, rewritten, keyed by stream. Callers use it
// when a stream ends without a terminal chunk (for example a client
// disconnect) so buffered content is not silently dropped.
func (s *StreamRewriter) FlushAll(ctx context.Context) map[string]string {
	if !s.Enabled() {
		return nil
	}
	s.mu.Lock()
	if len(s.held) == 0 {
		s.mu.Unlock()
		return nil
	}
	pending := s.held
	s.held = make(map[string]string)
	s.mu.Unlock()

	out := make(map[string]string, len(pending))
	for key, content := range pending {
		out[key] = s.rewriter.String(ctx, content)
	}
	return out
}
