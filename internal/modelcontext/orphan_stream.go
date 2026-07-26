package modelcontext

import (
	"regexp"
	"strings"
)

var orphanResourceHandleRE = regexp.MustCompile(`res://\d+`)

// orphanResourceStreamFilter removes unresolved resource handles only after
// the registered resource decoder has had a chance to restore known ones. It
// buffers a possible handle suffix so provider chunk boundaries cannot leak a
// partial internal token to the UI.
type orphanResourceStreamFilter struct {
	pending string
}

func (f *orphanResourceStreamFilter) Feed(chunk string) string {
	if f == nil {
		return chunk
	}
	combined := f.pending + chunk
	f.pending = ""
	if combined == "" {
		return ""
	}

	const prefix = "res://"
	holdAt := -1
	// Unknown handles must be safe across every provider split, including
	// "re" + "s://9999". This may defer at most three ordinary characters
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
	if holdAt >= 0 {
		f.pending = combined[holdAt:]
		combined = combined[:holdAt]
	}
	return orphanResourceHandleRE.ReplaceAllString(combined, "")
}

func (f *orphanResourceStreamFilter) Flush() string {
	if f == nil {
		return ""
	}
	pending := f.pending
	f.pending = ""
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
	pending = orphanResourceHandleRE.ReplaceAllString(pending, "")
	return pending
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
