package utils

import (
	"net"
	"os"
	"strings"
	"testing"
)

func TestSSRFSafeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		rawURL        string
		wantOK        bool
		wantReasonSub string
	}{
		{
			name:          "empty URL",
			rawURL:        "",
			wantOK:        false,
			wantReasonSub: "URL is empty",
		},
		{
			name:          "invalid scheme",
			rawURL:        "ftp://example.com/file.txt",
			wantOK:        false,
			wantReasonSub: "invalid scheme",
		},
		{
			name:          "missing hostname",
			rawURL:        "https:///api/v1/ping",
			wantOK:        false,
			wantReasonSub: "URL has no hostname",
		},
		{
			name:          "restricted hostname",
			rawURL:        "https://localhost/health",
			wantOK:        false,
			wantReasonSub: "is restricted",
		},
		{
			name:          "restricted hostname suffix",
			rawURL:        "https://service.internal/status",
			wantOK:        false,
			wantReasonSub: "hostname suffix .internal is restricted",
		},
		// --- All direct IPs are blocked by isSSRFSafeURL (strict mode) ---
		{
			name:          "direct IPv4 blocked",
			rawURL:        "https://8.8.8.8/dns-query",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
		{
			name:          "direct public IPv6 blocked",
			rawURL:        "https://[2001:4860:4860::8888]/dns-query",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
		{
			name:          "loopback IPv6 blocked",
			rawURL:        "https://[::1]/admin",
			wantOK:        false,
			wantReasonSub: "is restricted",
		},
		{
			name:          "link-local IPv6 blocked",
			rawURL:        "https://[fe80::1]/admin",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
		{
			name:          "ULA IPv6 blocked",
			rawURL:        "https://[fd12:3456:789a::1]/admin",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
		{
			name:          "IPv4-mapped IPv6 blocked",
			rawURL:        "https://[::ffff:127.0.0.1]/admin",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
		// --- IP obfuscation ---
		{
			name:          "IP-like decimal hostname blocked",
			rawURL:        "https://2130706433/",
			wantOK:        false,
			wantReasonSub: "IP-like hostname format is not allowed",
		},
		{
			name:          "IP-like octal hostname blocked",
			rawURL:        "https://0177.0.0.1/",
			wantOK:        false,
			wantReasonSub: "IP-like hostname format is not allowed",
		},
		// --- Port blocking ---
		{
			name:          "blocked internal service port",
			rawURL:        "https://example.com:3306/db",
			wantOK:        false,
			wantReasonSub: "port 3306 is blocked for security reasons",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ok, reason := isSSRFSafeURL(tt.rawURL)
			if ok != tt.wantOK {
				t.Fatalf("isSSRFSafeURL(%q) ok = %v, want %v, reason = %q", tt.rawURL, ok, tt.wantOK, reason)
			}
			if tt.wantReasonSub != "" && !strings.Contains(reason, tt.wantReasonSub) {
				t.Fatalf("isSSRFSafeURL(%q) reason = %q, want contains %q", tt.rawURL, reason, tt.wantReasonSub)
			}
		})
	}
}

func TestSSRFSafeURL_AllowPublicDomain(t *testing.T) {
	t.Parallel()

	ok, reason := isSSRFSafeURL("https://example.com/path")
	if !ok {
		// This path depends on runtime DNS/network. If DNS is unavailable, skip to keep CI stable.
		if strings.Contains(reason, "DNS resolution failed") {
			t.Skipf("skip due to DNS unavailable in test environment: %s", reason)
		}
		t.Fatalf("expected public domain to be allowed, got ok=%v reason=%q", ok, reason)
	}
}

// TestValidateURLForSSRF_IPv6Whitelist verifies that whitelisted IPv6 addresses
// bypass the strict IP block in isSSRFSafeURL.
func TestValidateURLForSSRF_IPv6Whitelist(t *testing.T) {
	tests := []struct {
		name      string
		whitelist string
		rawURL    string
		wantErr   bool
	}{
		{
			name:      "exact IPv6 whitelisted",
			whitelist: "2001:4860:4860::8888",
			rawURL:    "https://[2001:4860:4860::8888]/dns-query",
			wantErr:   false,
		},
		{
			name:      "IPv6 CIDR whitelisted",
			whitelist: "2001:db8::/32",
			rawURL:    "https://[2001:db8::1]/page",
			wantErr:   false,
		},
		{
			name:      "IPv6 not in whitelist still blocked",
			whitelist: "2001:db8::/32",
			rawURL:    "https://[2001:4860:4860::8888]/dns-query",
			wantErr:   true,
		},
		{
			name:      "IPv4 whitelisted",
			whitelist: "8.8.8.8",
			rawURL:    "https://8.8.8.8/dns-query",
			wantErr:   false,
		},
		{
			name:      "wildcard domain whitelisted",
			whitelist: "*.example.com",
			rawURL:    "https://api.example.com/v1",
			wantErr:   false,
		},
		{
			name:      "wildcard domain root whitelisted",
			whitelist: "*.example.com",
			rawURL:    "https://example.com/v1",
			wantErr:   false,
		},
		{
			name:      "bare host without scheme normalised",
			whitelist: "internal.service",
			rawURL:    "internal.service:8080/path",
			wantErr:   false,
		},
		{
			name:      "empty whitelist blocks direct IP",
			whitelist: "",
			rawURL:    "https://8.8.8.8/dns-query",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset whitelist singleton so we can set a new value
			resetSSRFWhitelistForTest()
			os.Setenv("SSRF_WHITELIST", tt.whitelist)
			defer func() {
				os.Unsetenv("SSRF_WHITELIST")
				resetSSRFWhitelistForTest()
			}()

			err := ValidateURLForSSRF(tt.rawURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateURLForSSRF(%q) with whitelist=%q: err = %v, wantErr = %v",
					tt.rawURL, tt.whitelist, err, tt.wantErr)
			}
		})
	}
}

// TestSSRFWhitelistExtraMerge verifies that SSRF_WHITELIST_EXTRA is merged
// into the effective whitelist alongside SSRF_WHITELIST, so deployment-managed
// defaults (e.g. docker-compose injected sidecar host names) survive when an
// operator overrides SSRF_WHITELIST in their .env.
func TestSSRFWhitelistExtraMerge(t *testing.T) {
	cases := []struct {
		name    string
		main    string
		extra   string
		host    string
		want    bool
	}{
		{name: "extra only", main: "", extra: "searxng", host: "searxng", want: true},
		{name: "main only does not match extra host", main: "internal", extra: "", host: "searxng", want: false},
		{name: "both merged - main host", main: "internal", extra: "searxng", host: "internal", want: true},
		{name: "both merged - extra host", main: "internal", extra: "searxng", host: "searxng", want: true},
		{name: "neither matches unrelated host", main: "internal", extra: "searxng", host: "evil.example", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetSSRFWhitelistForTest()
			os.Setenv("SSRF_WHITELIST", tc.main)
			os.Setenv("SSRF_WHITELIST_EXTRA", tc.extra)
			defer func() {
				os.Unsetenv("SSRF_WHITELIST")
				os.Unsetenv("SSRF_WHITELIST_EXTRA")
				resetSSRFWhitelistForTest()
			}()
			if got := IsSSRFWhitelisted(tc.host); got != tc.want {
				t.Fatalf("IsSSRFWhitelisted(%q) main=%q extra=%q = %v, want %v",
					tc.host, tc.main, tc.extra, got, tc.want)
			}
		})
	}
}

// TestIsRestrictedIP_IPv6 tests IPv6-specific restricted range detection.
func TestIsRestrictedIP_IPv6(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		ip         string
		wantBlock  bool
		wantReason string
	}{
		{"loopback", "::1", true, "loopback"},
		{"unspecified", "::", true, "unspecified"},
		{"link-local", "fe80::1", true, "link-local"},
		{"ULA", "fd12:3456:789a::1", true, ""},
		{"site-local", "fec0::1", true, "site-local"},
		{"Teredo", "2001:0000:4136:e378:8000:63bf:3fff:fdd2", true, "Teredo"},
		{"6to4 private", "2002:c0a8:0101::1", true, "6to4"},
		{"6to4 public", "2002:0808:0808::1", false, ""},
		{"public IPv6", "2001:4860:4860::8888", false, ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ip := parseIPForTest(t, tt.ip)
			blocked, reason := isRestrictedIP(ip)
			if blocked != tt.wantBlock {
				t.Fatalf("isRestrictedIP(%s) = %v, want %v (reason: %s)", tt.ip, blocked, tt.wantBlock, reason)
			}
			if tt.wantReason != "" && !strings.Contains(strings.ToLower(reason), strings.ToLower(tt.wantReason)) {
				t.Fatalf("isRestrictedIP(%s) reason = %q, want contains %q", tt.ip, reason, tt.wantReason)
			}
		})
	}
}

func parseIPForTest(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("invalid test IP: %s", s)
	}
	return ip
}
