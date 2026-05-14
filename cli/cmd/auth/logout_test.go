package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

// newLogoutFactory builds a Factory whose Config closure mutates the supplied
// cfg in place - runLogout writes back via config.Save which touches disk, so
// tests use t.Setenv("XDG_CONFIG_HOME", t.TempDir()) at the call site to
// isolate the on-disk file.
func newLogoutFactory(t *testing.T, cfg *config.Config, store secrets.Store) *cmdutil.Factory {
	t.Helper()
	return &cmdutil.Factory{
		Config:  func() (*config.Config, error) { return cfg, nil },
		Secrets: func() (secrets.Store, error) { return store, nil },
	}
}

func isolateConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func TestLogout_CurrentContext(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("prod", "access", "jwt-prod"))
	require.NoError(t, store.Set("prod", "refresh", "rfr-prod"))
	require.NoError(t, store.Set("staging", "api_key", "sk-staging"))

	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Host: "https://prod", TokenRef: store.Ref("prod", "access"), RefreshRef: store.Ref("prod", "refresh")},
			"staging": {Host: "https://staging", APIKeyRef: store.Ref("staging", "api_key")},
		},
	}
	require.NoError(t, runLogout(&LogoutOptions{}, nil, newLogoutFactory(t, cfg, store)))

	assert.Empty(t, cfg.CurrentContext, "current_context should clear when removed")
	assert.NotContains(t, cfg.Contexts, "prod")
	assert.Contains(t, cfg.Contexts, "staging", "non-target context untouched")

	// Secrets gone for the removed context, kept for the survivor.
	if _, err := store.Get("prod", "access"); err == nil {
		t.Error("prod access secret should be deleted")
	}
	if v, _ := store.Get("staging", "api_key"); v != "sk-staging" {
		t.Errorf("staging secret unexpectedly cleared: %q", v)
	}
}

func TestLogout_NamedContext(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("staging", "api_key", "sk-staging"))

	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Host: "https://prod", TokenRef: "tok"},
			"staging": {Host: "https://staging", APIKeyRef: store.Ref("staging", "api_key")},
		},
	}
	require.NoError(t, runLogout(&LogoutOptions{Name: "staging"}, nil, newLogoutFactory(t, cfg, store)))

	assert.Equal(t, "prod", cfg.CurrentContext, "current_context untouched when removing other")
	assert.NotContains(t, cfg.Contexts, "staging")
	assert.Contains(t, cfg.Contexts, "prod")
}

func TestLogout_All(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Host: "https://prod"},
			"staging": {Host: "https://staging"},
		},
	}
	require.NoError(t, runLogout(&LogoutOptions{All: true}, nil, newLogoutFactory(t, cfg, store)))

	assert.Empty(t, cfg.Contexts)
	assert.Empty(t, cfg.CurrentContext)
}

func TestLogout_NoContexts(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{}
	err := runLogout(&LogoutOptions{}, nil, newLogoutFactory(t, cfg, secrets.NewMemStore()))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

func TestLogout_UnknownName(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts:       map[string]config.Context{"prod": {Host: "https://prod"}},
	}
	err := runLogout(&LogoutOptions{Name: "ghost"}, nil, newLogoutFactory(t, cfg, secrets.NewMemStore()))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeLocalContextNotFound, typed.Code)
}

func TestLogout_NoCurrentNoFlag(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{
		Contexts: map[string]config.Context{"prod": {Host: "https://prod"}},
	}
	err := runLogout(&LogoutOptions{}, nil, newLogoutFactory(t, cfg, secrets.NewMemStore()))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputMissingFlag, typed.Code)
}

// TestLogout_Cobra exercises the cobra layer for the mutually-exclusive
// --name + --all flag pair.
func TestLogout_Cobra_FlagsMutuallyExclusive(t *testing.T) {
	isolateConfig(t)
	_, _ = iostreams.SetForTest(t)
	cfg := &config.Config{Contexts: map[string]config.Context{"a": {}}}
	cmd := NewCmdLogout(newLogoutFactory(t, cfg, secrets.NewMemStore()))
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--name", "a", "--all"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	require.Error(t, cmd.Execute())
}
