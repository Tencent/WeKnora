package oracle

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// Column names. Oracle folds unquoted identifiers to upper case, so they are
// written lower case here and never quoted.
const (
	fieldID              = "id"
	fieldContent         = "content"
	fieldSourceID        = "source_id"
	fieldSourceType      = "source_type"
	fieldChunkID         = "chunk_id"
	fieldKnowledgeID     = "knowledge_id"
	fieldKnowledgeBaseID = "knowledge_base_id"
	fieldTagID           = "tag_id"
	fieldIsEnabled       = "is_enabled"
	fieldEmbedding       = "embedding"
)

var columnsForRetrieve = []string{
	fieldID, fieldContent, fieldSourceID, fieldSourceType,
	fieldChunkID, fieldKnowledgeID, fieldKnowledgeBaseID, fieldTagID,
	fieldIsEnabled,
}

// insertColumns is the INSERT column order used by insertRows.
var insertColumns = append(append([]string{}, columnsForRetrieve...), fieldEmbedding)

// maxInListSize stays below Oracle's 1000-element IN-list limit (ORA-01795).
const maxInListSize = 500

// sanitizeTableBase turns a user table prefix into an unquoted Oracle
// identifier (see types.OracleTableBase); anything invalid falls back to def.
// This is the only user string that is spliced into SQL text, so it must stay
// strictly validated. ValidateOracleIndexConfig rejects such prefixes for
// UI-created stores; the fallback still guards env configuration.
func sanitizeTableBase(name, def string) string {
	if base, ok := types.OracleTableBase(name); ok {
		return base
	}
	return strings.ToUpper(def)
}

// binds accumulates positional bind values and hands out ":N" placeholders in
// the order they appear in the SQL text.
type binds struct{ args []any }

func (b *binds) add(v any) string {
	b.args = append(b.args, v)
	return ":" + strconv.Itoa(len(b.args))
}

// maxVarcharBind is the VARCHAR2 bind limit with MAX_STRING_SIZE=STANDARD.
const maxVarcharBind = 4000

// text binds s as one VARCHAR2 or, when longer than maxVarcharBind, as
// VARCHAR2 pieces concatenated server-side: TO_CLOB(:1) || :2 || ...
// This avoids go-ora CLOB binds, which create a temporary LOB (several extra
// round trips) per value; on a high-latency link that dominated write time.
func (b *binds) text(s string) string {
	if len(s) <= maxVarcharBind {
		return b.add(s)
	}
	var parts []string
	for len(s) > 0 {
		n := min(len(s), maxVarcharBind)
		for n < len(s) && !utf8.RuneStart(s[n]) { // never split a UTF-8 sequence
			n--
		}
		parts = append(parts, b.add(s[:n]))
		s = s[n:]
	}
	parts[0] = "TO_CLOB(" + parts[0] + ")"
	return strings.Join(parts, " || ")
}

func (b *binds) in(values []string) string {
	ph := make([]string, len(values))
	for i, v := range values {
		ph[i] = b.add(v)
	}
	return strings.Join(ph, ", ")
}

// whereFilter renders the RetrieveParams filters. Disabled chunks never match.
func whereFilter(b *binds, params types.RetrieveParams) string {
	conds := []string{fieldIsEnabled + " = 1"}
	addIn := func(field, op string, values []string) {
		if len(values) == 0 {
			return
		}
		// Split long lists into OR/AND groups to respect the IN-list limit.
		var parts []string
		for _, chunk := range chunkStrings(values, maxInListSize) {
			parts = append(parts, field+" "+op+" ("+b.in(chunk)+")")
		}
		joiner := " OR "
		if op == "NOT IN" {
			joiner = " AND "
		}
		conds = append(conds, "("+strings.Join(parts, joiner)+")")
	}
	addIn(fieldKnowledgeBaseID, "IN", params.KnowledgeBaseIDs)
	addIn(fieldKnowledgeID, "IN", params.KnowledgeIDs)
	addIn(fieldTagID, "IN", params.TagIDs)
	addIn(fieldKnowledgeID, "NOT IN", params.ExcludeKnowledgeIDs)
	addIn(fieldChunkID, "NOT IN", params.ExcludeChunkIDs)
	return strings.Join(conds, " AND ")
}

func chunkStrings(values []string, size int) [][]string {
	var out [][]string
	for len(values) > size {
		out = append(out, values[:size])
		values = values[size:]
	}
	if len(values) > 0 {
		out = append(out, values)
	}
	return out
}

// maxQueryTerms caps the Oracle Text query size.
const maxQueryTerms = 64

// buildTextQuery converts free text into an Oracle Text CONTAINS query that
// ranks rows by how many terms they match (ACCUM ≈ "match any").
//
// Text is split at script boundaries (Latin/digits, Han, Katakana, Hiragana,
// Hangul). Hiragana-only runs are dropped (mostly Japanese particles) and Han
// runs longer than two characters become overlapping bigrams, so a sentence
// without spaces still yields matchable terms. Every term is wrapped in {}
// so Oracle Text operators/reserved words in user input are taken literally.
//
// ponytail: script-run + bigram heuristic, swap for a morphological tokenizer
// (e.g. Sudachi) if keyword recall on Japanese text matters.
func buildTextQuery(query string) string {
	seen := map[string]bool{}
	var terms []string
	add := func(t string) {
		if t == "" || seen[t] || len(terms) >= maxQueryTerms {
			return
		}
		seen[t] = true
		terms = append(terms, "{"+t+"}")
	}
	for _, run := range scriptRuns(query) {
		r := []rune(run)
		switch {
		case unicode.Is(unicode.Hiragana, r[0]):
			continue
		case unicode.Is(unicode.Han, r[0]) && len(r) > 2:
			for i := 0; i+1 < len(r); i++ {
				add(string(r[i : i+2]))
			}
		default:
			add(run)
		}
	}
	return strings.Join(terms, " ACCUM ")
}

func scriptOf(r rune) int {
	switch {
	case unicode.Is(unicode.Han, r):
		return 1
	case unicode.Is(unicode.Katakana, r) || r == 'ー':
		return 2
	case unicode.Is(unicode.Hiragana, r):
		return 3
	case unicode.Is(unicode.Hangul, r):
		return 4
	case unicode.IsLetter(r) || unicode.IsDigit(r):
		return 5
	}
	return 0 // separator
}

func scriptRuns(s string) []string {
	var runs []string
	var cur []rune
	curScript := 0
	flush := func() {
		if len(cur) > 0 {
			runs = append(runs, string(cur))
		}
		cur = cur[:0]
	}
	for _, r := range s {
		sc := scriptOf(r)
		if sc != curScript {
			flush()
			curScript = sc
		}
		if sc != 0 {
			cur = append(cur, r)
		}
	}
	flush()
	return runs
}

func validTopK(k int) int {
	if k <= 0 {
		return 10
	}
	if k > 2000 {
		return 2000
	}
	return k
}

func tableName(base string, dim int) string { return fmt.Sprintf("%s_%d", base, dim) }
