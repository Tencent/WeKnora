package auth

import (
	"context"
	"strings"
	"testing"

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

// fakeLoginService captures the email/password it received.
type fakeLoginService struct {
	resp *sdk.LoginResponse
	err  error
	got  struct{ email, password string }
}

func (f *fakeLoginService) Login(_ context.Context, req sdk.LoginRequest) (*sdk.LoginResponse, error) {
	f.got.email = req.Email
	f.got.password = req.Password
	return f.resp, f.err
}

// scriptedPrompter satisfies prompt.Prompter with predetermined values.
type scriptedPrompter struct{ email, password string }

func (s scriptedPrompter) Input(string, string) (string, error) { return s.email, nil }
func (s scriptedPrompter) Password(string) (string, error)      { return s.password, nil }
func (s scriptedPrompter) Confirm(string, bool) (bool, error)   { return true, nil }

func newTestFactoryWithConfig(t *testing.T, p prompt.Prompter) (*cmdutil.Factory, *secrets.MemStore) {
	t.Helper()
	testutil.XDGTempDir(t)
	store := secrets.NewMemStore()
	return &cmdutil.Factory{
		Config:   func() (*config.Config, error) { return config.Load() },
		Client:   func() (*sdk.Client, error) { panic("client") },
		Prompter: func() prompt.Prompter { return p },
		Secrets:  func() (secrets.Store, error) { return store, nil },
	}, store
}

func TestRunLogin_PasswordMode(t *testing.T) {
	iostreams.SetForTest(t)
	f, store := newTestFactoryWithConfig(t, scriptedPrompter{email: "a@b.c", password: "secret"})
	svc := &fakeLoginService{resp: &sdk.LoginResponse{
		Success: true,
		Token:   "jwt-access",
		User:    &sdk.AuthUser{ID: "u1", Email: "a@b.c", TenantID: 7},
	}}
	opts := &LoginOptions{
		Host:    "https://kb.example.com",
		Context: "prod",
	}
	require.NoError(t, runLogin(context.Background(), opts, nil, f, svc))

	assert.Equal(t, "a@b.c", svc.got.email)
	assert.Equal(t, "secret", svc.got.password)

	got, _ := store.Get("prod", "access")
	assert.Equal(t, "jwt-access", got)
}

func TestRunLogin_WithToken(t *testing.T) {
	iostreams.SetForTest(t)
	f, store := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	opts := &LoginOptions{
		Host:        "https://kb.example.com",
		Context:     "ci",
		WithToken:   true,
		StdinReader: strings.NewReader("  sk-1234  \n"),
	}
	require.NoError(t, runLogin(context.Background(), opts, nil, f, nil))
	got, _ := store.Get("ci", "api_key")
	assert.Equal(t, "sk-1234", got)
}

func TestRunLogin_WithToken_Empty(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	opts := &LoginOptions{
		Host:        "https://kb.example.com",
		Context:     "ci",
		WithToken:   true,
		StdinReader: strings.NewReader(""),
	}
	err := runLogin(context.Background(), opts, nil, f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.missing_flag")
}

func TestRunLogin_BadHost(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	err := runLogin(context.Background(), &LoginOptions{Host: "ftp://nope"}, nil, f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

func TestRunLogin_LoginRefused(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, scriptedPrompter{email: "a@b.c", password: "x"})
	svc := &fakeLoginService{resp: &sdk.LoginResponse{Success: false, Message: "bad password"}}
	err := runLogin(context.Background(), &LoginOptions{Host: "https://x", Context: "p"}, nil, f, svc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth.bad_credential")
}

func TestValidateHost(t *testing.T) {
	require.NoError(t, validateHost("https://kb.example.com"))
	require.NoError(t, validateHost("http://localhost:8080"))
	require.Error(t, validateHost(""))
	require.Error(t, validateHost("ftp://x"))
	require.Error(t, validateHost("not a url"))
}
