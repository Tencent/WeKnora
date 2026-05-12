package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// TestCompletion_AllShells smoke-tests cobra's auto-registered completion
// command. The output is shell-specific bytecode/script; the test just
// asserts each shell produces a non-trivially-sized script and references
// the binary name. Guards against future cobra bumps silently breaking
// completion for one shell.
func TestCompletion_AllShells(t *testing.T) {
	cases := []struct {
		shell        string
		mustContain  string // signature byte sequence specific to the shell
		minSizeBytes int
	}{
		{"bash", "bash completion", 1024},
		{"zsh", "#compdef", 1024},
		{"fish", "complete -c weknora", 1024},
		{"powershell", "Register-ArgumentCompleter", 1024},
	}
	for _, tc := range cases {
		t.Run(tc.shell, func(t *testing.T) {
			cmd := NewRootCmd(&cmdutil.Factory{})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs([]string{"completion", tc.shell})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("completion %s: %v", tc.shell, err)
			}
			if buf.Len() < tc.minSizeBytes {
				t.Errorf("%s script suspiciously short: %d bytes", tc.shell, buf.Len())
			}
			if !strings.Contains(buf.String(), tc.mustContain) {
				t.Errorf("%s script missing signature %q (first 200 bytes: %q)",
					tc.shell, tc.mustContain, buf.String()[:min(200, buf.Len())])
			}
		})
	}
}
