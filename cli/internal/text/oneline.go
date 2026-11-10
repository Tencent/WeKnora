package text

import "strings"

// OneLine collapses newlines/carriage-returns/tabs in s to single spaces,
// then truncates to maxDisplayWidth columns (UTF-8 safe via Truncate).
// Use for human-readable preview rows where multiline content would
// break tabular layout.
func OneLine(maxDisplayWidth int, s string) string {
	return Truncate(maxDisplayWidth, lineReplacer.Replace(s))
}

var lineReplacer = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ")
