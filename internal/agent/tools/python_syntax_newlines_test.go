package tools

import (
	"strings"
	"testing"
)

func TestPythonQuoteCheckUniversalNewlines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		for _, tc := range []struct {
			source string
			line   int
			broken bool
		}{
			{"value = \"first\\\nsecond\"\n", 0, false},
			{"value = 'first\\\nsecond'\n", 0, false},
			{"value = r\"first\\\nsecond\"\n", 0, false},
			{"value = f\"first\\\n{2}\"\n", 0, false},
			{"# a comment\nvalue = \"unterminated\n", 2, true},
			{"value = \"\"\"first\nsecond\"\"\"\nbad = \"unterminated\n", 3, true},
		} {
			t.Run(newline+tc.source, func(t *testing.T) {
				line, broken := firstBrokenPythonQuote(strings.ReplaceAll(tc.source, "\n", newline))
				if line != tc.line || broken != tc.broken {
					t.Fatalf("line ending %q: got (%d, %t), want (%d, %t)",
						newline, line, broken, tc.line, tc.broken)
				}
			})
		}
	}
}

func TestPythonQuoteCheckRejectsUnescapedPhysicalNewlines(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		source := "value = \"first" + newline + "second\""
		if line, broken := firstBrokenPythonQuote(source); !broken || line != 1 {
			t.Fatalf("expected a broken string on line 1 for %q; got (%d, %t)", newline, line, broken)
		}
	}
}
