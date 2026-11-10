// Package auth — dryrun_validation_test.go asserts that --dry-run on
// auth subcommands rejects identically to the live path. Before the
// surrounding fix, validation lived in runX() and was reached only after
// HandleDryRun short-circuited, so --dry-run accepted invocations the live
// path would reject.
package auth

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

// authDryRunFactory builds a Factory whose Client closure panics if invoked —
// dry-run must early-exit before any SDK call. Config is supplied via the cfg
// argument so each test gets isolated state.
func authDryRunFactory(t *testing.T, cfg *config.Config) *cmdutil.Factory {
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
		Secrets: func() (secrets.Store, error) { return secrets.NewMemStore(), nil },
	}
}

// withRootHarnessAuth wraps an auth subcommand under a synthetic root cmd
// that registers the global persistent flags.
func withRootHarnessAuth(sub *cobra.Command, args ...string) *cobra.Command {
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

// TestAuthLogout_DryRun_RejectsNoProfiles: empty config → live path returns
// auth.unauthenticated; --dry-run must do the same.
func TestAuthLogout_DryRun_RejectsNoProfiles(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{}
	root := withRootHarnessAuth(NewCmdLogout(authDryRunFactory(t, cfg)),
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject empty config")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

// TestAuthLogout_DryRun_RejectsMissingActiveProfile: the active profile name
// (e.g. set via the global --profile) has no config entry → live path returns
// local.profile_not_found; --dry-run must do the same.
func TestAuthLogout_DryRun_RejectsMissingActiveProfile(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentProfile: "ghost", // active profile with no matching entry
		Profiles:       map[string]config.Profile{"prod": {Host: "https://prod"}},
	}
	root := withRootHarnessAuth(NewCmdLogout(authDryRunFactory(t, cfg)),
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err, "dry-run must reject a missing active profile")
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed), "expected *cmdutil.Error, got %T %v", err, err)
	assert.Equal(t, cmdutil.CodeLocalProfileNotFound, typed.Code)
}

// TestAuthRefresh_DryRun_RejectsNoCurrentProfile: no current profile → live
// path returns auth.unauthenticated; --dry-run must do the same.
func TestAuthRefresh_DryRun_RejectsNoCurrentProfile(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{}
	root := withRootHarnessAuth(NewCmdRefresh(authDryRunFactory(t, cfg)),
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

// TestAuthRefresh_DryRun_RejectsMissingRefreshToken: API-key-only profile →
// live path returns input.invalid_argument ("no refresh token"); --dry-run
// must do the same.
func TestAuthRefresh_DryRun_RejectsMissingRefreshToken(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod": {Host: "https://prod", APIKeyRef: "k"},
		},
	}
	root := withRootHarnessAuth(NewCmdRefresh(authDryRunFactory(t, cfg)),
		"--dry-run", "--format", "json")
	err := root.Execute()
	require.Error(t, err)
	var typed *cmdutil.Error
	require.True(t, errors.As(err, &typed))
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
}
