package modelcontext

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// HandleTable is a typed, invocation-local bidirectional mapping used when a
// prompt needs compact handles for durable values. Handles must never be
// persisted; Resolve converts model output back to the durable value first.
type HandleTable struct {
	mu       sync.RWMutex
	prefix   string
	width    int
	next     int
	toHandle map[string]string
	toValue  map[string]string
}

// NewHandleTable creates a handle space such as c000 (prefix=c, width=3,
// start=0) or ref-1 (prefix=ref-, width=0, start=1).
func NewHandleTable(prefix string, width, start int) *HandleTable {
	return &HandleTable{
		prefix:   prefix,
		width:    width,
		next:     start,
		toHandle: make(map[string]string),
		toValue:  make(map[string]string),
	}
}

// Register returns the stable handle assigned to value in this table.
func (t *HandleTable) Register(value string) string {
	if t == nil {
		return ""
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if handle := t.toHandle[value]; handle != "" {
		return handle
	}
	number := fmt.Sprintf("%d", t.next)
	if t.width > 0 {
		number = fmt.Sprintf("%0*d", t.width, t.next)
	}
	t.next++
	handle := t.prefix + number
	t.toHandle[value] = handle
	t.toValue[handle] = value
	return handle
}

// Handle returns an already registered handle without creating one.
func (t *HandleTable) Handle(value string) (string, bool) {
	if t == nil {
		return "", false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	handle, ok := t.toHandle[value]
	return handle, ok
}

// Resolve converts a known model handle back to its durable value.
func (t *HandleTable) Resolve(handle string) (string, bool) {
	if t == nil {
		return "", false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	value, ok := t.toValue[strings.TrimSpace(handle)]
	return value, ok
}

func (t *HandleTable) Empty() bool {
	if t == nil {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.toHandle) == 0
}

func (t *HandleTable) Len() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.toHandle)
}

// EncodeKnownText replaces already-registered durable values with their model
// handles. Longer values are processed first to avoid substring shadowing.
func (t *HandleTable) EncodeKnownText(value string) string {
	if t == nil || value == "" {
		return value
	}
	t.mu.RLock()
	type pair struct{ value, handle string }
	pairs := make([]pair, 0, len(t.toHandle))
	for durable, handle := range t.toHandle {
		pairs = append(pairs, pair{durable, handle})
	}
	t.mu.RUnlock()
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].value) > len(pairs[j].value) })
	for _, item := range pairs {
		value = strings.ReplaceAll(value, item.value, item.handle)
	}
	return value
}

// DecodeKnownText restores registered handles in complete text.
func (t *HandleTable) DecodeKnownText(value string) string {
	if t == nil || value == "" {
		return value
	}
	t.mu.RLock()
	handles := make([]string, 0, len(t.toValue))
	for handle := range t.toValue {
		handles = append(handles, handle)
	}
	t.mu.RUnlock()
	sort.SliceStable(handles, func(i, j int) bool { return len(handles[i]) > len(handles[j]) })
	for _, handle := range handles {
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(handle) + `\b`)
		if durable, ok := t.Resolve(handle); ok {
			// A durable value may legally contain '$'. ReplaceAllString would
			// interpret it as a regexp expansion, so use a literal callback.
			value = pattern.ReplaceAllStringFunc(value, func(string) string { return durable })
		}
	}
	return value
}

// HandleStreamDecoder restores HandleTable values without leaking a handle
// split across provider chunks.
type HandleStreamDecoder struct {
	table   *HandleTable
	pending string
}

func NewHandleStreamDecoder(table *HandleTable) *HandleStreamDecoder {
	return &HandleStreamDecoder{table: table}
}

func (d *HandleStreamDecoder) Feed(chunk string) string {
	if d == nil || d.table == nil {
		return chunk
	}
	combined := d.pending + chunk
	d.pending = ""
	if combined == "" {
		return ""
	}
	start := len(combined)
	for start > 0 && isHandleTokenByte(combined[start-1]) {
		start--
	}
	tail := combined[start:]
	d.table.mu.RLock()
	prefix := d.table.prefix
	d.table.mu.RUnlock()
	if couldBeNumericHandle(tail, prefix) {
		d.pending = tail
		combined = combined[:start]
	}
	return d.table.DecodeKnownText(combined)
}

func (d *HandleStreamDecoder) Flush() string {
	if d == nil || d.table == nil {
		return ""
	}
	tail := d.table.DecodeKnownText(d.pending)
	d.pending = ""
	return tail
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
