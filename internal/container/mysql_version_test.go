package container

import (
	"strings"
	"testing"
)

// CheckMySQLVersion enforces the MySQL 8.0.16+ hard requirement.
// CHECK constraints are enforced from 8.0.16; earlier 8.0.x silently
// ignores them. 8.4.x and 9.x are also accepted. MariaDB is rejected.
func TestCheckMySQLVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
		wantErr bool
	}{
		// Accepted versions
		{name: "8.0.16 accepted (minimum)", version: "8.0.16", wantErr: false},
		{name: "8.0.36 accepted", version: "8.0.36", wantErr: false},
		{name: "8.4.0 accepted", version: "8.4.0", wantErr: false},
		{name: "8.4.3 accepted", version: "8.4.3", wantErr: false},
		{name: "8.4 accepted (2-part for non-8.0 line)", version: "8.4", wantErr: false},
		{name: "9.0.1 accepted", version: "9.0.1", wantErr: false},
		{name: "9.1.0 accepted", version: "9.1.0", wantErr: false},
		{name: "distro build accepted", version: "8.0.36-0ubuntu0.20.04.1", wantErr: false},
		{name: "distro build 8.4 accepted", version: "8.4.0-1.el9", wantErr: false},

		// Rejected: too old
		{name: "8.0.15 rejected (before CHECK enforcement)", version: "8.0.15", wantErr: true},
		{name: "8.0.0 rejected", version: "8.0.0", wantErr: true},
		{name: "5.7.44 rejected (EOL)", version: "5.7.44", wantErr: true},
		{name: "5.6.51 rejected", version: "5.6.51", wantErr: true},

		// Rejected: incomplete version strings for the 8.0 line (need patch to verify >= 16)
		{name: "major-only '8' rejected", version: "8", wantErr: true},
		{name: "major.minor '8.0' rejected (cannot verify patch >= 16)", version: "8.0", wantErr: true},

		// Rejected: MariaDB
		{name: "10.11.8-MariaDB rejected", version: "10.11.8-MariaDB", wantErr: true},
		{name: "11.4.2-MariaDB rejected", version: "11.4.2-MariaDB", wantErr: true},

		// Rejected: garbage / empty
		{name: "empty version rejected", version: "", wantErr: true},
		{name: "garbage version rejected", version: "not-a-version", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckMySQLVersion(tt.version)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for version %q, got nil", tt.version)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error for version %q, got: %v", tt.version, err)
				}
			}
		})
	}
}

// Error messages must be actionable: mention the required version and
// the reason (CHECK enforcement from 8.0.16).
func TestCheckMySQLVersion_ErrorIsActionable(t *testing.T) {
	err := CheckMySQLVersion("8.0.15")
	if err == nil {
		t.Fatal("expected error for 8.0.15")
	}
	msg := strings.ToLower(err.Error())
	for _, want := range []string{"8.0.16", "check"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message should mention %q; got: %v", want, err)
		}
	}
}
