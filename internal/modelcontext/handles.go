package modelcontext

import (
	"regexp"
	"sort"
	"strings"
)

// HandleTable is a typed, invocation-local bidirectional mapping used when a
// prompt needs compact handles for durable values (wiki issue iN handles,
// ingest ref-N slug handles, c000 citation-batch handles). Handles must never
// be persisted; Resolve converts model output back to the durable value first.
// It is the exported word-boundary-aware wrapper over handleTable.
type HandleTable struct {
	table *handleTable[struct{}]
}

// NewHandleTable creates a handle space such as c000 (prefix=c, width=3,
// start=0) or ref-1 (prefix=ref-, width=0, start=1).
func NewHandleTable(prefix string, width, start int) *HandleTable {
	return &HandleTable{table: newHandleTable[struct{}](prefix, width, start)}
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
	return t.table.register(value, value, struct{}{}, nil)
}

// Handle returns an already registered handle without creating one.
func (t *HandleTable) Handle(value string) (string, bool) {
	if t == nil {
		return "", false
	}
	return t.table.handleForKey(value)
}

// Resolve converts a known model handle back to its durable value.
func (t *HandleTable) Resolve(handle string) (string, bool) {
	if t == nil {
		return "", false
	}
	value, _, ok := t.table.resolve(strings.TrimSpace(handle))
	return value, ok
}

func (t *HandleTable) Empty() bool {
	return t == nil || t.table.size() == 0
}

func (t *HandleTable) Len() int {
	if t == nil {
		return 0
	}
	return t.table.size()
}

// EncodeKnownText replaces already-registered durable values with their model
// handles. Longer values are processed first to avoid substring shadowing.
func (t *HandleTable) EncodeKnownText(value string) string {
	if t == nil || value == "" {
		return value
	}
	pairs := t.table.pairs()
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].value) > len(pairs[j].value) })
	for _, item := range pairs {
		value = strings.ReplaceAll(value, item.value, item.handle)
	}
	return value
}

// DecodeKnownText restores registered handles in complete text. Unlike the
// resource codec, replacement is word-boundary-aware so short handles such as
// i1 cannot fire inside ordinary prose tokens.
func (t *HandleTable) DecodeKnownText(value string) string {
	if t == nil || value == "" {
		return value
	}
	pairs := t.table.pairs()
	sort.SliceStable(pairs, func(i, j int) bool { return len(pairs[i].handle) > len(pairs[j].handle) })
	for _, item := range pairs {
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(item.handle) + `\b`)
		// A durable value may legally contain '$'. ReplaceAllString would
		// interpret it as a regexp expansion, so use a literal callback.
		durable := item.value
		value = pattern.ReplaceAllStringFunc(value, func(string) string { return durable })
	}
	return value
}
