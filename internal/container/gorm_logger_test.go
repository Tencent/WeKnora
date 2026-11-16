package container

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestNewGormLoggerSlowSQLOmitsBoundParameters is the regression test for
// issue #4543: a slow statement must still log SQL text and timing, but the
// bound chunk text and embedding vector must not appear in the log line.
//
// The production break this catches is ParameterizedQueries left false (or the
// logger omitted), which makes GORM inline every bound value before Trace.
func TestNewGormLoggerSlowSQLOmitsBoundParameters(t *testing.T) {
	var buf bytes.Buffer
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: newGormLogger(&buf),
	})
	if err != nil {
		t.Fatalf("gorm.Open: %v", err)
	}
	// GORM's default slow threshold is 200ms. Sleep inside the callback so the
	// measured statement is slow without depending on the host database.
	if err := db.Callback().Raw().Before("gorm:raw").Register("test:slow", func(*gorm.DB) {
		time.Sleep(250 * time.Millisecond)
	}); err != nil {
		t.Fatalf("register slow callback: %v", err)
	}

	const chunk = "SECRET_CHUNK_TEXT_do_not_log_9f3a7c1e"
	vector := "[" + strings.Repeat("0.123456789,", 32) + "0.5]"
	if err := db.Exec("SELECT ?, ?", chunk, vector).Error; err != nil {
		t.Fatalf("exec: %v", err)
	}

	logged := buf.String()
	if strings.Contains(logged, chunk) {
		t.Fatalf("slow SQL log included bound chunk text:\n%s", logged)
	}
	if strings.Contains(logged, "0.123456789") {
		t.Fatalf("slow SQL log included embedding vector values:\n%s", logged)
	}
	if !strings.Contains(logged, "SLOW SQL") {
		t.Fatalf("expected a slow SQL log entry, got %q", logged)
	}
	if !strings.Contains(logged, "SELECT") {
		t.Fatalf("slow SQL log should still include the SQL text, got %q", logged)
	}
	if !strings.Contains(logged, "?") {
		t.Fatalf("slow SQL log should keep placeholders, got %q", logged)
	}
}
