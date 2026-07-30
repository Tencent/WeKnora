package repository

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

func dialectName(db *gorm.DB) string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}

func isPostgres(db *gorm.DB) bool {
	return dialectName(db) == "postgres"
}

func isMySQL(db *gorm.DB) bool {
	return dialectName(db) == "mysql"
}

func isSQLite(db *gorm.DB) bool {
	return dialectName(db) == "sqlite"
}

func supportsRowLevelLocking(db *gorm.DB) bool {
	switch dialectName(db) {
	case "postgres", "mysql":
		return true
	default:
		return false
	}
}

func caseInsensitiveLikeCondition(db *gorm.DB, expr string) string {
	if isPostgres(db) {
		return expr + " ILIKE ?"
	}
	return fmt.Sprintf("LOWER(%s) LIKE LOWER(?)", expr)
}

func jsonTextExpr(db *gorm.DB, column, path string) string {
	switch dialectName(db) {
	case "postgres":
		return fmt.Sprintf("%s->>'%s'", column, path)
	case "mysql":
		return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '$.%s'))", column, path)
	default:
		return fmt.Sprintf("json_extract(%s, '$.%s')", column, path)
	}
}

func jsonPathForKey(key string) string {
	key = strings.ReplaceAll(key, `\`, `\\`)
	key = strings.ReplaceAll(key, `"`, `\"`)
	return `$."` + key + `"`
}

func jsonTextCastExpr(db *gorm.DB, column string) string {
	if isPostgres(db) {
		return column + "::text"
	}
	if isMySQL(db) {
		return "CAST(" + column + " AS CHAR)"
	}
	return "CAST(" + column + " AS TEXT)"
}

func jsonPathTextCastExpr(db *gorm.DB, column, path string) string {
	switch dialectName(db) {
	case "postgres":
		return fmt.Sprintf("CAST(COALESCE(%s->'%s', '[]'::jsonb) AS TEXT)", column, path)
	case "mysql":
		return fmt.Sprintf("CAST(COALESCE(JSON_EXTRACT(%s, '$.%s'), JSON_ARRAY()) AS CHAR)", column, path)
	default:
		return fmt.Sprintf("CAST(COALESCE(json_extract(%s, '$.%s'), '[]') AS TEXT)", column, path)
	}
}

func jsonArrayLengthExpr(db *gorm.DB, column string) string {
	switch dialectName(db) {
	case "postgres":
		return fmt.Sprintf("jsonb_array_length(COALESCE(%s, '[]'::jsonb))", column)
	case "mysql":
		return fmt.Sprintf("COALESCE(JSON_LENGTH(%s), 0)", column)
	default:
		return fmt.Sprintf("COALESCE(json_array_length(%s), 0)", column)
	}
}

func jsonValueEqualsClause(db *gorm.DB, column string) string {
	switch dialectName(db) {
	case "postgres":
		return column + "::jsonb = ?::jsonb"
	case "mysql":
		// Compare binary JSON values rather than their serialized text. MySQL
		// inserts whitespace when JSON is cast to CHAR, while encoding/json does
		// not, so text equality rejects otherwise identical arrays.
		return column + " = JSON_EXTRACT(?, '$')"
	default:
		return column + " = ?"
	}
}

func nowSQL(db *gorm.DB) string {
	if isSQLite(db) {
		return "datetime('now')"
	}
	return "NOW()"
}

func randomOrderSQL(db *gorm.DB) string {
	if isMySQL(db) {
		return "RAND()"
	}
	return "RANDOM()"
}

func sourceRefsContainsClause(db *gorm.DB) string {
	switch dialectName(db) {
	case "postgres":
		return "source_refs @> ?::jsonb"
	case "mysql":
		return "JSON_CONTAINS(source_refs, ?)"
	default:
		return "EXISTS (SELECT 1 FROM json_each(source_refs) WHERE value = ?)"
	}
}

func sourceRefsContainsArg(db *gorm.DB, value, jsonArrayNeedle string) string {
	if isSQLite(db) {
		return value
	}
	return jsonArrayNeedle
}

func sourceRefsTextLikeClause(db *gorm.DB) string {
	return jsonTextCastExpr(db, "source_refs") + " LIKE ?"
}
