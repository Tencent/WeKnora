package config

import "testing"

func TestPostgresSSLMode(t *testing.T) {
	for _, mode := range []string{"", "disable", "require", "verify-ca", "verify-full", "allow", "prefer", "nossl", "REQUIRE", "require sslmode=disable"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DB_SSLMODE", mode)
			got, err := PostgresSSLMode()
			switch mode {
			case "", "disable", "require", "verify-ca", "verify-full":
				want := mode
				if want == "" {
					want = "disable"
				}
				if err != nil || got != want {
					t.Fatalf("got (%q, %v), want (%q, nil)", got, err, want)
				}
			default:
				if err == nil || got != "" {
					t.Fatalf("unsupported mode accepted: (%q, %v)", got, err)
				}
			}
		})
	}
}
