package container

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A failed startup migration must abort by default (issue #5481). Only an
// explicit AUTO_MIGRATE_FAIL_OPEN=true restores warn-and-continue behavior.
func TestRunStartupMigrations(t *testing.T) {
	migrationErr := errors.New("Error 1050: table already dirty")
	cases := []struct {
		name    string
		env     string
		unset   bool
		err     error
		wantErr bool
	}{
		{name: "unset fails fast", unset: true, err: migrationErr, wantErr: true},
		{name: "empty fails fast", err: migrationErr, wantErr: true},
		{name: "false fails fast", env: "false", err: migrationErr, wantErr: true},
		{name: "uppercase true fails fast", env: "TRUE", err: migrationErr, wantErr: true},
		{name: "explicit true keeps starting", env: "true", err: migrationErr},
		{name: "successful migration keeps starting"},
		{name: "successful fail-open migration keeps starting", env: "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Setenv preserves the parent's value even when testing an absent variable.
			t.Setenv("AUTO_MIGRATE_FAIL_OPEN", tc.env)
			if tc.unset {
				if err := os.Unsetenv("AUTO_MIGRATE_FAIL_OPEN"); err != nil {
					t.Fatal(err)
				}
			}

			calls := 0
			err := runStartupMigrations(func() error {
				calls++
				return tc.err
			})
			if calls != 1 {
				t.Fatalf("migration runner called %d times, want 1", calls)
			}
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("startup must continue, got %v", err)
				}
				return
			}
			if !errors.Is(err, migrationErr) {
				t.Fatalf("startup must fail with the underlying migration error, got %v", err)
			}
			for _, hint := range []string{
				"database migration failed",
				"AUTO_MIGRATE=false",
				"AUTO_MIGRATE_FAIL_OPEN=true",
			} {
				if !strings.Contains(err.Error(), hint) {
					t.Fatalf("error %q must mention %q", err, hint)
				}
			}
		})
	}
}
