// Package text holds string helpers for human-readable CLI output.
// All functions are pure (no I/O, no time.Now), making them trivially testable.
package text

import "fmt"

// Pluralize returns "<n> <thing>" or "<n> <thing>s". Simple suffix "s" only;
// irregular forms (person/people) are not supported.
func Pluralize(n int, thing string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, thing)
	}
	return fmt.Sprintf("%d %ss", n, thing)
}
