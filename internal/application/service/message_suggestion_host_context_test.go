package service

import "testing"

// Reproduction for https://github.com/Tencent/WeKnora/issues/3820:
// on embed channels with setContext enabled, the frontend prepends a
// "[Host context]" block to every outbound query (see
// frontend/src/utils/embedContext.ts). ValidateAttribution must still
// recognize a suggestion click behind that prefix.
func TestStripHostContextPrefixIssue3820(t *testing.T) {
	prefixed := "[Host context]\nuserId: 123\npage: /some-page\n\nwhat is the refund policy"
	if got := stripHostContextPrefix(prefixed); got != "what is the refund policy" {
		t.Fatalf("prefixed query was not stripped, got %q", got)
	}
}

func TestStripHostContextPrefix(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"no prefix passthrough", "what is the refund policy", "what is the refund policy"},
		{"empty", "", ""},
		{"header only, no blank line", "[Host context]\nuserId: 123", "[Host context]\nuserId: 123"},
		{"multi-line context", "[Host context]\nuserId: 123\npage: /x\na: b\n\nreal query?", "real query?"},
		{"query containing blank lines preserved", "[Host context]\nuserId: 1\n\nline1\n\nline2", "line1\n\nline2"},
		{"not a prefix", "prefix [Host context]\n\nquery", "prefix [Host context]\n\nquery"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripHostContextPrefix(tc.query); got != tc.want {
				t.Fatalf("stripHostContextPrefix(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
