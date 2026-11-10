package cmdutil

import (
	"errors"
	"testing"

	"github.com/spf13/pflag"
)

func TestCheckSafeText(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"plain ascii", "my-knowledge-base", false},
		{"unicode letters", "测试知识库 café", false},
		{"emoji", "docs 🔥", false},
		{"multiline content allowed", "line1\nline2\twith tab\r\n", false},
		{"ansi escape rejected", "inject\x1b[31mRED", true},
		{"bell rejected", "name\x07bell", true},
		{"null byte rejected", "a\x00b", true},
		{"DEL rejected", "a\x7fb", true},
		{"bidi override rejected", "abc\u202ervd", true},
		{"zero-width rejected", "ab\u200bc", true},
		{"BOM rejected", "\ufeffname", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSafeText(tc.value, "name")
			if tc.wantErr && err == nil {
				t.Errorf("expected rejection for %q, got nil", tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected %q to be allowed, got %v", tc.value, err)
			}
			if tc.wantErr {
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != CodeInputInvalidArgument {
					t.Errorf("want *Error with CodeInputInvalidArgument, got %v", err)
				}
			}
		})
	}
}

func TestCheckSafeArgs_PositionalAndFlags(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	name := fs.String("name", "", "")
	_ = fs.Parse([]string{"--name", "clean-name"})

	// Clean positional + clean flag → ok.
	if err := CheckSafeArgs([]string{"good-arg"}, fs); err != nil {
		t.Errorf("clean args should pass, got %v", err)
	}

	// Dirty positional arg → rejected, names the argument.
	if err := CheckSafeArgs([]string{"bad\x1b[0marg"}, fs); err == nil {
		t.Error("control char in positional arg must be rejected")
	}

	// Dirty flag value → rejected.
	*name = "x" // touch to avoid unused
	fs2 := pflag.NewFlagSet("t2", pflag.ContinueOnError)
	fs2.String("title", "", "")
	_ = fs2.Parse([]string{"--title", "evil\x07"})
	if err := CheckSafeArgs(nil, fs2); err == nil {
		t.Error("control char in flag value must be rejected")
	}
}

// TestCheckSafeArgs_RejectsEmptyPositional pins that empty / whitespace-only
// positional args are rejected as input.invalid_argument before any command
// logic — the common agent failure of capturing an id from a failed prior step
// (KB=$(... --jq .data.id) → "") must surface clearly, not reach the server as
// an empty path segment (mis-classified network.error) or a malformed
// `delete  -y` confirmation. Empty FLAG values stay allowed (--description ""
// legitimately clears a field).
func TestCheckSafeArgs_RejectsEmptyPositional(t *testing.T) {
	for _, arg := range []string{"", "   ", "\t", "\n"} {
		err := CheckSafeArgs([]string{arg}, nil)
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != CodeInputInvalidArgument {
			t.Errorf("empty positional %q must be rejected as input.invalid_argument, got %v", arg, err)
		}
	}
	// A non-empty positional alongside an empty one still trips on the empty.
	if err := CheckSafeArgs([]string{"ok", ""}, nil); err == nil {
		t.Error("a later empty positional must be rejected")
	}
	// Empty flag value is NOT rejected (clearing a field is valid).
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("description", "", "")
	_ = fs.Parse([]string{"--description", ""})
	if err := CheckSafeArgs([]string{"id1"}, fs); err != nil {
		t.Errorf("empty flag value must stay allowed, got %v", err)
	}
}

// TestCheckSafeArgs_UnchangedFlagsNotScanned pins that default (unset) flag
// values are not scanned — only what the caller supplied.
func TestCheckSafeArgs_UnchangedFlagsNotScanned(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	// A default that (hypothetically) contains a control char must not trip
	// the check when the user never set the flag.
	fs.String("weird", "default\x1bvalue", "")
	_ = fs.Parse(nil)
	if err := CheckSafeArgs(nil, fs); err != nil {
		t.Errorf("unset flag default must not be scanned, got %v", err)
	}
}
