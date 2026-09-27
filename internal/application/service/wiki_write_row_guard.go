package service

import (
	"errors"
	"sort"
	"strings"
)

// ErrWikiWriteDroppedTableRows is returned when a machine write is refused
// because the incoming body no longer carries table rows the stored page still
// has. The stored page is left untouched. See wikiWriteMissingRowIdentities.
var ErrWikiWriteDroppedTableRows = errors.New("wiki page write dropped table rows the stored page still has")

// wikiWriteGuardLogLimit caps how many example row identities a refusal names.
const wikiWriteGuardLogLimit = 5

// wikiWriteEmphasisMarkers are the inline markers that change how a cell
// renders but not which entity it names, so they are ignored when comparing
// one spelling of a row against another.
var wikiWriteEmphasisMarkers = strings.NewReplacer("*", "", "_", "", "`", "")

// isWikiWriteTableRow reports whether line is a markdown table row: trimmed it
// starts with '|' and carries at least two pipes. A single pipe is a stray
// character in prose, not a table.
func isWikiWriteTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// isWikiWriteDelimiterRow reports whether line is the `| --- | :--: |` row that
// separates a table's header from its data: every cell is built only from '-',
// ':' and spaces. The delimiter names no entity of its own.
func isWikiWriteDelimiterRow(line string) bool {
	for _, cell := range strings.Split(line, "|") {
		if strings.Trim(cell, "-: \t") != "" {
			return false
		}
	}
	return true
}

// normalizeWikiWriteCell makes two spellings of the same cell compare equal:
// the full-width space becomes an ordinary one, emphasis/code markers are
// dropped, whitespace runs collapse, and the result is lower-cased. Formatting
// and case are the two things a model changes freely while re-emitting a page,
// so neither may read as a lost row.
func normalizeWikiWriteCell(cell string) string {
	cell = strings.ReplaceAll(cell, "\u3000", " ")
	cell = wikiWriteEmphasisMarkers.Replace(cell)
	return strings.ToLower(strings.Join(strings.Fields(cell), " "))
}

// wikiWriteRowIdentity returns the normalized first non-empty cell of a table
// row — the cell that says what the row is about — or "" when the row names
// nothing at all.
func wikiWriteRowIdentity(line string) string {
	for _, cell := range strings.Split(line, "|") {
		if identity := normalizeWikiWriteCell(cell); identity != "" {
			return identity
		}
	}
	return ""
}

// wikiWriteTableRowIdentities lists the data-row identities of every markdown
// table in content, in file order. Header rows are skipped: the row directly
// above the `| --- |` delimiter names columns, not entities, and a model
// renames it freely, so counting it would refuse healthy rewrites.
func wikiWriteTableRowIdentities(content string) []string {
	lines := strings.Split(content, "\n")
	identities := make([]string, 0, 8)
	for i, line := range lines {
		if !isWikiWriteTableRow(line) || isWikiWriteDelimiterRow(line) {
			continue
		}
		if i+1 < len(lines) && isWikiWriteTableRow(lines[i+1]) && isWikiWriteDelimiterRow(lines[i+1]) {
			continue
		}
		if identity := wikiWriteRowIdentity(line); identity != "" {
			identities = append(identities, identity)
		}
	}
	return identities
}

// wikiWriteMissingRowIdentities reports which data rows of existing are no
// longer present in rewritten, as a sorted, de-duplicated list of identities.
//
// Rows are matched by identity rather than by whole line, so re-flowing
// whitespace, bolding a name or updating a date/amount cell keeps the row
// counted as present; only a row whose subject disappeared counts as lost.
// Duplicates are matched as a multiset, so dropping one of two rows that name
// the same entity is still a loss.
func wikiWriteMissingRowIdentities(existing, rewritten string) []string {
	oldIdentities := wikiWriteTableRowIdentities(existing)
	if len(oldIdentities) == 0 {
		return nil
	}
	remaining := make(map[string]int, len(oldIdentities))
	for _, identity := range wikiWriteTableRowIdentities(rewritten) {
		remaining[identity]++
	}
	missing := make([]string, 0, 4)
	reported := make(map[string]bool, 4)
	for _, identity := range oldIdentities {
		if remaining[identity] > 0 {
			remaining[identity]--
			continue
		}
		if reported[identity] {
			continue
		}
		reported[identity] = true
		missing = append(missing, identity)
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return missing
}

// wikiWriteDroppedRowExamples caps a missing-row list for a log line.
func wikiWriteDroppedRowExamples(missing []string) []string {
	if len(missing) > wikiWriteGuardLogLimit {
		return missing[:wikiWriteGuardLogLimit]
	}
	return missing
}
