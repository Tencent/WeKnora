package repository

import (
	"encoding/json"
	"strings"

	"gorm.io/gorm"
)

func dialectName(db *gorm.DB) string {
	if db == nil || db.Dialector == nil {
		return ""
	}
	return db.Dialector.Name()
}

func isMySQL(db *gorm.DB) bool {
	return dialectName(db) == "mysql"
}

func isPostgres(db *gorm.DB) bool {
	return dialectName(db) == "postgres"
}

func isSQLite(db *gorm.DB) bool {
	return dialectName(db) == "sqlite"
}

func caseInsensitiveLikeExpr(db *gorm.DB, column string) string {
	if isPostgres(db) {
		return column + " ILIKE ?"
	}
	return "LOWER(" + column + ") LIKE LOWER(?)"
}

func mysqlJSONContainsExpr(column string) string {
	return "JSON_CONTAINS(" + column + ", ?)"
}

func jsonTextExpr(db *gorm.DB, column string) string {
	switch dialectName(db) {
	case "postgres":
		return column + "::text"
	case "mysql":
		return "CAST(" + column + " AS CHAR)"
	default:
		return column
	}
}

func jsonPathForKey(key string) string {
	raw, err := json.Marshal(key)
	if err != nil {
		return "$." + key
	}
	return "$." + string(raw)
}

func nowExpr(db *gorm.DB) string {
	if isSQLite(db) {
		return "CURRENT_TIMESTAMP"
	}
	return "NOW()"
}

func escapeLikeKeyword(keyword string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(keyword)
}
