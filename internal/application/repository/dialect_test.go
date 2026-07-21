package repository

import (
	"reflect"
	"testing"
)

func TestDialectCaseInsensitivePredicates(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "postgres", want: `"title" ILIKE ?`},
		{name: "mysql", want: "LOWER(`title`) LIKE LOWER(?)"},
		{name: "sqlite", want: `LOWER("title") LIKE LOWER(?)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (Dialect{name: tt.name}).CaseInsensitiveLike("title"); got != tt.want {
				t.Fatalf("CaseInsensitiveLike() = %q, want %q", got, tt.want)
			}
		})
	}

	if got := (Dialect{name: "mysql"}).CaseInsensitiveRegex("title"); got != "REGEXP_LIKE(`title`, ?, 'i')" {
		t.Fatalf("MySQL regex predicate = %q", got)
	}
}

func TestDialectJSONExpressions(t *testing.T) {
	mysql := Dialect{name: "mysql"}
	text := mysql.JSONText("metadata", "standard_question")
	if text.SQL != "JSON_UNQUOTE(JSON_EXTRACT(`metadata`, ?))" ||
		!reflect.DeepEqual(text.Args, []interface{}{`$."standard_question"`}) {
		t.Fatalf("unexpected MySQL JSONText: %#v", text)
	}

	length := mysql.JSONLength("metadata", "generated_questions")
	if length.SQL != "COALESCE(JSON_LENGTH(JSON_EXTRACT(`metadata`, ?)), 0)" ||
		!reflect.DeepEqual(length.Args, []interface{}{`$."generated_questions"`}) {
		t.Fatalf("unexpected MySQL JSONLength: %#v", length)
	}

	contains, err := mysql.JSONArrayContainsString("source_refs", `id"with\chars`)
	if err != nil {
		t.Fatalf("JSONArrayContainsString() error = %v", err)
	}
	if contains.SQL != "JSON_CONTAINS(COALESCE(`source_refs`, JSON_ARRAY()), CAST(? AS JSON))" {
		t.Fatalf("unexpected MySQL JSON contains SQL: %q", contains.SQL)
	}
}

func TestDialectIdentifierValidation(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected unsafe identifier panic")
		}
	}()
	(Dialect{name: "mysql"}).QuoteIdentifier("title; DROP TABLE users")
}

func TestDialectCapabilities(t *testing.T) {
	if !(Dialect{name: "mysql"}).SupportsSkipLocked() {
		t.Fatal("MySQL 8 should support SKIP LOCKED")
	}
	if (Dialect{name: "mysql"}).SupportsUpdateReturning() {
		t.Fatal("MySQL should not use UPDATE RETURNING")
	}
	if got := (Dialect{name: "mysql"}).RandomOrder(); got != "RAND()" {
		t.Fatalf("RandomOrder() = %q", got)
	}
	if got := (Dialect{name: "sqlite"}).CurrentTimestamp(); got != "datetime('now')" {
		t.Fatalf("CurrentTimestamp() = %q", got)
	}
}

func TestDialectUpsertValidatesColumns(t *testing.T) {
	d := Dialect{name: "mysql"}
	clause := d.Upsert([]string{"tenant_id", "name"}, []string{"updated_at"})
	if len(clause.Columns) != 2 || clause.Columns[0].Name != "tenant_id" {
		t.Fatalf("unexpected upsert clause: %#v", clause)
	}
}
