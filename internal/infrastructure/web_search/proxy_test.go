package web_search

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestValidateProxyURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"no scheme private IP bare host:port", "192.168.0.1:3128", true},
		{"localhost http", "http://localhost:3128", true},
		{"direct IPv4 http", "http://192.168.1.1:3128", true},
		{"socks5 to IP blocked", "socks5://10.0.0.1:1080", true},
		{"bad scheme", "ftp://proxy.example:21", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProxyURL(tt.raw)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewSearchHTTPClient_RejectsUnsafeProxy(t *testing.T) {
	_, err := NewSearchHTTPClient(0, "http://127.0.0.1:3128")
	if err == nil {
		t.Fatal("expected error for disallowed proxy URL (127.0.0.1)")
	}
	if !strings.Contains(err.Error(), "SSRF") {
		t.Fatalf("expected SSRF-related error, got: %v", err)
	}
}

func TestNewSearchHTTPClient_AcceptsEmptyProxy(t *testing.T) {
	c, err := NewSearchHTTPClient(0, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport == nil {
		t.Fatal("expected transport")
	}
	if c.CheckRedirect == nil {
		t.Fatal("expected CheckRedirect for SSRF-safe redirects")
	}
}

func TestSsrfSafeRedirect_BlocksNonHTTPSScheme(t *testing.T) {
	h := ssrfSafeRedirect(10)
	req := &http.Request{URL: mustParse(t, "ftp://evil.com/")}
	// len(via)==0 so first redirect
	err := h(req, nil)
	if err == nil {
		t.Fatal("expected error for ftp redirect")
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
