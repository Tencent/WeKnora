package cmdutil

import (
	"errors"
	"strings"
	"testing"
)

// TestValidateProfileName_AcceptsAllowlist verifies the documented charset
// is accepted as-is.
func TestValidateProfileName_AcceptsAllowlist(t *testing.T) {
	for _, name := range []string{
		"default",
		"prod",
		"staging-2026",
		"ci_runner",
		"alice.example",
		"a", // single char
		"A-Z_0-9",
	} {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q) unexpected error: %v", name, err)
		}
	}
}

// TestValidateProfileName_RejectsEmpty guards the empty-string base case.
func TestValidateProfileName_RejectsEmpty(t *testing.T) {
	err := ValidateProfileName("")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	var ce *Error
	if !errors.As(err, &ce) || ce.Code != CodeInputInvalidArgument {
		t.Errorf("expected input.invalid_argument, got %v", err)
	}
}

// TestValidateProfileName_RejectsShellMetachars is the security-critical
// case: anything that could break retry_command shell interpolation must be
// rejected at the entry point. If this test ever loosens, an agent that
// exec()s retry_command becomes injectable via a malicious profile name.
func TestValidateProfileName_RejectsShellMetachars(t *testing.T) {
	cases := []string{
		"evil; rm -rf /",
		"foo && bar",
		"foo || bar",
		"foo|bar",
		"foo`whoami`",
		"foo$(whoami)",
		"foo$bar",
		"foo>out",
		"foo<in",
		"foo bar", // space alone
		"foo'bar",
		`foo"bar`,
		"foo\nbar", // newline
		"foo\tbar", // tab
		"foo\\bar", // backslash (also path-like)
		"foo/bar",  // slash (also path-like)
		"#foo",     // comment marker
		"foo*bar",  // glob
		"foo?bar",  // glob
		"foo~bar",  // home expansion
		"foo!bar",  // history expansion
		"foo,bar",  // brace-expansion-ish
		"foo[bar",
	}
	for _, name := range cases {
		err := ValidateProfileName(name)
		if err == nil {
			t.Errorf("ValidateProfileName(%q) should have rejected the name; an agent exec'ing retry_command would be injectable", name)
			continue
		}
		var ce *Error
		if !errors.As(err, &ce) || ce.Code != CodeInputInvalidArgument {
			t.Errorf("ValidateProfileName(%q) returned wrong code %v; want input.invalid_argument", name, err)
		}
	}
}

// TestValidateProfileName_RejectsPathTraversal covers the keyring-namespace
// escape vector. `.` and `..` are reserved, and any slash is rejected.
func TestValidateProfileName_RejectsPathTraversal(t *testing.T) {
	for _, name := range []string{".", "..", "../foo", "foo/..", "a/b", `a\b`} {
		err := ValidateProfileName(name)
		if err == nil {
			t.Errorf("ValidateProfileName(%q) should have rejected the path-like name", name)
			continue
		}
		// Path-shaped names hit the dedicated "reserved or path-like" branch
		// first (clearer hint than "invalid character %q") for `..` and the
		// slashed forms.
		if name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			if !strings.Contains(err.Error(), "reserved or path-like") {
				t.Errorf("ValidateProfileName(%q): expected path-like hint, got %v", name, err)
			}
		}
	}
}

// TestValidateProfileName_HintMentionsAllowlist verifies the user-facing
// hint actually tells them what's allowed (not just "invalid").
func TestValidateProfileName_HintMentionsAllowlist(t *testing.T) {
	err := ValidateProfileName("foo bar")
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected typed Error, got %v", err)
	}
	if !strings.Contains(ce.Hint, "letters") || !strings.Contains(ce.Hint, "dots") {
		t.Errorf("hint should describe allowlist; got %q", ce.Hint)
	}
}
