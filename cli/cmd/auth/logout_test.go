package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

// newLogoutFactory builds a Factory whose Config closure mutates the supplied
// cfg in place - runLogout writes back via config.Save which touches disk, so
// tests use t.Setenv("XDG_CONFIG_HOME", t.TempDir()) at the call site to
// isolate the on-disk file.
func newLogoutFactory(t *testing.T, cfg *config.Config, store secrets.Store) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Config:   func() (*config.Config, error) { return cfg, nil },
		Secrets:  func() (secrets.Store, error) { return store, nil },
		Prompter: func() prompt.Prompter { return &prompt.AgentPrompter{} },
	}
}

func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestLogout_CurrentProfile(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("prod", "access", "jwt-prod"))
	require.NoError(t, store.Set("prod", "refresh", "rfr-prod"))
	require.NoError(t, store.Set("staging", "api_key", "sk-staging"))

	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod":    {Host: "https://prod", TokenRef: store.Ref("prod", "access"), RefreshRef: store.Ref("prod", "refresh")},
			"staging": {Host: "https://staging", APIKeyRef: store.Ref("staging", "api_key")},
		},
	}
	require.NoError(t, runLogout(&LogoutOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, newLogoutFactory(t, cfg, store)))

	assert.Empty(t, cfg.CurrentProfile, "current_profile should clear when removed")
	assert.NotContains(t, cfg.Profiles, "prod")
	assert.Contains(t, cfg.Profiles, "staging", "non-target profile untouched")

	// Secrets gone for the removed profile, kept for the survivor.
	if _, err := store.Get("prod", "access"); err == nil {
		t.Error("prod access secret should be deleted")
	}
	if v, _ := store.Get("staging", "api_key"); v != "sk-staging" {
		t.Errorf("staging secret unexpectedly cleared: %q", v)
	}
}

// TestLogout_ActiveProfileViaOverride exercises targeting a non-default
// profile. Production resolves this via the global --profile flag (which
// rewrites cfg.CurrentProfile in Factory.Config); here we simulate that by
// setting CurrentProfile=staging directly, since runLogout's single target is
// the active profile.
func TestLogout_ActiveProfileViaOverride(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("staging", "api_key", "sk-staging"))

	cfg := &config.Config{
		CurrentProfile: "staging", // global --profile staging resolves to this
		Profiles: map[string]config.Profile{
			"prod":    {Host: "https://prod", TokenRef: "tok"},
			"staging": {Host: "https://staging", APIKeyRef: store.Ref("staging", "api_key")},
		},
	}
	require.NoError(t, runLogout(&LogoutOptions{Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, newLogoutFactory(t, cfg, store)))

	assert.NotContains(t, cfg.Profiles, "staging", "active profile (staging) is the target")
	assert.Contains(t, cfg.Profiles, "prod", "non-target profile untouched")
}

func TestLogout_All(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod":    {Host: "https://prod"},
			"staging": {Host: "https://staging"},
		},
	}
	require.NoError(t, runLogout(&LogoutOptions{All: true, Yes: true}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, newLogoutFactory(t, cfg, store)))

	assert.Empty(t, cfg.Profiles)
	assert.Empty(t, cfg.CurrentProfile)
}

func TestLogout_NoProfiles(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{}
	err := runLogout(&LogoutOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, newLogoutFactory(t, cfg, secrets.NewMemStore()))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

// TestLogout_ActiveProfileMissing covers the corrupt-config case where the
// active profile name (e.g. set via --profile ghost) has no matching entry.
func TestLogout_ActiveProfileMissing(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentProfile: "ghost", // points at a non-existent entry
		Profiles:       map[string]config.Profile{"prod": {Host: "https://prod"}},
	}
	err := runLogout(&LogoutOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, newLogoutFactory(t, cfg, secrets.NewMemStore()))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeLocalProfileNotFound, typed.Code)
}

func TestLogout_NoCurrentNoFlag(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{
		Profiles: map[string]config.Profile{"prod": {Host: "https://prod"}},
	}
	err := runLogout(&LogoutOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, newLogoutFactory(t, cfg, secrets.NewMemStore()))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
}

// TestLogout_NoNameFlag asserts the --name flag is gone (logout now targets
// the active profile / global --profile) while --all stands alone.
func TestLogout_NoNameFlag(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{Profiles: map[string]config.Profile{"a": {}}}
	cmd := NewCmdLogout(newLogoutFactory(t, cfg, secrets.NewMemStore()))
	assert.Nil(t, cmd.Flags().Lookup("name"), "--name flag must be removed")
	assert.NotNil(t, cmd.Flags().Lookup("all"), "--all flag must remain")
}
