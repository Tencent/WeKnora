package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
	sdk "github.com/Tencent/WeKnora/client"
)

func TestNewCmdAuth_TreeShape(t *testing.T) {
	cmd := NewCmdAuth(&cmdutil.Factory{})
	assert.Equal(t, "auth", cmd.Use)
	subs := map[string]*cobra.Command{}
	for _, c := range cmd.Commands() {
		subs[c.Use] = c
	}
	assert.Contains(t, subs, "login")
	assert.Contains(t, subs, "status")
}

func TestNewCmdLogin_FlagsRegistered(t *testing.T) {
	cmd := NewCmdLogin(&cmdutil.Factory{}, nil)
	// --format is a persistent root flag (v0.7); only per-command flags here.
	assert.NotNil(t, cmd.Flags().Lookup("with-token"), "flag with-token missing")
	// login authenticates the active profile; host/name come from config
	// (created via `profile add`), so these flags are gone.
	assert.Nil(t, cmd.Flags().Lookup("host"), "auth login must not declare --host (v0.9)")
	assert.Nil(t, cmd.Flags().Lookup("name"), "auth login must not declare --name (v0.9)")
	// --profile is the global persistent override; local registration would
	// silently shadow it.
	assert.Nil(t, cmd.Flags().Lookup("profile"), "auth login must not declare a local --profile flag (use the global --profile)")
}

func TestNewCmdLogin_InvokesRunF(t *testing.T) {
	iostreams.SetForTest(t)
	called := false
	store := secrets.NewMemStore()
	f := &cmdutil.Factory{
		Secrets: func() (secrets.Store, error) { return store, nil },
	}
	cmd := NewCmdLogin(f, func(_ context.Context, opts *LoginOptions, _ *cmdutil.FormatOptions, _ *cmdutil.Factory, _ LoginService) error {
		called = true
		// host/profile resolve inside runLogin from the active profile;
		// the runF seam just receives the parsed flags.
		assert.True(t, opts.WithToken)
		return nil
	})
	cmd.SetArgs([]string{"--with-token"})
	require.NoError(t, cmd.Execute())
	assert.True(t, called)
}

func TestLoginServiceFor(t *testing.T) {
	assert.Nil(t, loginServiceFor(""))
	assert.NotNil(t, loginServiceFor("https://x"))
}

func TestPersistAPIKey_WritesContext(t *testing.T) {
	iostreams.SetForTest(t)
	testutil.XDGTempDir(t)
	store := secrets.NewMemStore()
	f := &cmdutil.Factory{
		Config:   func() (*config.Config, error) { return config.Load() },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		Secrets:  func() (secrets.Store, error) { return store, nil },
	}
	// persistAPIKey MERGES into the existing active profile record (created
	// via `profile add`) rather than creating it; seed it first.
	require.NoError(t, config.Save(&config.Config{
		CurrentProfile: "ci",
		Profiles:       map[string]config.Profile{"ci": {Host: "https://kb.example.com"}},
	}))
	opts := &LoginOptions{
		Host:    "https://kb.example.com",
		Profile: "ci",
		APIKey:  "sk-zzz",
	}
	require.NoError(t, persistAPIKey(opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil))
	v, _ := store.Get("ci", "api_key")
	assert.Equal(t, "sk-zzz", v)
	cfg, _ := f.Config()
	assert.Equal(t, "ci", cfg.CurrentProfile, "active profile unchanged by login")
	assert.Equal(t, "https://kb.example.com", cfg.Profiles["ci"].Host, "existing host preserved by merge")
	// APIKeyRef should be the mem:// URI from the store's Ref method.
	assert.Equal(t, "mem://ci/api_key", cfg.Profiles["ci"].APIKeyRef)
}

func TestPersistJWT_StoresBothTokens(t *testing.T) {
	iostreams.SetForTest(t)
	testutil.XDGTempDir(t)
	store := secrets.NewMemStore()
	f := &cmdutil.Factory{
		Config:   func() (*config.Config, error) { return config.Load() },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		Secrets:  func() (secrets.Store, error) { return store, nil },
	}
	opts := &LoginOptions{
		Host:    "https://x",
		Profile: "p",
	}
	resp := &sdk.LoginResponse{
		Token:        "jwt-acc",
		RefreshToken: "jwt-ref",
		User:         &sdk.AuthUser{Email: "a@b.c", TenantID: 7},
	}
	require.NoError(t, persistJWT(opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, f, resp))
	a, _ := store.Get("p", "access")
	r, _ := store.Get("p", "refresh")
	assert.Equal(t, "jwt-acc", a)
	assert.Equal(t, "jwt-ref", r)
}

func TestReadStdinTrimmed(t *testing.T) {
	out, err := readStdinTrimmed(strings.NewReader("  hello  \n"))
	require.NoError(t, err)
	assert.Equal(t, "hello", out)

	out, err = readStdinTrimmed(nil)
	require.NoError(t, err)
	assert.Equal(t, "", out)
}
