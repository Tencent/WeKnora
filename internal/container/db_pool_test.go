package container

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// Failure modes: the variable is ignored (pool stays unlimited and the server
// refuses connections past max_connections); a bad value crashes startup or
// silently sets an unintended cap such as 0 or a negative number; an unset
// variable changes the existing unlimited default.
func TestApplyDBMaxOpenConns(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want int
	}{
		{name: "unset keeps the unlimited default", env: "", want: 0},
		{name: "positive value caps the pool", env: "40", want: 40},
		{name: "surrounding spaces are tolerated", env: " 25 ", want: 25},
		{name: "zero means unlimited", env: "0", want: 0},
		{name: "negative value is ignored", env: "-5", want: 0},
		{name: "non-numeric value is ignored", env: "forty", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DB_MAX_OPEN_CONNS", tc.env)
			// sql.Open does not dial, so no server is needed to inspect the pool.
			sqlDB, err := sql.Open("pgx", "postgres://user@127.0.0.1:1/db")
			if err != nil {
				t.Fatalf("sql.Open: %v", err)
			}
			t.Cleanup(func() { _ = sqlDB.Close() })

			applyDBMaxOpenConns(context.Background(), sqlDB)

			if got := sqlDB.Stats().MaxOpenConnections; got != tc.want {
				t.Fatalf("MaxOpenConnections = %d, want %d", got, tc.want)
			}
		})
	}
}
