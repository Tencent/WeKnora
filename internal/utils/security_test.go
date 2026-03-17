package utils

import (
	"strings"
	"testing"
)

func TestIsSSRFSafeURL(t *testing.T) {
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
		{
			name:          "direct IPv4 blocked",
			rawURL:        "https://8.8.8.8/dns-query",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
		{
			name:          "direct IPv6 blocked",
			rawURL:        "https://[2001:4860:4860::8888]/dns-query",
			wantOK:        false,
			wantReasonSub: "direct IP address access is not allowed",
		},
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

			ok, reason := IsSSRFSafeURL(tt.rawURL)
			if ok != tt.wantOK {
				t.Fatalf("IsSSRFSafeURL(%q) ok = %v, want %v, reason = %q", tt.rawURL, ok, tt.wantOK, reason)
			}
			if tt.wantReasonSub != "" && !strings.Contains(reason, tt.wantReasonSub) {
				t.Fatalf("IsSSRFSafeURL(%q) reason = %q, want contains %q", tt.rawURL, reason, tt.wantReasonSub)
			}
		})
	}
}

func TestIsSSRFSafeURL_AllowPublicDomain(t *testing.T) {
	t.Parallel()

	ok, reason := IsSSRFSafeURL("https://example.com/path")
	if !ok {
		// This path depends on runtime DNS/network. If DNS is unavailable, skip to keep CI stable.
		if strings.Contains(reason, "DNS resolution failed") {
			t.Skipf("skip due to DNS unavailable in test environment: %s", reason)
		}
		t.Fatalf("expected public domain to be allowed, got ok=%v reason=%q", ok, reason)
	}
}
