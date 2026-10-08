package mcpserver

import "testing"

func TestGrepPatternAndTermsKeepsRegexSemantics(t *testing.T) {
	cases := []struct {
		query   string
		terms   string
		matches []string
		rejects []string
	}{
		{
			query: "stardust|skyvault", terms: "stardust skyvault",
			matches: []string{"the Stardust drive", "SKYVAULT"}, rejects: []string{"star dust"},
		},
		{
			query: `\bE1001\b`, terms: "E1001",
			matches: []string{"code E1001 raised"}, rejects: []string{"E10012"},
		},
		{
			query: "psionic.*engine", terms: "psionic engine",
			matches: []string{"psionic warp engine"}, rejects: []string{"engine psionic"},
		},
		{
			// Not a valid expression: matched literally, as it used to fail.
			query: "C++", terms: "C",
			matches: []string{"written in C++"}, rejects: []string{"written in C"},
		},
		{query: "图片素材", terms: "图片素材", matches: []string{"关于图片素材的说明"}},
	}
	for _, tc := range cases {
		re, terms := grepPatternAndTerms(tc.query)
		if terms != tc.terms {
			t.Errorf("%q terms = %q, want %q", tc.query, terms, tc.terms)
		}
		for _, s := range tc.matches {
			if !re.MatchString(s) {
				t.Errorf("%q should match %q", tc.query, s)
			}
		}
		for _, s := range tc.rejects {
			if re.MatchString(s) {
				t.Errorf("%q should not match %q", tc.query, s)
			}
		}
	}
	if _, terms := grepPatternAndTerms(`^\d+$`); terms != "" {
		t.Fatalf("a pattern without literal text has no index terms, got %q", terms)
	}
}
