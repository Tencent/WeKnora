package repository

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/database"
	"gorm.io/gorm"
)

func wikiDialect(db *gorm.DB) string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}

func wikiSearchPredicate(dialect string) (string, int) {
	if dialect == "postgres" {
		return "(to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', ?) OR aliases::text ILIKE ?)", 2
	}
	return "(" +
		database.CaseInsensitiveMatch(dialect, "title") + " OR " +
		database.CaseInsensitiveMatch(dialect, "content") + " OR " +
		database.CaseInsensitiveMatch(dialect, "aliases") + ")", 3
}

func wikiContainmentArgument(dialect, encodedArray, scalar string) any {
	if dialect == "sqlite" {
		return scalar
	}
	return encodedArray
}

func wikiSimilarRank(dialect string) string {
	switch dialect {
	case "postgres":
		return "similarity(lower(title), ?) AS sim"
	case "mysql":
		return "CASE WHEN LOWER(title) LIKE CONCAT('%', LOWER(?), '%') THEN 1 ELSE 0 END AS sim"
	default:
		return "CASE WHEN LOWER(title) LIKE '%' || LOWER(?) || '%' THEN 1 ELSE 0 END AS sim"
	}
}

func wikiSimilarPredicate(dialect string) string {
	switch dialect {
	case "postgres":
		return "lower(title) % ?"
	case "mysql":
		return "LOWER(title) LIKE CONCAT('%', LOWER(?), '%')"
	default:
		return "LOWER(title) LIKE '%' || LOWER(?) || '%'"
	}
}

func wikiSimilarOrder(dialect string) string {
	if dialect == "mysql" {
		return "sim DESC, CHAR_LENGTH(title) ASC, updated_at DESC"
	}
	if dialect == "sqlite" {
		return "sim DESC, LENGTH(title) ASC, updated_at DESC"
	}
	return "sim DESC"
}

func wikiRegexRank(dialect string) string {
	return fmt.Sprintf(
		"CASE WHEN %s THEN 4 WHEN %s THEN 3 WHEN %s THEN 2 WHEN %s THEN 1 ELSE 0 END AS match_rank",
		database.CaseInsensitiveRegex(dialect, "title"),
		database.CaseInsensitiveRegex(dialect, "slug"),
		database.CaseInsensitiveRegex(dialect, "summary"),
		database.CaseInsensitiveRegex(dialect, "content"),
	)
}

func wikiRegexPredicate(dialect string) string {
	return "(" +
		database.CaseInsensitiveRegex(dialect, "title") + " OR " +
		database.CaseInsensitiveRegex(dialect, "content") + " OR " +
		database.CaseInsensitiveRegex(dialect, "summary") + " OR " +
		database.CaseInsensitiveRegex(dialect, "slug") + ")"
}
