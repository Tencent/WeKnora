package modelcontext

import (
	"fmt"
	"regexp"
	"sync"
)

// handleTable is the single bidirectional primitive behind every model-handle
// space: cN/dN/bN/wN source handles, iN issue handles, res://NNNN resource
// handles, and the ref-N / c000 ingest handles exposed through HandleTable.
//
// It maps a durable dedup key to a sequentially allocated handle and stores
// the durable value plus optional metadata under one lock, so a resolvable
// handle can never be observed without its metadata. The key and the value
// may differ (web references dedup on a canonicalized URL but decode back to
// the original raw URL). Entries are never removed, which makes the counter
// equivalent to the historical len(map)+1 allocation and keeps handle
// numbering stable for the lifetime of the table.
type handleTable[M any] struct {
	mu            sync.RWMutex
	prefix        string
	width         int
	next          int
	handleByKey   map[string]string
	entryByHandle map[string]*handleEntry[M]
}

type handleEntry[M any] struct {
	value       string // durable value a handle decodes back to
	meta        M
	wordBounded *regexp.Regexp
}

// handlePair is a snapshot row for text codecs (compaction/decoding).
type handlePair struct {
	value  string
	handle string
	// wordBounded matches the handle only on word boundaries. It is compiled
	// once at registration because DecodeKnownText runs on every streamed
	// chunk, where recompiling per handle per chunk dominated the cost.
	wordBounded *regexp.Regexp
}

// newHandleTable creates a handle space such as c1 (prefix="c", width=0,
// start=1), c000 (prefix="c", width=3, start=0) or res://0001
// (prefix="res://", width=4, start=1).
func newHandleTable[M any](prefix string, width, start int) *handleTable[M] {
	return &handleTable[M]{
		prefix:        prefix,
		width:         width,
		next:          start,
		handleByKey:   make(map[string]string),
		entryByHandle: make(map[string]*handleEntry[M]),
	}
}

// register returns the handle assigned to key, allocating the next one on
// first use. value is what the handle decodes back to; merge, when non-nil,
// folds the metadata of a repeated registration into the existing entry.
func (t *handleTable[M]) register(key, value string, meta M, merge func(dst *M, src M)) string {
	if t == nil || key == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if handle, ok := t.handleByKey[key]; ok {
		if merge != nil {
			merge(&t.entryByHandle[handle].meta, meta)
		}
		return handle
	}
	number := fmt.Sprintf("%d", t.next)
	if t.width > 0 {
		number = fmt.Sprintf("%0*d", t.width, t.next)
	}
	t.next++
	handle := t.prefix + number
	t.handleByKey[key] = handle
	t.entryByHandle[handle] = &handleEntry[M]{
		value:       value,
		meta:        meta,
		wordBounded: regexp.MustCompile(`\b` + regexp.QuoteMeta(handle) + `\b`),
	}
	return handle
}

// handleForKey returns an existing handle without allocating one.
func (t *handleTable[M]) handleForKey(key string) (string, bool) {
	if t == nil {
		return "", false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	handle, ok := t.handleByKey[key]
	return handle, ok
}

// resolve returns the durable value and metadata snapshot for a known handle.
func (t *handleTable[M]) resolve(handle string) (string, M, bool) {
	var zero M
	if t == nil {
		return "", zero, false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.entryByHandle[handle]
	if !ok {
		return "", zero, false
	}
	return entry.value, entry.meta, true
}

// has reports whether handle exists in this table.
func (t *handleTable[M]) has(handle string) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.entryByHandle[handle]
	return ok
}

func (t *handleTable[M]) size() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.entryByHandle)
}

// pairs returns a value/handle snapshot for text codecs. Callers own ordering
// (e.g. longest-value-first for substring compaction).
func (t *handleTable[M]) pairs() []handlePair {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]handlePair, 0, len(t.entryByHandle))
	for handle, entry := range t.entryByHandle {
		out = append(out, handlePair{
			value:       entry.value,
			handle:      handle,
			wordBounded: entry.wordBounded,
		})
	}
	return out
}
