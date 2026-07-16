package cmdutil

import (
	"reflect"
	"testing"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"list", "list", 0},
		{"list", "listt", 1},
		{"list", "lst", 1},
		{"limit", "limti", 2},
		{"café", "cafe", 1}, // rune-aware: é vs e is one edit
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSuggestClosest(t *testing.T) {
	subs := []string{"create", "delete", "edit", "list", "pin", "status", "unpin", "view"}
	cases := []struct {
		target string
		want   []string
	}{
		{"lst", []string{"list"}},                 // 1 edit
		{"listt", []string{"list"}},               // 1 edit
		{"vieww", []string{"view"}},               // 1 edit
		{"creat", []string{"create"}},             // 1 edit
		{"xyzzy", nil},                            // nothing close
		{"", nil},                                 // empty
		{"pinn", []string{"pin"}},                 // 1 edit (pin), unpin is 2
	}
	for _, tc := range cases {
		got := SuggestClosest(tc.target, subs)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("SuggestClosest(%q) = %v, want %v", tc.target, got, tc.want)
		}
	}
}

// TestSuggestClosest_RanksByDistance pins closest-first ordering when
// candidates are at different distances.
func TestSuggestClosest_RanksByDistance(t *testing.T) {
	// "creat" → "create" is 1 edit (append e); "delete" is far. create first.
	got := SuggestClosest("creat", []string{"delete", "create"})
	if len(got) == 0 || got[0] != "create" {
		t.Errorf("closest to 'creat' should be 'create' first; got %v", got)
	}
}
