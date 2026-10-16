package utils

import "testing"

func TestStripHostContextEnvelope(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "text without an envelope is returned unchanged",
			query: "who am i",
			want:  "who am i",
		},
		{
			name:  "empty query stays empty",
			query: "",
			want:  "",
		},
		{
			name:  "a single-field envelope is dropped",
			query: "[Host context]\nuserId: 21\n\nwho am i",
			want:  "who am i",
		},
		{
			name:  "a multi-field envelope is dropped",
			query: "[Host context]\nuserId: 21\npage: /ai-agent\n\nwho am i",
			want:  "who am i",
		},
		{
			// Context values may carry newlines; only the blank line ends
			// the envelope, so the visitor's question starts after it.
			name:  "values containing single newlines keep the question intact",
			query: "[Host context]\nnote: line1\nline2\n\nask",
			want:  "ask",
		},
		{
			name:  "a multi-line question is preserved",
			query: "[Host context]\npage: /pricing\n\nfirst\n\nsecond",
			want:  "first\n\nsecond",
		},
		{
			name:  "the marker only counts at the start of the message",
			query: "quote: [Host context]\n\nx",
			want:  "quote: [Host context]\n\nx",
		},
		{
			name:  "a marker that never closes leaves the query alone",
			query: "[Host context]\nuserId: 21",
			want:  "[Host context]\nuserId: 21",
		},
		{
			name:  "an envelope with an empty question yields empty text",
			query: "[Host context]\nuserId: 21\n\n",
			want:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripHostContextEnvelope(tc.query); got != tc.want {
				t.Fatalf("StripHostContextEnvelope(%q) = %q, want %q", tc.query, got, tc.want)
			}
		})
	}
}
