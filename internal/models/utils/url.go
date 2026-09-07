package utils

import "strings"

// AppendPathOnce joins base and suffix without double-appending when base
// already ends with the suffix (case-insensitive). Single home for the
// endpoint guards in chat, vlm and provider packages.
func AppendPathOnce(base, suffix string) string {
	trimmed := strings.TrimRight(base, "/")
	if strings.HasSuffix(strings.ToLower(trimmed), strings.ToLower(suffix)) {
		return trimmed
	}
	return trimmed + suffix
}

// StripPathSuffix removes the first matching suffix (checked in caller
// order, so pass longer suffixes first) from base, case-insensitively.
func StripPathSuffix(base string, suffixes []string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	lowered := strings.ToLower(trimmed)
	for _, suffix := range suffixes {
		if strings.HasSuffix(lowered, strings.ToLower(suffix)) {
			return strings.TrimRight(trimmed[:len(trimmed)-len(suffix)], "/")
		}
	}
	return trimmed
}
