package database

import (
	"encoding/json"
)

func CaseInsensitiveMatch(dialect, column string) string {
	if dialect == "postgres" {
		return column + " ILIKE ?"
	}
	return "LOWER(" + column + ") LIKE LOWER(?)"
}

func JSONScalarLookup(dialect, column, key string) (expression, keyArgument string) {
	switch dialect {
	case "postgres":
		return column + " ->> ?", key
	case "mysql":
		return "JSON_UNQUOTE(JSON_EXTRACT(" + column + ", ?))", jsonPathForKey(key)
	default:
		return "json_extract(" + column + ", ?)", jsonPathForKey(key)
	}
}

func jsonPathForKey(key string) string {
	encoded, _ := json.Marshal(key)
	return "$." + string(encoded)
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
