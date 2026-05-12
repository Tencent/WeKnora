package search

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

// TestSearch_NoArgs_ShowsHelp: bare `weknora search` (no subcommand)
// must run cobra's Help() without erroring.
func TestSearch_NoArgs_ShowsHelp(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cmd := NewCmdSearch(&cmdutil.Factory{})
	cmd.SetArgs([]string{})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	require.NoError(t, cmd.Execute())
}

// TestSearch_RejectsPositional: bare positional (e.g. the v0.0-style
// `weknora search "<q>" --kb X`) must error — the legacy alias was
// dropped, search is now pure dispatcher.
func TestSearch_RejectsPositional(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	cmd := NewCmdSearch(&cmdutil.Factory{})
	cmd.SetArgs([]string{"hello", "--kb", "kb_abc"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	require.Error(t, err)
}

// TestSearch_SubcommandsRegistered: ensure chunks/kb/docs/sessions are
// reachable through the parent. Smoke-test only; the subcommands' own
// tests cover behavior.
func TestSearch_SubcommandsRegistered(t *testing.T) {
	f := &cmdutil.Factory{}
	cmd := NewCmdSearch(f)
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"chunks", "kb", "docs", "sessions"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}
