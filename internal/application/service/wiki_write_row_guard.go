package service

import (
	"errors"
	"sort"
	"strings"
)

// ErrWikiWriteDroppedTableRows is the explicit refusal signal a machine write
// gets when the incoming body no longer carries table rows the stored page
// still has. The stored page is left untouched.
//
// It is returned for both guarded policies — see UpdatePage — and it exists
// because an unchanged `version` cannot stand in for it: UpdatePage leaves the
// version alone for a successful write that changes nothing user-visible, so
// callers that must retry a refused contribution (wiki ingest's reduce) need a
// signal of its own. See wikiWriteMissingRowIdentities.
var ErrWikiWriteDroppedTableRows = errors.New("wiki page write dropped table rows the stored page still has")

// wikiWriteGuardLogLimit caps how many example row identities a refusal names.
const wikiWriteGuardLogLimit = 5

// wikiWriteIngestRowIdentityLossLimit is the largest share of a stored page's
// distinct row identities that an ingest rewrite may stop carrying and still be
// written: it refuses only when the loss is strictly GREATER than this, so a
// rewrite that loses exactly one identity in five (0.20) goes through.
//
// Ingest needs the tolerance because its editor prompt explicitly allows
// de-duplicating rows, and a refused write keeps the contributing documents
// queued for another full Map — a legitimate de-duplication that tripped the
// guard on every batch would spend the documents' retry budget and end in the
// dead-letter lane with their additions never stored. The agent's whole-page
// writer has no such allowance; see UpdatePage.
const wikiWriteIngestRowIdentityLossLimit = 0.20

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

// wikiWriteUniqueRowIdentityCount counts the DISTINCT data-row identities in
// content — the denominator of the loss ratio. Rows that name the same entity
// in their first non-empty cell count once, however many times the page lists
// them, so a page that repeats a holder does not dilute the ratio.
func wikiWriteUniqueRowIdentityCount(content string) int {
	identities := wikiWriteTableRowIdentities(content)
	if len(identities) == 0 {
		return 0
	}
	unique := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		unique[identity] = struct{}{}
	}
	return len(unique)
}

// wikiWriteRowIdentityLossRatio is the share of the stored page's distinct row
// identities a rewrite no longer carries: missing / stored. A page with no
// identifiable rows has no ratio (0).
func wikiWriteRowIdentityLossRatio(missing, stored int) float64 {
	if stored <= 0 {
		return 0
	}
	return float64(missing) / float64(stored)
}

// wikiWriteMissingRowIdentities reports which data rows of existing are no
// longer present in rewritten, as a sorted, de-duplicated list of identities.
//
// Rows are matched by identity rather than by whole line, so re-flowing
// whitespace, bolding a name or updating a date/amount cell keeps the row
// counted as present; only a row whose subject disappeared counts as lost.
//
// Identities are compared as a SET, not as a multiset: an identity is present
// as soon as the rewrite carries it once, so merging two rows that share a
// first non-empty cell into one is not a loss, however many times the stored
// page listed them.
//
// The scope of that identity is deliberately narrow — it is only the row's
// first non-empty cell — so this reports rows that vanished, not rows that
// lost information: a rewrite that keeps the holder name but blanks, truncates
// or rewrites the other columns is NOT reported. Merging same-identity rows is
// therefore allowed without any guarantee that their remaining columns stay
// complete; the guard catches truncation, not cell-level data loss.
func wikiWriteMissingRowIdentities(existing, rewritten string) []string {
	oldIdentities := wikiWriteTableRowIdentities(existing)
	if len(oldIdentities) == 0 {
		return nil
	}
	// Set membership, not occurrence count: presence anywhere in the rewrite
	// keeps an identity alive.
	present := make(map[string]struct{}, len(oldIdentities))
	for _, identity := range wikiWriteTableRowIdentities(rewritten) {
		present[identity] = struct{}{}
	}
	missing := make([]string, 0, 4)
	reported := make(map[string]struct{}, 4)
	for _, identity := range oldIdentities {
		if _, ok := present[identity]; ok {
			continue
		}
		// The same identity can appear on several stored rows; an identity
		// that is gone is one lost identity, not one per row.
		if _, ok := reported[identity]; ok {
			continue
		}
		reported[identity] = struct{}{}
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
