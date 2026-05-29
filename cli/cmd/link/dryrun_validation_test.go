// Package linkcmd — dryrun_validation_test.go asserts that --dry-run on
// link / unlink rejects identically to the live path. Before the surrounding
// fix, validation lived in runX() and was reached only after HandleDryRun
// short-circuited, so --dry-run accepted invocations the live path rejects.
package linkcmd

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	sdk "github.com/Tencent/WeKnora/client"
)

// linkDryRunFactory builds a Factory whose Client closure panics if invoked —
// dry-run must early-exit before any SDK call.
func linkDryRunFactory(t *testing.T, cfg *config.Config) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Config: func() (*config.Config, error) { return cfg, nil },
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

// withRootHarnessLink wraps a link subcommand under a synthetic root cmd
// that registers the global persistent flags.
func withRootHarnessLink(sub *cobra.Command, args ...string) *cobra.Command {
	root := &cobra.Command{Use: "weknora"}
	pf := root.PersistentFlags()
	pf.BoolP("yes", "y", false, "")
	pf.String("format", "", "")
	pf.StringP("jq", "q", "", "")
	root.AddCommand(sub)
	root.SetArgs(append([]string{sub.Name()}, args...))
	root.SetContext(context.Background())
	root.SilenceErrors = true
	root.SilenceUsage = true
	return root
}

// TestLink_DryRun_RejectsNoCurrentProfile: link with no active profile →
// live path returns auth.unauthenticated; --dry-run must do the same.
func TestLink_DryRun_RejectsNoCurrentProfile(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{}
	root := withRootHarnessLink(NewCmd(linkDryRunFactory(t, cfg)),
		"--kb", "00000000-0000-0000-0000-000000000001", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject when no current profile")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

// TestLink_DryRun_RejectsNoKBNoTTY: --kb omitted on non-TTY → live path
// returns local.kb_id_required; --dry-run must do the same.
func TestLink_DryRun_RejectsNoKBNoTTY(t *testing.T) {
	iostreams.SetForTest(t) // non-TTY
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://prod"}},
	}
	root := withRootHarnessLink(NewCmd(linkDryRunFactory(t, cfg)),
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject missing --kb on non-TTY")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeKBIDRequired, typed.Code)
}

// TestUnlink_DryRun_RejectsMissingLink: cwd has no .weknora/project.yaml →
// live path returns input.invalid_argument; --dry-run must do the same.
func TestUnlink_DryRun_RejectsMissingLink(t *testing.T) {
	iostreams.SetForTest(t)
	// Change to an empty temp dir so projectlink.Discover finds nothing.
	tmp := t.TempDir()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(tmp))
	t.Cleanup(func() { _ = os.Chdir(prev) })

	root := withRootHarnessLink(NewCmdUnlink(),
		"--dry-run", "--format", "json")
	err = root.Execute()
	require.Error(t, err, "dry-run must reject when no project link present")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}
