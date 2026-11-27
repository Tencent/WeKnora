package searchutil

import (
	"strings"
	"testing"
)

func tokenSet(tokens ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		set[tok] = struct{}{}
	}
	return set
}

func TestJaccard(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]struct{}
		want float64
	}{
		{"both empty", tokenSet(), tokenSet(), 0},
		{"one empty", tokenSet("a"), tokenSet(), 0},
		{"identical", tokenSet("a", "b"), tokenSet("a", "b"), 1},
		{"disjoint", tokenSet("a", "b"), tokenSet("c", "d"), 0},
		{"partial overlap", tokenSet("a", "b"), tokenSet("b", "c"), 1.0 / 3},
		{"subset", tokenSet("a"), tokenSet("a", "b"), 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Jaccard(tc.a, tc.b); got != tc.want {
				t.Errorf("Jaccard = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTokenOverlapRatio(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]struct{}
		want float64
	}{
		{"either empty", tokenSet("a"), tokenSet(), 0},
		{"both empty", tokenSet(), tokenSet(), 0},
		{"smaller contained", tokenSet("a", "b"), tokenSet("a", "b", "c"), 1},
		{"partial overlap", tokenSet("a", "b", "c"), tokenSet("b", "c", "d", "e"), 2.0 / 3},
		{"disjoint", tokenSet("a"), tokenSet("b"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TokenOverlapRatio(tc.a, tc.b); got != tc.want {
				t.Errorf("TokenOverlapRatio = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClampFloat(t *testing.T) {
	cases := []struct {
		name        string
		v, min, max float64
		want        float64
	}{
		{"below min", -1, 0, 1, 0},
		{"above max", 2, 0, 1, 1},
		{"at min", 0, 0, 1, 0},
		{"at max", 1, 0, 1, 1},
		{"inside", 0.5, 0, 1, 0.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClampFloat(tc.v, tc.min, tc.max); got != tc.want {
				t.Errorf("ClampFloat(%v, %v, %v) = %v, want %v", tc.v, tc.min, tc.max, got, tc.want)
			}
		})
	}
}

func TestBuildContentSignature(t *testing.T) {
	if got := BuildContentSignature(""); got != "" {
		t.Errorf("empty content should give an empty signature, got %q", got)
	}
	if got := BuildContentSignature("   \n\t "); got != "" {
		t.Errorf("whitespace-only content should give an empty signature, got %q", got)
	}
	a := BuildContentSignature("Hello   World")
	b := BuildContentSignature("hello world")
	if a != b {
		t.Errorf("signature should ignore case and collapse whitespace: %q vs %q", a, b)
	}
	if len(a) != 32 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Errorf("signature should be a 32-char hex MD5, got %q", a)
	}
	if c := BuildContentSignature("hello world!"); c == a {
		t.Errorf("different content must give a different signature, both %q", a)
	}
	if d := BuildContentSignature("Hello   World"); d != a {
		t.Errorf("signature must be deterministic: %q vs %q", a, d)
	}
}

func TestNormalizeContent(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"  Hello   WORLD  ", "hello world"},
		{"already\nnormalized\ttext", "already normalized text"},
	}
	for _, tc := range cases {
		if got := NormalizeContent(tc.in); got != tc.want {
			t.Errorf("NormalizeContent(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsContentContained(t *testing.T) {
	cases := []struct {
		name        string
		short, long string
		want        bool
	}{
		{"contained", "hello", "say hello world", true},
		{"equal", "hello", "hello", true},
		{"not contained", "bye", "say hello world", false},
		{"empty short", "", "say hello world", false},
		{"empty long", "hello", "", false},
		{"short longer than long", "say hello world", "hello", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsContentContained(tc.short, tc.long); got != tc.want {
				t.Errorf("IsContentContained(%q, %q) = %v, want %v", tc.short, tc.long, got, tc.want)
			}
		})
	}
}
