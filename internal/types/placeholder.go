package types

import (
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Unified prompt placeholder rendering
// ---------------------------------------------------------------------------

// PlaceholderValues is a map of placeholder names (without braces) to their
// replacement values. Example: {"query": "How to use?", "language": "English"}
type PlaceholderValues map[string]string

// RenderPromptPlaceholders replaces all {{key}} occurrences in template with
// the corresponding values from vals. Unknown placeholders are left untouched.
//
// Built-in auto-values (filled when not supplied explicitly):
//   - {{current_time}} -> time.Now().Format("2006-01-02 15:04:05")
//   - {{current_week}} -> current weekday name
//   - {{yesterday}}    -> yesterday's date (2006-01-02)
func RenderPromptPlaceholders(template string, vals PlaceholderValues) string {
	if template == "" {
		return ""
	}

	// Populate auto-generated values when callers don't supply them.
	autoFill := func(key, value string) {
		if _, exists := vals[key]; !exists {
			if strings.Contains(template, "{{"+key+"}}") {
				vals[key] = value
			}
		}
	}

	now := time.Now()
	autoFill("current_time", now.Format("2006-01-02 15:04:05"))
	autoFill("current_week", now.Weekday().String())
	autoFill("yesterday", now.AddDate(0, 0, -1).Format("2006-01-02"))

	result := template
	for key, value := range vals {
		placeholder := "{{" + key + "}}"
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, value)
		}
	}
	return result
}
