package repository

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var safeIdentifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SQLExpression is a parameterized SQL fragment. Identifiers are validated
// separately and values stay in Args.
type SQLExpression struct {
	SQL  string
	Args []interface{}
}

// Dialect centralizes small, repeated SQL differences. Business-specific
// queries remain in their repositories.
type Dialect struct {
	name string
}

func NewDialect(db *gorm.DB) Dialect {
	if db == nil || db.Dialector == nil {
		return Dialect{}
	}
	return Dialect{name: strings.ToLower(db.Dialector.Name())}
}

func (d Dialect) Name() string {
	return d.name
}

func (d Dialect) IsPostgres() bool {
	return d.name == "postgres"
}

func (d Dialect) IsMySQL() bool {
	return d.name == "mysql"
}

// QuoteIdentifier quotes a trusted table or column identifier. An invalid
// identifier is a programming error, so it fails immediately.
func (d Dialect) QuoteIdentifier(identifier string) string {
	parts := strings.Split(identifier, ".")
	quoted := make([]string, 0, len(parts))
	quote := `"`
	if d.IsMySQL() {
		quote = "`"
	}
	for _, part := range parts {
		if !safeIdentifierPattern.MatchString(part) {
			panic(fmt.Sprintf("unsafe SQL identifier %q", identifier))
		}
		quoted = append(quoted, quote+part+quote)
	}
	return strings.Join(quoted, ".")
}

func (d Dialect) CaseInsensitiveLike(identifier string) string {
	column := d.QuoteIdentifier(identifier)
	if d.IsPostgres() {
		return column + " ILIKE ?"
	}
	return "LOWER(" + column + ") LIKE LOWER(?)"
}

// CaseInsensitiveLikeExpr applies a case-insensitive LIKE comparison to a
// parameterized SQL expression, preserving its existing arguments.
func (d Dialect) CaseInsensitiveLikeExpr(expr SQLExpression, value interface{}) SQLExpression {
	args := append([]interface{}{}, expr.Args...)
	args = append(args, value)
	if d.IsPostgres() {
		return SQLExpression{
			SQL:  "(" + expr.SQL + ") ILIKE ?",
			Args: args,
		}
	}
	return SQLExpression{
		SQL:  "LOWER(" + expr.SQL + ") LIKE LOWER(?)",
		Args: args,
	}
}

// CaseInsensitiveRegex returns a parameterized, case-insensitive regular
// expression predicate for a trusted text column.
func (d Dialect) CaseInsensitiveRegex(identifier string) string {
	column := d.QuoteIdentifier(identifier)
	switch d.name {
	case "postgres":
		return column + " ~* ?"
	case "mysql":
		return "REGEXP_LIKE(" + column + ", ?, 'i')"
	default:
		return column + " REGEXP ('(?i)' || ?)"
	}
}

// CastText converts a trusted column to text for comparisons and display.
func (d Dialect) CastText(identifier string) string {
	column := d.QuoteIdentifier(identifier)
	if d.IsMySQL() {
		return "CAST(" + column + " AS CHAR)"
	}
	return "CAST(" + column + " AS TEXT)"
}

// CurrentTimestamp returns the database-side current timestamp expression.
func (d Dialect) CurrentTimestamp() string {
	if d.name == "sqlite" {
		return "datetime('now')"
	}
	return "NOW()"
}

// RandomOrder returns the dialect's random ordering function.
func (d Dialect) RandomOrder() string {
	if d.IsMySQL() {
		return "RAND()"
	}
	return "RANDOM()"
}

// JSONText extracts a nested JSON value as text.
func (d Dialect) JSONText(identifier string, path ...string) SQLExpression {
	column := d.QuoteIdentifier(identifier)
	switch d.name {
	case "postgres":
		placeholders := make([]string, len(path))
		args := make([]interface{}, len(path))
		for i, key := range path {
			placeholders[i] = "?"
			args[i] = key
		}
		return SQLExpression{
			SQL:  fmt.Sprintf("jsonb_extract_path_text(%s, %s)", column, strings.Join(placeholders, ", ")),
			Args: args,
		}
	case "mysql":
		return SQLExpression{
			SQL:  fmt.Sprintf("JSON_UNQUOTE(JSON_EXTRACT(%s, ?))", column),
			Args: []interface{}{jsonPath(path)},
		}
	default:
		return SQLExpression{
			SQL:  fmt.Sprintf("json_extract(%s, ?)", column),
			Args: []interface{}{jsonPath(path)},
		}
	}
}

// JSONSet returns a complete assignment expression for a JSON-compatible
// value. Values are encoded once and passed as a bound parameter.
func (d Dialect) JSONSet(identifier string, value interface{}, path ...string) (SQLExpression, error) {
	column := d.QuoteIdentifier(identifier)
	encoded, err := json.Marshal(value)
	if err != nil {
		return SQLExpression{}, fmt.Errorf("encode JSON value: %w", err)
	}
	switch d.name {
	case "postgres":
		return SQLExpression{
			SQL:  fmt.Sprintf("jsonb_set(COALESCE(%s, '{}'::jsonb), ?::text[], ?::jsonb, true)", column),
			Args: []interface{}{postgresJSONPath(path), string(encoded)},
		}, nil
	case "mysql":
		return SQLExpression{
			SQL:  fmt.Sprintf("JSON_SET(COALESCE(%s, JSON_OBJECT()), ?, CAST(? AS JSON))", column),
			Args: []interface{}{jsonPath(path), string(encoded)},
		}, nil
	default:
		return SQLExpression{
			SQL:  fmt.Sprintf("json_set(COALESCE(%s, '{}'), ?, json(?))", column),
			Args: []interface{}{jsonPath(path), string(encoded)},
		}, nil
	}
}

// JSONLength returns the length of a JSON array at the requested path. Missing
// values are treated as empty arrays.
func (d Dialect) JSONLength(identifier string, path ...string) SQLExpression {
	column := d.QuoteIdentifier(identifier)
	switch d.name {
	case "postgres":
		if len(path) == 0 {
			return SQLExpression{
				SQL: fmt.Sprintf("jsonb_array_length(COALESCE(%s, '[]'::jsonb))", column),
			}
		}
		return SQLExpression{
			SQL:  fmt.Sprintf("jsonb_array_length(COALESCE(%s #> ?::text[], '[]'::jsonb))", column),
			Args: []interface{}{postgresJSONPath(path)},
		}
	case "mysql":
		if len(path) == 0 {
			return SQLExpression{SQL: fmt.Sprintf("COALESCE(JSON_LENGTH(%s), 0)", column)}
		}
		return SQLExpression{
			SQL:  fmt.Sprintf("COALESCE(JSON_LENGTH(JSON_EXTRACT(%s, ?)), 0)", column),
			Args: []interface{}{jsonPath(path)},
		}
	default:
		if len(path) == 0 {
			return SQLExpression{SQL: fmt.Sprintf("COALESCE(json_array_length(%s), 0)", column)}
		}
		return SQLExpression{
			SQL:  fmt.Sprintf("COALESCE(json_array_length(json_extract(%s, ?)), 0)", column),
			Args: []interface{}{jsonPath(path)},
		}
	}
}

// JSONEquals compares a JSON column with a Go value using the native JSON
// representation of each supported database.
func (d Dialect) JSONEquals(identifier string, value interface{}) (SQLExpression, error) {
	column := d.QuoteIdentifier(identifier)
	encoded, err := json.Marshal(value)
	if err != nil {
		return SQLExpression{}, fmt.Errorf("encode JSON comparison value: %w", err)
	}
	switch d.name {
	case "postgres":
		return SQLExpression{
			SQL:  column + " = ?::jsonb",
			Args: []interface{}{string(encoded)},
		}, nil
	case "mysql":
		return SQLExpression{
			SQL:  "JSON_EXTRACT(" + column + ", '$') = CAST(? AS JSON)",
			Args: []interface{}{string(encoded)},
		}, nil
	default:
		return SQLExpression{
			SQL:  "json(" + column + ") = json(?)",
			Args: []interface{}{string(encoded)},
		}, nil
	}
}

// JSONArrayContainsString checks whether a JSON array contains an exact string
// value. It is used for business metadata such as wiki source references.
func (d Dialect) JSONArrayContainsString(identifier, value string) (SQLExpression, error) {
	column := d.QuoteIdentifier(identifier)
	switch d.name {
	case "postgres":
		needle, err := json.Marshal([]string{value})
		if err != nil {
			return SQLExpression{}, fmt.Errorf("encode JSON array needle: %w", err)
		}
		return SQLExpression{
			SQL:  column + " @> ?::jsonb",
			Args: []interface{}{string(needle)},
		}, nil
	case "mysql":
		needle, err := json.Marshal([]string{value})
		if err != nil {
			return SQLExpression{}, fmt.Errorf("encode JSON array needle: %w", err)
		}
		return SQLExpression{
			SQL:  "JSON_CONTAINS(COALESCE(" + column + ", JSON_ARRAY()), CAST(? AS JSON))",
			Args: []interface{}{string(needle)},
		}, nil
	default:
		return SQLExpression{
			SQL:  "EXISTS (SELECT 1 FROM json_each(COALESCE(" + column + ", '[]')) WHERE value = ?)",
			Args: []interface{}{value},
		}, nil
	}
}

func (d Dialect) SupportsUpdateReturning() bool {
	return d.IsPostgres()
}

func (d Dialect) SupportsSkipLocked() bool {
	return d.IsPostgres() || d.IsMySQL()
}

// Upsert returns a structured GORM clause translated by each supported driver.
func (d Dialect) Upsert(conflictColumns, updateColumns []string) clause.OnConflict {
	columns := make([]clause.Column, 0, len(conflictColumns))
	for _, name := range conflictColumns {
		if !safeIdentifierPattern.MatchString(name) {
			panic(fmt.Sprintf("unsafe upsert column %q", name))
		}
		columns = append(columns, clause.Column{Name: name})
	}
	for _, name := range updateColumns {
		if !safeIdentifierPattern.MatchString(name) {
			panic(fmt.Sprintf("unsafe upsert column %q", name))
		}
	}
	return clause.OnConflict{
		Columns:   columns,
		DoUpdates: clause.AssignmentColumns(updateColumns),
	}
}

func jsonPath(path []string) string {
	var builder strings.Builder
	builder.WriteByte('$')
	for _, key := range path {
		escaped := strings.ReplaceAll(key, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		builder.WriteString(`."`)
		builder.WriteString(escaped)
		builder.WriteByte('"')
	}
	return builder.String()
}

func postgresJSONPath(path []string) string {
	escaped := make([]string, len(path))
	for i, key := range path {
		key = strings.ReplaceAll(key, `\`, `\\`)
		key = strings.ReplaceAll(key, `"`, `\"`)
		escaped[i] = `"` + key + `"`
	}
	return "{" + strings.Join(escaped, ",") + "}"
}
