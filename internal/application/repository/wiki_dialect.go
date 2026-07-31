package repository

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/database"
	"gorm.io/gorm"
)

// Dialect-aware SQL fragments for wiki_page.go. Each helper returns a
// per-dialect SQL expression; PG keeps its native operators, MySQL/SQLite
// get portable equivalents. Where PG has no MySQL analogue (trigram
// similarity, full-text search), the fallback uses LIKE with different
// matching and ranking semantics.

func wikiJSONArrayLength(dialectName, column string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("COALESCE(jsonb_array_length(%s), 0)", column)
	case "mysql":
		return fmt.Sprintf("COALESCE(JSON_LENGTH(%s), 0)", column)
	default:
		return fmt.Sprintf("COALESCE(json_array_length(%s), 0)", column)
	}
}

// wikiJSONContains tests JSON array containment. SQLite uses json_each;
// callers must use wikiJSONContainsArg to shape the bind value.
func wikiJSONContains(dialectName, column string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("%s @> ?::jsonb", column)
	case "mysql":
		return fmt.Sprintf("JSON_CONTAINS(%s, ?)", column)
	default:
		return fmt.Sprintf("EXISTS (SELECT 1 FROM json_each(%s) WHERE value = ?)", column)
	}
}

// wikiJSONContainsArg: PG/MySQL get the JSON-encoded array; SQLite gets
// the bare scalar (json_each compares with =).
func wikiJSONContainsArg(dialectName, needleJSON, scalarValue string) string {
	if dialectName == "sqlite" {
		return scalarValue
	}
	return needleJSON
}

func wikiJSONAsText(dialectName, column string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("%s::text", column)
	case "mysql":
		return fmt.Sprintf("CAST(%s AS CHAR)", column)
	default:
		return fmt.Sprintf("CAST(%s AS TEXT)", column)
	}
}

func wikiJSONEqual(dialectName, column string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("%s::jsonb = ?::jsonb", column)
	case "mysql":
		return fmt.Sprintf("%s = CAST(? AS JSON)", column)
	default:
		return fmt.Sprintf("%s = ?", column)
	}
}

// wikiCaseInsensitiveRegex: PG ~*, MySQL REGEXP_LIKE('i'), SQLite LIKE.
func wikiCaseInsensitiveRegex(dialectName, column, placeholder string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("%s ~* %s", column, placeholder)
	case "mysql":
		return fmt.Sprintf("REGEXP_LIKE(%s, %s, 'i')", column, placeholder)
	default:
		return fmt.Sprintf("%s LIKE %s", column, placeholder)
	}
}

// wikiSimilarityRank: PG trigram similarity; MySQL/SQLite degrade to
// a binary "contains" match (1/0).
func wikiSimilarityRank(dialectName, column, placeholder string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("similarity(lower(%s), %s)", column, placeholder)
	case "mysql":
		return fmt.Sprintf("(CASE WHEN LOWER(%s) LIKE CONCAT('%%', LOWER(%s), '%%') THEN 1 ELSE 0 END)", column, placeholder)
	default:
		return fmt.Sprintf("(CASE WHEN LOWER(%s) LIKE '%%' || LOWER(%s) || '%%' THEN 1 ELSE 0 END)", column, placeholder)
	}
}

func wikiSimilarityThreshold(dialectName, column, placeholder string) string {
	switch dialectName {
	case "postgres":
		return fmt.Sprintf("lower(%s) %% %s", column, placeholder)
	case "mysql":
		return fmt.Sprintf("LOWER(%s) LIKE CONCAT('%%', LOWER(%s), '%%')", column, placeholder)
	default:
		return fmt.Sprintf("LOWER(%s) LIKE '%%' || LOWER(%s) || '%%'", column, placeholder)
	}
}

// wikiFullTextSearch: PG to_tsvector; MySQL/SQLite use multi-column LIKE.
func wikiFullTextSearch(dialectName string) string {
	switch dialectName {
	case "postgres":
		return "(to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) " +
			"@@ plainto_tsquery('simple', ?) OR aliases::text ILIKE ?)"
	default:
		return "(" + database.CaseInsensitiveLike(dialectName, "title", "?") +
			" OR " + database.CaseInsensitiveLike(dialectName, "content", "?") +
			" OR " + database.CaseInsensitiveLike(dialectName, "aliases", "?") + ")"
	}
}

func wikiDialectName(db *gorm.DB) string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}
