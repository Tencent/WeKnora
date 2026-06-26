package cmdutil

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
)

// unsafeRune reports whether r is a control / formatting character that has no
// place in a CLI-supplied value: C0 control codes (except the ordinary
// whitespace \t \n \r), DEL, Unicode Bidi overrides/isolates, and zero-width
// characters.
//
// These are rejected because CLI arguments are untrusted — in an agent-first
// CLI they frequently originate from an AI agent or upstream content. A
// smuggled ANSI escape (ESC, U+001B) or Bidi override stored in a resource
// name would execute in, or visually mislead, a human's terminal the moment
// the value is rendered by a later `list` / `view`. Blocking at the input
// boundary mirrors the input-hygiene stance of other agent-first CLIs.
func unsafeRune(r rune) bool {
	switch {
	case r == '\t' || r == '\n' || r == '\r':
		return false // ordinary whitespace is allowed (multi-line --text etc.)
	case r < 0x20 || r == 0x7f:
		return true // C0 controls (includes ESC U+001B) and DEL
	case r >= 0x202a && r <= 0x202e:
		return true // LRE RLE PDF LRO RLO — Bidi overrides
	case r >= 0x2066 && r <= 0x2069:
		return true // LRI RLI FSI PDI — Bidi isolates
	case r == 0x200b || r == 0x200c || r == 0x200d || r == 0xfeff:
		return true // zero-width space / non-joiner / joiner / BOM
	}
	return false
}

// CheckSafeText returns an input.invalid_argument error if value contains a
// control / Bidi / zero-width character (see unsafeRune). label names the
// offending input so an agent can fix the right argument. Returns nil for
// ordinary text (including multi-line content with \t \n \r).
func CheckSafeText(value, label string) error {
	for i, r := range value {
		if unsafeRune(r) {
			return &Error{
				Code: CodeInputInvalidArgument,
				Message: fmt.Sprintf(
					"%s contains a disallowed control character (U+%04X at byte %d); strip control / ANSI-escape / Bidi / zero-width characters",
					label, r, i,
				),
			}
		}
	}
	return nil
}

// CheckSafeArgs is the command-boundary input gate, hooked into the root
// PersistentPreRunE so every command inherits it without per-command wiring.
// It enforces two independent rules, each in its own helper so the rule is
// discoverable:
//
//   - checkNonEmptyArgs: every positional argument must be non-empty.
//   - CheckSafeText: no positional argument or explicitly-set flag value may
//     carry control / ANSI / Bidi / zero-width characters.
//
// Default (unchanged) flag values are not scanned — only what the caller
// actually supplied.
func CheckSafeArgs(args []string, flags *pflag.FlagSet) error {
	if err := checkNonEmptyArgs(args); err != nil {
		return err
	}
	for i, a := range args {
		if err := CheckSafeText(a, fmt.Sprintf("argument %d", i+1)); err != nil {
			return err
		}
	}
	if flags == nil {
		return nil
	}
	var ferr error
	flags.Visit(func(f *pflag.Flag) {
		if ferr == nil {
			ferr = CheckSafeText(f.Value.String(), "--"+f.Name)
		}
	})
	return ferr
}

// checkNonEmptyArgs rejects empty / whitespace-only positional arguments before
// any command logic runs — including the dry-run and exit-10 confirmation gates.
// Every positional in this CLI is an id, name, query, or path; none is
// meaningfully empty. The common agent trigger is capturing an id from a failed
// prior step (KB=$(weknora kb create … --jq .data.id) when the create failed →
// ""), which otherwise reaches the server as an empty path segment: GET/DELETE
// /resource/ falls through to the LIST route, the array response fails to
// unmarshal into a single-object struct, and the failure is mis-reported as
// network.error ("check base URL"). Worse, an empty-id delete reaches the
// confirmation gate with a malformed `weknora kb delete  -y` retry_argv.
// Caught here as a clear input.invalid_argument (exit 5) instead.
//
// This is intentionally a global invariant (no command exempts itself); flag
// VALUES are not covered, so `--description ""` still legitimately clears a field.
func checkNonEmptyArgs(args []string) error {
	for i, a := range args {
		if strings.TrimSpace(a) == "" {
			return &Error{
				Code:    CodeInputInvalidArgument,
				Message: fmt.Sprintf("argument %d is empty", i+1),
				Hint:    "an empty id / name / query is never valid — verify the value you captured (e.g. from a prior --jq) is non-empty",
			}
		}
	}
	return nil
}
