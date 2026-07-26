// stream.go owns streaming-safe handle decoding: the shared suffix-hold
// primitive, the per-space decoders built on it, and the composed public
// StreamDecoder that applies every stage in the only safe order.
package modelcontext

import "strings"

// streamHold is the shared primitive for decoders that must never emit a
// partial model handle: each Feed withholds a trailing byte run that could
// still grow into a handle in the next provider chunk, and applies the
// space-specific decode to everything released. flush decides what happens to
// a suffix still held when the stream closes.
type streamHold struct {
	pending string
	holdLen func(combined string) int    // trailing bytes to withhold
	emit    func(released string) string // decode applied to released text
	flush   func(pending string) string  // end-of-stream disposition
}

func (h *streamHold) Feed(chunk string) string {
	combined := h.pending + chunk
	h.pending = ""
	if combined == "" {
		return ""
	}
	if hold := h.holdLen(combined); hold > 0 && hold <= len(combined) {
		h.pending = combined[len(combined)-hold:]
		combined = combined[:len(combined)-hold]
	}
	return h.emit(combined)
}

func (h *streamHold) Flush() string {
	pending := h.pending
	h.pending = ""
	return h.flush(pending)
}

// resourceStreamDecoder restores res:// handles split across provider chunks.
type resourceStreamDecoder struct {
	hold streamHold
}

func newResourceStreamDecoder(registry *resourceRegistry) *resourceStreamDecoder {
	return &resourceStreamDecoder{hold: streamHold{
		holdLen: func(combined string) int {
			hold := 0
			for _, alias := range registry.aliases() {
				// A provider may split the token at any byte boundary, including
				// "re" + "s://0001". Holding at most the short matching suffix is
				// the only way to guarantee a request-local handle never leaks.
				for n := 1; n < len(alias); n++ {
					if n > hold && strings.HasSuffix(combined, alias[:n]) {
						hold = n
					}
				}
			}
			return hold
		},
		emit:  registry.DecodeText,
		flush: registry.DecodeText,
	}}
}

func (d *resourceStreamDecoder) Feed(chunk string) string {
	if d == nil {
		return chunk
	}
	return d.hold.Feed(chunk)
}

func (d *resourceStreamDecoder) Flush() string {
	if d == nil {
		return ""
	}
	return d.hold.Flush()
}

// HandleStreamDecoder restores HandleTable values without leaking a handle
// split across provider chunks.
type HandleStreamDecoder struct {
	table *HandleTable
	hold  streamHold
}

func NewHandleStreamDecoder(table *HandleTable) *HandleStreamDecoder {
	return &HandleStreamDecoder{table: table, hold: streamHold{
		holdLen: func(combined string) int {
			start := len(combined)
			for start > 0 && isHandleTokenByte(combined[start-1]) {
				start--
			}
			tail := combined[start:]
			// The prefix is immutable after construction, so no lock is needed.
			if couldBeNumericHandle(tail, table.table.prefix) {
				return len(tail)
			}
			return 0
		},
		emit:  table.DecodeKnownText,
		flush: table.DecodeKnownText,
	}}
}

func (d *HandleStreamDecoder) Feed(chunk string) string {
	if d == nil || d.table == nil {
		return chunk
	}
	return d.hold.Feed(chunk)
}

func (d *HandleStreamDecoder) Flush() string {
	if d == nil || d.table == nil {
		return ""
	}
	return d.hold.Flush()
}

func isHandleTokenByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9' || value == '-' || value == '_'
}

func couldBeNumericHandle(value, prefix string) bool {
	if value == "" || prefix == "" {
		return false
	}
	if strings.HasPrefix(prefix, value) {
		return true
	}
	if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
		return false
	}
	for _, char := range value[len(prefix):] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// orphanResourceStreamFilter removes unresolved resource handles only after
// the registered resource decoder has had a chance to restore known ones. It
// buffers a possible handle suffix so provider chunk boundaries cannot leak a
// partial internal token to the UI.
type orphanResourceStreamFilter struct {
	hold streamHold
}

func newOrphanResourceStreamFilter() *orphanResourceStreamFilter {
	return &orphanResourceStreamFilter{hold: streamHold{
		holdLen: orphanResourceHoldLen,
		emit: func(released string) string {
			return resourceHandleShapeRE.ReplaceAllString(released, "")
		},
		flush: orphanResourceFlush,
	}}
}

func orphanResourceHoldLen(combined string) int {
	const prefix = "res://"
	holdAt := -1
	// Unknown handles must be safe across every provider split, including
	// "re" + "s://9999". This may defer at most a few ordinary characters
	// until the next chunk; Flush preserves them when they are normal prose.
	for n := 1; n < len(prefix); n++ {
		if strings.HasSuffix(combined, prefix[:n]) {
			holdAt = len(combined) - n
		}
	}
	if idx := strings.LastIndex(combined, prefix); idx >= 0 {
		suffix := combined[idx+len(prefix):]
		if suffix == "" || allDigits(suffix) {
			holdAt = idx
		}
	}
	if holdAt < 0 {
		return 0
	}
	return len(combined) - holdAt
}

func orphanResourceFlush(pending string) string {
	// A stream that ends mid-token must not surface the model-context
	// protocol fragment. Preserve ordinary r/re/res prose, but discard any
	// suffix that has already crossed into the reserved URL-like syntax.
	if strings.HasPrefix("res://", pending) && len(pending) >= len("res:") {
		return ""
	}
	if strings.HasPrefix(pending, "res://") {
		digits := strings.TrimPrefix(pending, "res://")
		if digits == "" || allDigits(digits) {
			return ""
		}
	}
	return resourceHandleShapeRE.ReplaceAllString(pending, "")
}

func (f *orphanResourceStreamFilter) Feed(chunk string) string {
	if f == nil {
		return chunk
	}
	return f.hold.Feed(chunk)
}

func (f *orphanResourceStreamFilter) Flush() string {
	if f == nil {
		return ""
	}
	return f.hold.Flush()
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// StreamDecoder composes resource restoration, source-citation expansion,
// issue-handle decoding and orphan filtering so callers cannot split handle
// processing or apply stages in the wrong order.
type StreamDecoder struct {
	resources *resourceStreamDecoder
	sources   *citationStreamExpander
	issues    *HandleStreamDecoder
	orphans   *orphanResourceStreamFilter
}

func (d *StreamDecoder) Feed(chunk string) string {
	if d == nil {
		return chunk
	}
	if d.resources != nil {
		chunk = d.resources.Feed(chunk)
	}
	if d.sources != nil {
		chunk = d.sources.Feed(chunk)
	}
	if d.issues != nil {
		chunk = d.issues.Feed(chunk)
	}
	if d.orphans != nil {
		chunk = d.orphans.Feed(chunk)
	}
	return chunk
}

func (d *StreamDecoder) Flush() string {
	if d == nil {
		return ""
	}
	// Each stage's tail must be fed THROUGH the later stages before those
	// stages flush their own pending suffix, otherwise a handle completed by
	// an earlier stage's tail would bypass later decoding.
	var tail string
	if d.resources != nil {
		tail = d.resources.Flush()
	}
	if d.sources != nil {
		tail = d.sources.Feed(tail) + d.sources.Flush()
	}
	if d.issues != nil {
		tail = d.issues.Feed(tail) + d.issues.Flush()
	}
	if d.orphans != nil {
		tail = d.orphans.Feed(tail) + d.orphans.Flush()
	}
	return tail
}
