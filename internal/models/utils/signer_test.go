package utils

import (
	"crypto/md5"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestRfc3986Encode(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"aZ0-._~", "aZ0-._~"},
		{"/", "%2F"},
		{" ", "%20"},
		{"a+b=c&d", "a%2Bb%3Dc%26d"},
		{"hello world", "hello%20world"},
		// Non-ASCII must be encoded per UTF-8 byte, not per code point:
		// U+4E2D is bytes E4 B8 AD, U+00E9 is bytes C3 A9.
		{"中", "%E4%B8%AD"},
		{"é", "%C3%A9"},
		{"中文测试", "%E4%B8%AD%E6%96%87%E6%B5%8B%E8%AF%95"},
		{"a中b", "a%E4%B8%ADb"},
	}
	for _, tt := range tests {
		if got := rfc3986Encode(tt.in); got != tt.want {
			t.Errorf("rfc3986Encode(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMd5Hex(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
		{"hello", "5d41402abc4b2a76b9719d911017c592"},
		{"{}", "99914b932bd37a50b983c5e7c90ae93b"},
	}
	for _, tt := range tests {
		if got := md5Hex(tt.in); got != tt.want {
			t.Errorf("md5Hex(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGenerateNonce(t *testing.T) {
	// Only shape is asserted: the value itself is random by design.
	n := generateNonce(nonceLength)
	if len(n) != nonceLength {
		t.Fatalf("generateNonce(%d) has length %d", nonceLength, len(n))
	}
	for _, c := range n {
		if !strings.ContainsRune(nonceChars, c) {
			t.Fatalf("generateNonce returned %q, which contains %q outside the nonce alphabet", n, c)
		}
	}
}

// Sign is deterministic given its inputs except for the timestamp and the
// nonce, which are generated inside. Recompute the signature from the
// returned headers to prove the header set is complete and self-consistent.
func TestSignHeadersAreCompleteAndSelfConsistent(t *testing.T) {
	for _, body := range []string{"{}", "", `{"q":1}`} {
		headers := Sign("app1", "key1", "req-1", body)

		for _, k := range []string{"X-APPID", "X-API-Key", "X-Request-ID", "X-Timestamp", "X-Nonce", "X-Signature"} {
			if headers[k] == "" {
				t.Fatalf("Sign(%q) is missing header %s: %v", body, k, headers)
			}
		}
		if headers["X-APPID"] != "app1" || headers["X-API-Key"] != "key1" || headers["X-Request-ID"] != "req-1" {
			t.Fatalf("Sign(%q) echoed wrong identity headers: %v", body, headers)
		}

		bodyForHash := body
		if bodyForHash == "" {
			bodyForHash = "{}"
		}
		params := map[string]string{
			"x-appid":      "app1",
			"x-api-key":    "key1",
			"x-request-id": "req-1",
			"x-timestamp":  headers["X-Timestamp"],
			"x-nonce":      headers["X-Nonce"],
			"body":         md5Hex(bodyForHash),
		}
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, rfc3986Encode(k)+"="+rfc3986Encode(params[k]))
		}
		want := fmt.Sprintf("%x", md5.Sum([]byte(strings.Join(parts, "&"))))
		if headers["X-Signature"] != want {
			t.Errorf("Sign(%q) signature = %q, recomputed %q", body, headers["X-Signature"], want)
		}
	}
}
