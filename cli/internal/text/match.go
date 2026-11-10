package text

import "strings"

// ContainsFold reports whether any of fields contains needle, comparing
// case-insensitively. Avoids the inline `strings.Contains(strings.ToLower
// (field), needle)` triple-pattern when callers want to OR-match across
// several columns of the same record. The caller passes the needle in
// lowercase form so we don't lowercase the same string per call site.
func ContainsFold(lowerNeedle string, fields ...string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), lowerNeedle) {
			return true
		}
	}
	return false
}
