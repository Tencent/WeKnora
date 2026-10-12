package utils

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
)

// whitelistOnly configures the whitelist and the whitelist-only switch for
// one test and restores the cached whitelist afterwards.
func whitelistOnly(t *testing.T, whitelist, only string) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", whitelist)
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	t.Setenv("SSRF_DNS_WHITELIST_ONLY", only)
	ResetSSRFWhitelistForTest()
	t.Cleanup(ResetSSRFWhitelistForTest)
}

func TestCheckSSRFWhitelistOnly(t *testing.T) {
	const whitelist = "api.internal,*.corp.example,10.0.0.0/8,203.0.113.5"
	cases := []struct {
		name    string
		only    string
		host    string
		refused bool
	}{
		{"off: any domain passes", "false", "outside.invalid", false},
		{"off: any IP passes", "false", "198.51.100.7", false},
		{"unset counts as off", "", "outside.invalid", false},
		{"on: exact name", "true", "api.internal", false},
		{"on: exact name, any case", "true", "API.Internal", false},
		{"on: wildcard suffix", "true", "svc.corp.example", false},
		{"on: wildcard root", "true", "corp.example", false},
		{"on: IP in CIDR", "true", "10.20.30.40", false},
		{"on: exact IP", "true", "203.0.113.5", false},
		{"on: other domain refused", "true", "outside.invalid", true},
		{"on: other IP refused", "true", "198.51.100.7", true},
		{"on: loopback refused unless listed", "true", "127.0.0.1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			whitelistOnly(t, whitelist, tc.only)
			err := CheckSSRFWhitelistOnly(tc.host)
			if tc.refused {
				if !errors.Is(err, ErrSSRFHostNotWhitelisted) {
					t.Fatalf("expected ErrSSRFHostNotWhitelisted, got %v", err)
				}
				if !strings.Contains(err.Error(), tc.host) {
					t.Fatalf("error should name the host: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}

// The switch is a security control, so a value that is neither true nor false
// must not silently leave it off.
func TestSSRFWhitelistOnlyEnabledParsesLikeTheRestOfTheRepo(t *testing.T) {
	cases := map[string]bool{
		"":        false,
		"false":   false,
		"FALSE":   false,
		"0":       false,
		"f":       false,
		"true":    true,
		"TRUE":    true,
		"True":    true,
		"1":       true,
		"t":       true,
		" true ":  true,
		"yes":     true, // unparsable: fail closed rather than silently off
		"on":      true,
		"enabled": true,
	}
	for raw, want := range cases {
		t.Run("value="+raw, func(t *testing.T) {
			whitelistOnly(t, "api.internal", raw)
			if got := SSRFWhitelistOnlyEnabled(); got != want {
				t.Fatalf("SSRF_DNS_WHITELIST_ONLY=%q: enabled=%v, want %v", raw, got, want)
			}
		})
	}
}

// A dialer sees an address, not always a name: gRPC resolves its own target
// before calling the dialer it was given, so a whitelisted service name
// arrives as an IP. Judging names is the dialer's job; judging IP literals is
// the URL layer's.
func TestCheckDialWhitelistOnlyJudgesNamesOnly(t *testing.T) {
	whitelistOnly(t, "api.internal", "true")

	if err := CheckDialWhitelistOnly("outside.invalid"); !errors.Is(err, ErrSSRFHostNotWhitelisted) {
		t.Fatalf("a non-whitelisted name must be refused, got %v", err)
	}
	if err := CheckDialWhitelistOnly("api.internal"); err != nil {
		t.Fatalf("a whitelisted name must pass, got %v", err)
	}
	for _, ip := range []string{"198.51.100.7", "10.20.30.40", "2001:db8::1"} {
		if err := CheckDialWhitelistOnly(ip); err != nil {
			t.Fatalf("%s: an address must be left to the address checks, got %v", ip, err)
		}
	}
}

// With the mode on the refusal comes from the whitelist, before the name is
// ever resolved; with it off the host takes the ordinary path.
func TestValidateURLForSSRF_WhitelistOnlyRefusesBeforeDNS(t *testing.T) {
	whitelistOnly(t, "api.internal", "true")
	err := ValidateURLForSSRF("https://outside.invalid/v1")
	if !errors.Is(err, ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected the whitelist refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "DNS resolution failed") {
		t.Fatalf("the refusal must come before DNS: %v", err)
	}
	if err := ValidateURLForSSRF("https://api.internal/v1"); err != nil {
		t.Fatalf("a whitelisted host must pass: %v", err)
	}

	whitelistOnly(t, "api.internal", "false")
	if err := ValidateURLForSSRF("https://outside.invalid/v1"); errors.Is(err, ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off nothing is refused for being off-whitelist, got %v", err)
	}
}

func TestSSRFSafeDialContext_WhitelistOnlyRefusesNamesBeforeDNS(t *testing.T) {
	whitelistOnly(t, "api.internal", "true")
	_, err := SSRFSafeDialContext(context.Background(), "tcp", "outside.invalid:443")
	if !errors.Is(err, ErrSSRFHostNotWhitelisted) {
		t.Fatalf("expected the whitelist refusal, got %v", err)
	}
	// An address reaches the restricted-IP checks instead of this one.
	_, err = SSRFSafeDialContext(context.Background(), "tcp", "198.51.100.7:443")
	if errors.Is(err, ErrSSRFHostNotWhitelisted) {
		t.Fatalf("an address must be left to the address checks, got %v", err)
	}

	whitelistOnly(t, "api.internal", "false")
	_, err = SSRFSafeDialContext(context.Background(), "tcp", "outside.invalid:443")
	if errors.Is(err, ErrSSRFHostNotWhitelisted) {
		t.Fatalf("with the mode off the name is resolved as before, got %v", err)
	}
}

// With CIDR entries the whitelist resolves a hostname to see whether its
// addresses fall inside; whitelist-only mode matches by name only, because
// that lookup is the query the mode exists to prevent.
func TestIsSSRFWhitelisted_WhitelistOnlyMatchesByNameOnly(t *testing.T) {
	ips, err := net.LookupIP("localhost")
	loopback := false
	for _, ip := range ips {
		if ip.IsLoopback() {
			loopback = true
		}
	}
	if err != nil || !loopback {
		t.Skip("localhost does not resolve to a loopback address here; the CIDR fallback cannot be exercised")
	}

	whitelistOnly(t, "127.0.0.0/8,::1/128,api.internal", "false")
	if !IsSSRFWhitelisted("localhost") {
		t.Fatal("with the mode off a hostname is matched by what it resolves to")
	}

	whitelistOnly(t, "127.0.0.0/8,::1/128,api.internal", "true")
	if IsSSRFWhitelisted("localhost") {
		t.Fatal("whitelist-only mode must match by name, never by resolved address")
	}
	if !IsSSRFWhitelisted("api.internal") || !IsSSRFWhitelisted("127.0.0.1") {
		t.Fatal("name and IP-literal CIDR entries must still match")
	}
}
