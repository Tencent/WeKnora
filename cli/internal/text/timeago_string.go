package text

import "time"

// FuzzyAgoStr is the string-input variant of FuzzyAgo for SDK types that
// carry timestamps as RFC3339 strings (sdk.Chunk.UpdatedAt, sdk.Session.
// UpdatedAt, ...) rather than time.Time.
//
// Behavior:
//   - ts == ""        → "-"   (server has no timestamp; render placeholder)
//   - parse error     → ts    (surface the raw value rather than hide it)
//   - parse OK        → FuzzyAgo(now, parsed)
//
// The parse-error fallback is deliberate: if the server starts emitting a
// new format we want it visible in the table, not silently replaced by "-".
func FuzzyAgoStr(now time.Time, ts string) string {
	if ts == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return FuzzyAgo(now, t)
}
