package database

import "testing"

func TestMigrationSourceURL(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "postgres uses versioned migrations",
			dsn:  "postgres://user:pass@localhost:5432/WeKnora?sslmode=disable",
			want: "file://migrations/versioned",
		},
		{
			name: "sqlite uses sqlite migrations",
			dsn:  "sqlite3:///tmp/weknora.db",
			want: "file://migrations/sqlite",
		},
		{
			name: "mysql uses mysql migrations",
			dsn:  "mysql://user:pass@tcp(localhost:3306)/WeKnora?parseTime=true",
			want: "file://migrations/mysql",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := migrationSourceURL(tt.dsn); got != tt.want {
				t.Fatalf("migrationSourceURL(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}
