package agentcmd

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// agentDryRunFactory builds a Factory whose Client closure panics if invoked —
// dry-run must early-exit before any SDK call.
func agentDryRunFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Client: func() (*sdk.Client, error) {
			t.Fatal("dry-run path must not call Factory.Client(); SDK side effect leaked")
			return nil, nil
		},
		Prompter: func() prompt.Prompter {
			t.Fatal("dry-run path must not call Factory.Prompter(); confirm-prompt side effect leaked")
			return nil
		},
	}
}

// TestAgentEdit_DryRun_RejectsNoFlag: --dry-run must reject the same
// "no update flag supplied" invocation the live path rejects. Before the
// fix the editHasAnyFlag check lived in runEdit() (live-only), reached only
// after HandleDryRun early-exited.
func TestAgentEdit_DryRun_RejectsNoFlag(t *testing.T) {
	iostreams.SetForTest(t)
	f := agentDryRunFactory(t)
	root := withRootHarnessAgent(NewCmdEdit(f), "ag_abc", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject identically to live path")

	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}
