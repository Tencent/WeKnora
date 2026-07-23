package database

import (
	"testing"
)

// migrationsPathForDSN picks the directory of migration .sql files
// based on the database DSN's scheme.
//
//   - sqlite3:// → migrations/sqlite     (squash baseline, no version history)
//   - mysql://    → migrations/mysql      (squash baseline, no version history)
//   - anything else (postgres://, etc.) → migrations/versioned (the full
//     72-migration PostgreSQL history)
//
// These tests are the contract, written before the helper exists.
// The function will not compile until added to migration.go.

func TestMigrationsPathForDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "sqlite dsn resolves to sqlite baseline dir",
			dsn:  "sqlite3:///tmp/test.db",
			want: "file://migrations/sqlite",
		},
		{
			name: "mysql dsn resolves to mysql baseline dir",
			dsn:  "mysql://user:pass@tcp(host:3306)/db",
			want: "file://migrations/mysql",
		},
		{
			name: "postgres dsn resolves to versioned dir (default)",
			dsn:  "postgres://user:pass@host:5432/db?sslmode=disable",
			want: "file://migrations/versioned",
		},
		{
			name: "unknown scheme defaults to versioned dir",
			dsn:  "somethingelse://foo",
			want: "file://migrations/versioned",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := migrationsPathForDSN(tt.dsn)
			if got != tt.want {
				t.Fatalf("migrationsPathForDSN(%q) = %q; want %q", tt.dsn, got, tt.want)
			}
		})
	}
}
