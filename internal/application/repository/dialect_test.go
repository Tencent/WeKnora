package repository

import (
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type testDialector string

func (d testDialector) Name() string {
	return string(d)
}

func (d testDialector) Initialize(*gorm.DB) error {
	return nil
}

func (d testDialector) Migrator(*gorm.DB) gorm.Migrator {
	return nil
}

func (d testDialector) DataTypeOf(*schema.Field) string {
	return ""
}

func (d testDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (d testDialector) BindVarTo(clause.Writer, *gorm.Statement, interface{}) {
}

func (d testDialector) QuoteTo(clause.Writer, string) {
}

func (d testDialector) Explain(sql string, _ ...interface{}) string {
	return sql
}

func dbWithDialect(name string) *gorm.DB {
	return &gorm.DB{Config: &gorm.Config{Dialector: testDialector(name)}}
}

func TestDialectHelpersCaseInsensitiveLike(t *testing.T) {
	if got := caseInsensitiveLikeCondition(dbWithDialect("postgres"), "title"); got != "title ILIKE ?" {
		t.Fatalf("postgres like = %q", got)
	}
	if got := caseInsensitiveLikeCondition(dbWithDialect("mysql"), "title"); got != "LOWER(title) LIKE LOWER(?)" {
		t.Fatalf("mysql like = %q", got)
	}
}

func TestDialectHelpersJSONExpressions(t *testing.T) {
	if got := jsonTextExpr(dbWithDialect("postgres"), "metadata", "model_id"); got != "metadata->>'model_id'" {
		t.Fatalf("postgres json text = %q", got)
	}
	if got := jsonTextExpr(dbWithDialect("mysql"), "metadata", "model_id"); got != "JSON_UNQUOTE(JSON_EXTRACT(metadata, '$.model_id'))" {
		t.Fatalf("mysql json text = %q", got)
	}
	if got := jsonTextCastExpr(dbWithDialect("mysql"), "source_refs"); got != "CAST(source_refs AS CHAR)" {
		t.Fatalf("mysql json cast = %q", got)
	}
}

func TestDialectHelpersSourceRefs(t *testing.T) {
	if got := sourceRefsContainsClause(dbWithDialect("postgres")); got != "source_refs @> ?::jsonb" {
		t.Fatalf("postgres source ref clause = %q", got)
	}
	if got := sourceRefsContainsClause(dbWithDialect("mysql")); got != "JSON_CONTAINS(source_refs, ?)" {
		t.Fatalf("mysql source ref clause = %q", got)
	}
	if got := sourceRefsContainsArg(dbWithDialect("sqlite"), "kid", `["kid"]`); got != "kid" {
		t.Fatalf("sqlite source ref arg = %q", got)
	}
}

func TestDialectHelpersTimeAndRandom(t *testing.T) {
	if got := nowSQL(dbWithDialect("mysql")); got != "NOW()" {
		t.Fatalf("mysql now = %q", got)
	}
	if got := nowSQL(dbWithDialect("sqlite")); got != "datetime('now')" {
		t.Fatalf("sqlite now = %q", got)
	}
	if got := randomOrderSQL(dbWithDialect("mysql")); got != "RAND()" {
		t.Fatalf("mysql random = %q", got)
	}
}
