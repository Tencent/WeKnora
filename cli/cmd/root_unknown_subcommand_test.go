package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
)

// TestAgentInvoke_NowReturnsUnknownSubcommand verifies the deleted v0.6
// command emits a typed envelope rather than cobra's free-form exit-2 prose.
func TestAgentInvoke_NowReturnsUnknownSubcommand(t *testing.T) {
	root := NewRootCmd(cmdutil.New())
	root.SetArgs([]string{"agent", "invoke", "ag_x", "q"})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	err := root.Execute()
	ce := cmdutil.AsError(err)
	if ce == nil || ce.Code != cmdutil.CodeInputUnknownSubcommand {
		t.Errorf("expected CodeInputUnknownSubcommand, got %v", err)
	}
}

func TestUnknownSubcommand_EmitsTypedEnvelope(t *testing.T) {
	t.Cleanup(func() { cmdutil.SetFormatMode("") })

	root := NewRootCmd(cmdutil.New())
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs([]string{"fooo"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected unknown-subcommand error, got nil")
	}

	// Force JSON mode for PrintError regardless of how PersistentPreRunE
	// resolved it during the test invocation (no TTY in test buffer).
	cmdutil.SetFormatMode("json")
	mapped := MapCobraError(err)
	cmdutil.PrintError(&stderr, mapped)

	got := stderr.String()
	if !strings.Contains(got, `"type":"input.unknown_subcommand"`) {
		t.Errorf("expected typed code; got %q", got)
	}
	if !strings.Contains(got, `"available":[`) {
		t.Errorf("expected detail.available[]; got %q", got)
	}
	if !strings.Contains(got, `"retry_command":"weknora --help"`) {
		t.Errorf("expected retry_command; got %q", got)
	}
}
