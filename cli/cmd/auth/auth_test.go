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
	for _, name := range []string{"host", "name", "with-token", "json"} {
		assert.NotNilf(t, cmd.Flags().Lookup(name), "flag %s missing", name)
	}
	// `--context` should NOT be a local flag (it's the global persistent flag).
	// Local registration would silently shadow the global single-shot override.
	assert.Nil(t, cmd.Flags().Lookup("context"), "auth login must not declare a local --context flag (use --name)")
}

func TestNewCmdLogin_InvokesRunF(t *testing.T) {
	iostreams.SetForTest(t)
	called := false
	store := secrets.NewMemStore()
	f := &cmdutil.Factory{
		Secrets: func() (secrets.Store, error) { return store, nil },
	}
	cmd := NewCmdLogin(f, func(_ context.Context, opts *LoginOptions, _ *cmdutil.Factory, _ LoginService) error {
		called = true
		assert.Equal(t, "https://kb.example.com", opts.Host)
		assert.True(t, opts.WithToken)
		return nil
	})
	cmd.SetArgs([]string{"--host", "https://kb.example.com", "--with-token"})
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
	opts := &LoginOptions{
		Host:    "https://kb.example.com",
		Context: "ci",
		APIKey:  "sk-zzz",
	}
	require.NoError(t, persistAPIKey(opts, f))
	v, _ := store.Get("ci", "api_key")
	assert.Equal(t, "sk-zzz", v)
	cfg, _ := f.Config()
	assert.Equal(t, "ci", cfg.CurrentContext)
	assert.Equal(t, "https://kb.example.com", cfg.Contexts["ci"].Host)
	// APIKeyRef should be the mem:// URI from the store's Ref method.
	assert.Equal(t, "mem://ci/api_key", cfg.Contexts["ci"].APIKeyRef)
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
		Context: "p",
		JSONOut: true,
	}
	resp := &sdk.LoginResponse{
		Token:        "jwt-acc",
		RefreshToken: "jwt-ref",
		User:         &sdk.AuthUser{Email: "a@b.c", TenantID: 7},
	}
	require.NoError(t, persistJWT(opts, f, resp))
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
