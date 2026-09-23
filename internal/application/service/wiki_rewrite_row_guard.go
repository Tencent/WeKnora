package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// The editor model is asked to re-emit the whole wiki page and markdown tables
// are the shape it gets wrong most often: a hundred-row certificate ledger comes
// back with thirty rows, finish_reason=stop, no provider error. That is NOT the
// completion-budget truncation handled by generateWithTemplateResult (which
// continues and then refuses) — the answer is complete, it is simply short on
// rows. Storing it shrinks the page silently: no error, no failed addition, and
// a page-history entry that reads like any other edit. Measured on one real
// knowledge base: 156 pages got shorter, 88,556 characters lost in total, one
// certificate ledger went from 23 rows to 4.
//
// The guard below refuses that write. It compares the table data rows of the
// existing body against the table data rows of the rewrite, keyed by each row's
// first non-empty cell — the column that says which entity the row is about.
// Keying on that cell instead of comparing whole lines is what keeps legitimate
// rewrites flowing: the model may bold a name, re-flow whitespace or update the
// date/amount columns and the row still counts as present. Only a row whose
// entity disappeared entirely counts as lost, and only when the batch carries no
// retraction — a retract round removes content on purpose, so rows are expected
// to disappear there.

// wikiDroppedRowLogLimit caps how many example identities a refusal logs.
const wikiDroppedRowLogLimit = 5

// markdownEmphasisMarkers are the inline markers that change how a cell renders
// but not which entity it names, so they are ignored when comparing identities.
var markdownEmphasisMarkers = strings.NewReplacer("*", "", "_", "", "`", "")

// isMarkdownTableRow reports whether line is a markdown table row: trimmed it
// starts with '|' and carries at least two pipes. One pipe alone is a stray
// character in prose, not a table.
func isMarkdownTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// isMarkdownTableDelimiterRow reports whether line is the `| --- | :--: |` row
// that separates a table's header from its data: every cell is built only from
// '-', ':' and spaces. A delimiter row names no entity of its own.
func isMarkdownTableDelimiterRow(line string) bool {
	for _, cell := range strings.Split(line, "|") {
		if strings.Trim(cell, "-: \t") != "" {
			return false
		}
	}
	return true
}

// normalizeTableRowIdentity makes two spellings of the same cell compare equal:
// surrounding whitespace goes away, emphasis/code markers are dropped, runs of
// whitespace (including the full-width space) collapse to one space, and the
// result is lower-cased.
func normalizeTableRowIdentity(cell string) string {
	cell = markdownEmphasisMarkers.Replace(cell)
	return strings.ToLower(strings.Join(strings.Fields(cell), " "))
}

// markdownTableRowIdentity returns the identity of a markdown table data row:
// its first non-empty cell, normalized. ok is false when every cell is empty,
// i.e. the row stands for no entity and cannot be missed.
func markdownTableRowIdentity(line string) (string, bool) {
	for _, cell := range strings.Split(line, "|") {
		if key := normalizeTableRowIdentity(cell); key != "" {
			return key, true
		}
	}
	return "", false
}

// tableRowIdentities returns the identity of every markdown table data row in
// body, in document order and de-duplicated. Header rows and the delimiter row
// under them are skipped: the header is the line directly above a delimiter row.
//
// Inline chunk citations ([c003]) are stripped first. The editor prompt never
// shows them, so the model cannot reproduce them, and the page write path strips
// them again before storage — comparing them would make every rewrite of a legacy
// page whose first column carried a citation look like a dropped row.
func tableRowIdentities(body string) []string {
	if body == "" {
		return nil
	}
	lines := strings.Split(stripWikiInlineChunkCitations(body), "\n")
	identities := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for i, line := range lines {
		if !isMarkdownTableRow(line) || isMarkdownTableDelimiterRow(line) {
			continue
		}
		if i+1 < len(lines) && isMarkdownTableRow(lines[i+1]) && isMarkdownTableDelimiterRow(lines[i+1]) {
			continue // header row, not data
		}
		identity, ok := markdownTableRowIdentity(line)
		if !ok {
			continue
		}
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		identities = append(identities, identity)
	}
	return identities
}

// rewriteDroppedRowKeys returns the table row identities that the old body had
// and the rewrite no longer contains — the rows the model silently dropped.
// Order follows the old body and each identity is reported once. A body without
// tables, or one whose rows all survive, yields nothing.
func rewriteDroppedRowKeys(existing, rewritten string) []string {
	existingKeys := tableRowIdentities(existing)
	if len(existingKeys) == 0 {
		return nil
	}
	kept := make(map[string]struct{})
	for _, key := range tableRowIdentities(rewritten) {
		kept[key] = struct{}{}
	}
	var dropped []string
	for _, key := range existingKeys {
		if _, ok := kept[key]; !ok {
			dropped = append(dropped, key)
		}
	}
	return dropped
}

// applyRewriteToPage is the single write-back path for an editor rewrite in
// reduceSlugUpdates. It returns applied=true after copying the rewrite onto the
// page, or applied=false plus the dropped row identities when the rewrite has to
// be refused.
//
// It refuses when the model left out table rows that are still on the page,
// because a page that keeps its previous body is recoverable — the rows are
// still in the source documents, so a later ingest can add the new information
// without the loss — while a silently shortened page is not.
//
// hasRetractions turns the guard off: a retract round removes content on purpose
// and the model is expected to drop the rows that belonged to the removed
// document. An empty old body (new page) or one without tables never blocks.
// splitSummaryLine semantics are unchanged from the caller's original inline
// version: the body replaces Content, and the SUMMARY: line replaces Summary only
// when the rewrite actually carried one.
func applyRewriteToPage(
	page *types.WikiPage,
	updatedContent string,
	hasRetractions bool,
) (applied bool, dropped []string) {
	if page == nil {
		return false, nil
	}

	updatedSummary, updatedBody := splitSummaryLine(updatedContent)
	// An empty body means the answer had no SUMMARY: prefix (or was that single
	// line), in which case the whole answer is the page, exactly as before.
	newBody := updatedBody
	if newBody == "" {
		newBody = updatedContent
	}

	if page.Content != "" && !hasRetractions {
		if dropped = rewriteDroppedRowKeys(page.Content, newBody); len(dropped) > 0 {
			return false, dropped
		}
	}

	page.Content = newBody
	if updatedSummary != "" {
		page.Summary = updatedSummary
	}
	return true, nil
}
