package runtime

import (
	"strings"
	"testing"
)

func TestCheckAppSearxngHostPortCollision_DistinctDefaultsOK(t *testing.T) {
	if err := CheckAppSearxngHostPortCollision("", ""); err != nil {
		t.Fatalf("empty env should resolve to %s vs %s: %v", DefaultAppHostPort, DefaultSearxngHostPort, err)
	}
	if err := CheckAppSearxngHostPortCollision(DefaultAppHostPort, DefaultSearxngHostPort); err != nil {
		t.Fatalf("explicit defaults %s vs %s should be ok: %v", DefaultAppHostPort, DefaultSearxngHostPort, err)
	}
	if err := CheckAppSearxngHostPortCollision(" 8080 ", "8888"); err != nil {
		t.Fatalf("trimmed distinct ports should be ok: %v", err)
	}
}

func TestCheckAppSearxngHostPortCollision_EqualPortsError(t *testing.T) {
	cases := []struct {
		name         string
		app, searxng string
	}{
		{name: "both 8080", app: "8080", searxng: "8080"},
		{name: "both 8888", app: "8888", searxng: "8888"},
		{name: "SEARXNG_PORT equals default APP_PORT", app: "", searxng: DefaultAppHostPort},
		{name: "APP_PORT equals default SEARXNG_PORT", app: DefaultSearxngHostPort, searxng: ""},
		{name: "whitespace-equal 8080", app: " 8080 ", searxng: "8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckAppSearxngHostPortCollision(tc.app, tc.searxng)
			if err == nil {
				t.Fatalf("CheckAppSearxngHostPortCollision(%q, %q) = nil, want error", tc.app, tc.searxng)
			}
			if want := "collides"; !strings.Contains(err.Error(), want) {
				t.Fatalf("error %q does not mention %q", err.Error(), want)
			}
		})
	}
}

func TestCheckAppSearxngHostPortsFromEnv(t *testing.T) {
	t.Setenv("APP_PORT", "")
	t.Setenv("SEARXNG_PORT", "")
	if err := CheckAppSearxngHostPortsFromEnv(); err != nil {
		t.Fatalf("unset env should use distinct defaults: %v", err)
	}

	t.Setenv("APP_PORT", "8080")
	t.Setenv("SEARXNG_PORT", "8080")
	if err := CheckAppSearxngHostPortsFromEnv(); err == nil {
		t.Fatal("equal APP_PORT and SEARXNG_PORT should error")
	}
}
