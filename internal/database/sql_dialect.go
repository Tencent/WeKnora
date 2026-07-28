package database

import (
	"fmt"
	"regexp"
)

var jsonKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func CaseInsensitiveMatch(dialect, column string) string {
	if dialect == "postgres" {
		return column + " ILIKE ?"
	}
	return "LOWER(" + column + ") LIKE LOWER(?)"
}

func JSONScalarExpression(dialect, column, key string) (string, error) {
	if !jsonKeyPattern.MatchString(key) {
		return "", fmt.Errorf("invalid JSON key %q", key)
	}
	switch dialect {
	case "postgres":
		return fmt.Sprintf("%s ->> '%s'", column, key), nil
	case "mysql":
		return fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, '$.\"%s\"'))", column, key), nil
	default:
		return fmt.Sprintf("json_extract(%s, '$.\"%s\"')", column, key), nil
	}
}

func SupportsRowLock(dialect string) bool {
	return dialect == "postgres" || dialect == "mysql"
}

func CaseInsensitiveRegex(dialect, column string) string {
	switch dialect {
	case "postgres":
		return column + " ~* ?"
	case "mysql":
		return "REGEXP_LIKE(" + column + ", ?, 'i')"
	default:
		return column + " REGEXP ?"
	}
}

func JSONArrayLength(dialect, column string) string {
	switch dialect {
	case "postgres":
		return "COALESCE(jsonb_array_length(" + column + "), 0)"
	case "mysql":
		return "COALESCE(JSON_LENGTH(" + column + "), 0)"
	default:
		return "COALESCE(json_array_length(" + column + "), 0)"
	}
}

func JSONArrayContains(dialect, column string) string {
	switch dialect {
	case "postgres":
		return column + " @> ?::jsonb"
	case "mysql":
		return "JSON_CONTAINS(" + column + ", ?)"
	default:
		return "EXISTS (SELECT 1 FROM json_each(" + column + ") WHERE value = ?)"
	}
}

func JSONAsText(dialect, column string) string {
	switch dialect {
	case "postgres":
		return column + "::text"
	case "mysql":
		return "CAST(" + column + " AS CHAR)"
	default:
		return "CAST(" + column + " AS TEXT)"
	}
}

func JSONEquals(dialect, column string) string {
	switch dialect {
	case "postgres":
		return column + "::jsonb = ?::jsonb"
	case "mysql":
		return column + " = CAST(? AS JSON)"
	default:
		return column + " = ?"
	}
}
