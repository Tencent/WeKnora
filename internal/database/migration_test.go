package database

import "testing"

func TestMigrationSourceURLSelectsBackendDirectory(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{name: "postgres", dsn: "postgres://user:pass@localhost/db", want: "file://migrations/versioned"},
		{name: "sqlite", dsn: "sqlite3://data/weknora.db", want: "file://migrations/sqlite"},
		{name: "mysql", dsn: "mysql://user:pass@tcp(localhost:3306)/weknora", want: "file://migrations/mysql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := migrationSourceURL(tt.dsn); got != tt.want {
				t.Fatalf("migrationSourceURL(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}
