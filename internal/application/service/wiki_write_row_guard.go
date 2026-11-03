package service

import (
	"bytes"
	"errors"
	"sort"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	tableast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
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

// wikiWriteHeaderWidth counts explicitly supplied GFM header cells, including
// empty ones. Escaped pipes are cell content, and outer pipes are optional.
func wikiWriteHeaderWidth(line []byte) int {
	header := strings.TrimSpace(string(line))
	width := 1
	for i := 0; i < len(header); i++ {
		if header[i] == '\\' && i+1 < len(header) {
			i++
			continue
		}
		if header[i] == '|' {
			width++
			if i == 0 {
				width--
			}
			if i == len(header)-1 {
				width--
			}
		}
	}
	return width
}

// wikiWriteTableRowIdentities lists the data-row identities of every markdown
// table in content, in file order. GFM permits optional outer pipes; parsing
// table structure also keeps headers, prose and code examples out of the guard.
// Each row still names its normalized first non-empty cell.
func wikiWriteTableRowIdentities(content string) []string {
	source := []byte(content)
	document := goldmark.New(goldmark.WithExtensions(extension.Table)).Parser().Parse(text.NewReader(source))
	identities := make([]string, 0, 8)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering && node.Kind() == tableast.KindTable {
			// Goldmark pads short headers. Compare their actual source width
			// with the delimiter width, while preserving explicitly empty cells.
			header := node.FirstChild()
			line := source[header.Pos():]
			if end := bytes.IndexByte(line, '\n'); end >= 0 {
				line = line[:end]
			}
			if wikiWriteHeaderWidth(line) != header.ChildCount() {
				return ast.WalkSkipChildren, nil
			}
		}
		if !entering || node.Kind() != tableast.KindTableRow {
			return ast.WalkContinue, nil
		}
		for cell := node.FirstChild(); cell != nil; cell = cell.NextSibling() {
			if identity := normalizeWikiWriteCell(string(cell.Lines().Value(source))); identity != "" {
				identities = append(identities, identity)
				break
			}
		}
		return ast.WalkSkipChildren, nil
	})
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
