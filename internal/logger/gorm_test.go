package logger

import (
	"context"
	"strings"
	"testing"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

// The slow-query path must keep its diagnostics: the statement and the elapsed
// time are what an operator needs to triage a heavy batch. Only the bind
// values are withheld, because those carry chunk text and embedding payloads.
func TestNewGORMKeepsSlowQueryDiagnostics(t *testing.T) {
	var out strings.Builder
	lg := NewGORMTo(&out)

	lg.Trace(context.Background(), time.Now().Add(-time.Second), func() (string, int64) {
		return "INSERT INTO embeddings (content, embedding) VALUES (?, ?)", 40
	}, nil)

	written := out.String()
	if !strings.Contains(written, "SLOW SQL") {
		t.Fatalf("slow query warning missing, out=%q", written)
	}
	if !strings.Contains(written, "INSERT INTO embeddings") {
		t.Fatalf("slow query statement missing, out=%q", written)
	}
}

// GORM routes every Explain through ParamsFilter. The parameterized mode is
// the contract that keeps bind values out of the log, so it is asserted
// directly rather than inferred from the Config struct.
func TestNewGORMParameterizesSQL(t *testing.T) {
	lg := NewGORMTo(&strings.Builder{})

	filter, ok := lg.(interface {
		ParamsFilter(context.Context, string, ...any) (string, []any)
	})
	if !ok {
		t.Fatal("gorm logger does not implement ParamsFilter")
	}

	sql, vars := filter.ParamsFilter(
		context.Background(),
		"INSERT INTO embeddings (content, embedding) VALUES (?, ?)",
		"SECRET_CHUNK_TEXT", "VECTOR_PAYLOAD",
	)
	if sql == "" {
		t.Fatal("parameterized mode must keep the SQL statement")
	}
	if len(vars) != 0 {
		t.Fatalf("parameterized mode must drop bind values, got %v", vars)
	}

	// Prove the assertion is meaningful: the same statement with bind values
	// does render the sensitive payload, while the filtered form never does.
	withValues := gormlogger.ExplainSQL(sql, nil, `'`, "SECRET_CHUNK_TEXT", "VECTOR_PAYLOAD")
	if !strings.Contains(withValues, "SECRET_CHUNK_TEXT") ||
		!strings.Contains(withValues, "VECTOR_PAYLOAD") {
		t.Fatalf("control render did not include bind values: %q", withValues)
	}
	withoutValues := gormlogger.ExplainSQL(sql, nil, `'`, vars...)
	if strings.Contains(withoutValues, "SECRET_CHUNK_TEXT") ||
		strings.Contains(withoutValues, "VECTOR_PAYLOAD") {
		t.Fatalf("parameterized render leaked bind values: %q", withoutValues)
	}
}
