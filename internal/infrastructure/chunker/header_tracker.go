// Package chunker - header_tracker.go implements context-preserving header
// tracking for document chunking, ported from docreader/splitter/header_hook.py.
//
// When a large Markdown table is split across multiple chunks, each chunk after
// the first would lose the table header context. The headerTracker detects table
// headers and signals the merge logic to prepend them to subsequent chunks.
package chunker

import (
	"regexp"
	"sort"
	"strings"
)

// headerTrackerHook defines a pattern pair for detecting contextual headers.
// When startPattern matches a unit's text, that text becomes an "active header".
// The header stays active until endPattern matches a subsequent unit.
type headerTrackerHook struct {
	startPattern *regexp.Regexp
	endPattern   *regexp.Regexp
	priority     int
}

// defaultHeaderHooks returns header tracking hooks matching the Python defaults
// in docreader/splitter/header_hook.py.
var defaultHeaderHooks = []headerTrackerHook{
	{
		// Markdown table: header row + separator row (e.g. "| A | B |\n| --- | --- |\n")
		startPattern: regexp.MustCompile(`(?si)^\s*(?:\|[^|\n]*)+[\r\n]+\s*(?:\|\s*:?-{3,}:?\s*)+\|?[\r\n]+$`),
		// Empty/whitespace line or a line that doesn't start with | or whitespace
		endPattern: regexp.MustCompile(`(?si)^\s*$|^\s*[^|\s].*$`),
		priority:   15,
	},
}

// tableRowPattern matches a single Markdown table row: "| cell | cell | ... |\n"
var tableRowPattern = regexp.MustCompile(`(?m)^\s*(?:\|[^|\n]*)+\|\s*$`)

// headerTracker maintains the state of active headers across split units.
type headerTracker struct {
	hooks         []headerTrackerHook
	activeHeaders map[int]string // priority -> header text
	endedHeaders  map[int]bool   // priorities that have been ended
	pendingExtend map[int]bool   // headers with empty column names awaiting first data row
}

func newHeaderTracker() *headerTracker {
	return &headerTracker{
		hooks:         defaultHeaderHooks,
		activeHeaders: make(map[int]string),
		endedHeaders:  make(map[int]bool),
		pendingExtend: make(map[int]bool),
	}
}

// update checks split text for header start/end markers and updates internal state.
func (ht *headerTracker) update(split string) {
	// 1. Check for header-end markers among currently active headers
	for _, hook := range ht.hooks {
		if _, active := ht.activeHeaders[hook.priority]; active {
			if hook.endPattern.MatchString(split) {
				ht.endedHeaders[hook.priority] = true
				delete(ht.activeHeaders, hook.priority)
				delete(ht.pendingExtend, hook.priority)
			}
		}
	}

	// 2. If a header has an empty column-name row (e.g. "||"), replace it with
	//    a proper Markdown table header using the first data row as column names.
	//
	//    Before: "||"           + "| --- | --- |\n"
	//    After:  "| col1 | col2 |\n" + "| --- | --- |\n"
	for p := range ht.pendingExtend {
		if _, active := ht.activeHeaders[p]; active && tableRowPattern.MatchString(split) {
			sep := extractSeparatorLine(ht.activeHeaders[p])
			ht.activeHeaders[p] = split + sep
		}
		delete(ht.pendingExtend, p)
	}

	// 3. Check for new header-start markers (only for hooks that are neither active nor ended)
	for _, hook := range ht.hooks {
		if _, active := ht.activeHeaders[hook.priority]; active {
			continue
		}
		if ht.endedHeaders[hook.priority] {
			continue
		}
		if loc := hook.startPattern.FindString(split); loc != "" {
			ht.activeHeaders[hook.priority] = loc
			if isEmptyTableHeaderRow(loc) {
				ht.pendingExtend[hook.priority] = true
			}
		}
	}

	// 4. If all headers ended, clear the ended set so future tables can be tracked
	if len(ht.activeHeaders) == 0 {
		for k := range ht.endedHeaders {
			delete(ht.endedHeaders, k)
		}
	}
}

// getHeaders returns all active headers concatenated, sorted by priority descending.
func (ht *headerTracker) getHeaders() string {
	if len(ht.activeHeaders) == 0 {
		return ""
	}

	type entry struct {
		priority int
		text     string
	}
	entries := make([]entry, 0, len(ht.activeHeaders))
	for p, t := range ht.activeHeaders {
		entries = append(entries, entry{p, t})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].priority > entries[j].priority
	})

	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = e.text
	}
	return strings.Join(parts, "\n")
}

// isEmptyTableHeaderRow checks if the header row (the line before the separator)
// contains only pipes and whitespace — meaning the column names are empty.
// This is common with MarkItDown and similar converters that produce tables like:
//
//	||
//	| --- | --- |
//	| real column A | real column B |
func isEmptyTableHeaderRow(header string) bool {
	idx := strings.IndexByte(header, '\n')
	if idx < 0 {
		return false
	}
	row := strings.TrimSpace(header[:idx])
	for _, r := range row {
		if r != '|' && r != ' ' && r != '\t' {
			return false
		}
	}
	return true
}

// extractSeparatorLine returns the separator line (e.g. "| --- | --- |\n") from
// a table header string. It looks for the line containing "---".
func extractSeparatorLine(header string) string {
	for _, line := range strings.Split(header, "\n") {
		if strings.Contains(line, "---") {
			return line + "\n"
		}
	}
	return ""
}
