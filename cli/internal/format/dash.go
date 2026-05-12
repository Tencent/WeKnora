package format

// DashIfEmpty returns "-" for empty strings, otherwise s itself. Standard
// rendering convention for human-readable table cells whose value is
// optional (e.g. user email in `auth list` / `context list`).
func DashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
