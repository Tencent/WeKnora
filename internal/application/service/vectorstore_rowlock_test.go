package service

import (
	"testing"
)

// dialectSupportsRowLocking reports whether a GORM dialector with the
// given name supports SELECT ... FOR UPDATE row-level locking. It
// backs vectorStoreService.supportsRowLocking (renamed from
// isPostgres), which gates the clause.Locking{Strength: "UPDATE"}
// clause in DeleteStore.
//
// PostgreSQL and MySQL both support FOR UPDATE. SQLite does not (it
// serialises via database-level locking), so the clause is skipped to
// avoid SQL parse errors on older SQLite versions.
//
// The rename from isPostgres is deliberate: the method's contract is
// about a capability (row locking), not a dialect identity. Under
// DB_DRIVER=mysql the delete guard must still take the row lock —
// otherwise concurrent deletes can pass the KB-count check
// simultaneously and leave dangling bindings.
//
// These tests are written before the helper exists (TDD red).

func TestDialectSupportsRowLocking(t *testing.T) {
	tests := []struct {
		dialect string
		want    bool
	}{
		{"postgres", true},
		{"mysql", true},
		{"sqlite", false},
		{"", false},
		{"sqlserver", false},
	}

	for _, tt := range tests {
		t.Run(tt.dialect, func(t *testing.T) {
			got := dialectSupportsRowLocking(tt.dialect)
			if got != tt.want {
				t.Fatalf("dialectSupportsRowLocking(%q) = %v; want %v", tt.dialect, got, tt.want)
			}
		})
	}
}
