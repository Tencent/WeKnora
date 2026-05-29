// Package profilecmd — dryrun_validation_test.go asserts that --dry-run on
// profile subcommands rejects identically to the live path. Before the
// surrounding fix, validation lived in runX() and was reached only after
// HandleDryRun short-circuited, so --dry-run accepted invocations the live
// path would reject.
package profilecmd

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	sdk "github.com/Tencent/WeKnora/client"
)

// profileDryRunFactory builds a Factory whose Client closure panics if
// invoked — dry-run must early-exit before any SDK call.
func profileDryRunFactory(t *testing.T) *cmdutil.Factory {
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
		Secrets: func() (secrets.Store, error) { return secrets.NewMemStore(), nil },
	}
}

// withRootHarnessProfile wraps a profile subcommand under a synthetic root
// cmd that registers the global persistent flags.
func withRootHarnessProfile(sub *cobra.Command, args ...string) *cobra.Command {
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

// TestProfileAdd_DryRun_RejectsDuplicate: profile already exists → live path
// returns resource.already_exists; --dry-run must do the same.
func TestProfileAdd_DryRun_RejectsDuplicate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	iostreams.SetForTest(t)
	require.NoError(t, config.Save(&config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://prod"}},
	}))

	root := withRootHarnessProfile(NewCmdAdd(profileDryRunFactory(t)),
		"prod", "--host", "https://other.example.com", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject duplicate profile name")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeResourceAlreadyExists, typed.Code)
}

// TestProfileAdd_DryRun_RejectsInvalidName: shell-unsafe profile name → live
// path returns input.invalid_argument; --dry-run must do the same.
func TestProfileAdd_DryRun_RejectsInvalidName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	iostreams.SetForTest(t)

	root := withRootHarnessProfile(NewCmdAdd(profileDryRunFactory(t)),
		"bad name", "--host", "https://x.example.com", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject invalid profile name")
}

// TestProfileRemove_DryRun_RejectsUnknownName: removing a nonexistent profile
// returns local.profile_not_found on the live path; --dry-run must too.
func TestProfileRemove_DryRun_RejectsUnknownName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	iostreams.SetForTest(t)
	require.NoError(t, config.Save(&config.Config{
		Profiles: map[string]config.Profile{"prod": {Host: "https://prod"}},
	}))

	root := withRootHarnessProfile(NewCmdRemove(profileDryRunFactory(t)),
		"ghost", "--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject unknown profile name")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeLocalProfileNotFound, typed.Code)
}
