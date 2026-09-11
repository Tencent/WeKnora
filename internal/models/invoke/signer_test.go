package invoke

import (
	"strings"
	"testing"
)

// The nonce is part of the WeKnoraCloud signature input (protocol surface), so
// its shape is pinned: fixed length, alnum charset, and fresh draws (审查轮 T-3;
// generateNonce has no direct v1 test — this guards charset/length regressions).
func TestGenerateNonce(t *testing.T) {
	n := generateNonce(nonceLength)
	if len(n) != nonceLength {
		t.Fatalf("nonce length = %d, want %d", len(n), nonceLength)
	}
	for _, r := range n {
		if !strings.ContainsRune(nonceChars, r) {
			t.Fatalf("nonce %q contains %q outside the charset", n, r)
		}
	}

	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		seen[generateNonce(nonceLength)] = true
	}
	if len(seen) != 100 {
		t.Fatalf("expected 100 distinct nonces, got %d (charset uniformity broken?)", len(seen))
	}
}
