package runtime

import "testing"

func TestCheckAppSearxngHostPortCollision_SkipWhenSearxngUnset(t *testing.T) {
	// Standalone / independently deployed SearXNG: no SEARXNG_PORT → no check.
	if err := CheckAppSearxngHostPortCollision("", ""); err != nil {
		t.Fatalf("empty SEARXNG_PORT should skip: %v", err)
	}
	// APP_PORT=8888 with SearXNG disabled must not fatal (regression from #3387).
	if err := CheckAppSearxngHostPortCollision(DefaultSearxngHostPort, ""); err != nil {
		t.Fatalf("APP_PORT=%s with empty SEARXNG_PORT should skip: %v", DefaultSearxngHostPort, err)
	}
	if err := CheckAppSearxngHostPortCollision("8888", "  "); err != nil {
		t.Fatalf("whitespace-only SEARXNG_PORT should skip: %v", err)
	}
}

func TestCheckAppSearxngHostPortCollision_DistinctWhenEnabledOK(t *testing.T) {
	if err := CheckAppSearxngHostPortCollision("", DefaultSearxngHostPort); err != nil {
		t.Fatalf("default APP_PORT vs explicit SEARXNG_PORT %s: %v", DefaultSearxngHostPort, err)
	}
	if err := CheckAppSearxngHostPortCollision(DefaultAppHostPort, DefaultSearxngHostPort); err != nil {
		t.Fatalf("explicit %s vs %s should be ok: %v", DefaultAppHostPort, DefaultSearxngHostPort, err)
	}
	if err := CheckAppSearxngHostPortCollision(" 8080 ", "8888"); err != nil {
		t.Fatalf("trimmed distinct ports: %v", err)
	}
}

func TestCheckAppSearxngHostPortCollision_equalPortsErrorWhenEnabled(t *testing.T) {
	cases := []struct {
		name, app, searxng string
	}{
		{name: "both 8080", app: "8080", searxng: "8080"},
		{name: "both 8888", app: "8888", searxng: "8888"},
		{name: "SEARXNG_PORT equals default APP_PORT", app: "", searxng: DefaultAppHostPort},
		{name: "whitespace-equal 8080", app: " 8080 ", searxng: "8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckAppSearxngHostPortCollision(tc.app, tc.searxng)
			if err == nil {
				t.Fatalf("CheckAppSearxngHostPortCollision(%q, %q) = nil, want error", tc.app, tc.searxng)
			}
		})
	}
}

func TestCheckAppSearxngHostPortsFromEnv(t *testing.T) {
	t.Setenv("APP_PORT", "")
	t.Setenv("SEARXNG_PORT", "")
	if err := CheckAppSearxngHostPortsFromEnv(); err != nil {
		t.Fatalf("unset SEARXNG_PORT should skip: %v", err)
	}

	// Regression: standalone APP_PORT=8888, SearXNG not enabled.
	t.Setenv("APP_PORT", "8888")
	t.Setenv("SEARXNG_PORT", "")
	if err := CheckAppSearxngHostPortsFromEnv(); err != nil {
		t.Fatalf("APP_PORT=8888 without SEARXNG_PORT must not error: %v", err)
	}

	// Local SearXNG enabled with colliding explicit port.
	t.Setenv("APP_PORT", "8080")
	t.Setenv("SEARXNG_PORT", "8080")
	if err := CheckAppSearxngHostPortsFromEnv(); err == nil {
		t.Fatal("equal APP_PORT and SEARXNG_PORT should error when SearXNG enabled")
	}

	// Local SearXNG enabled, distinct ports OK.
	t.Setenv("SEARXNG_PORT", "8888")
	if err := CheckAppSearxngHostPortsFromEnv(); err != nil {
		t.Fatalf("8080 vs 8888 should be ok: %v", err)
	}
}
