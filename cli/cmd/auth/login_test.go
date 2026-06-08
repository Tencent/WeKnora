package auth

import (
	"context"
	"encoding/json"
	"errors"
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

// seedActiveProfile writes an active profile to the (XDG-isolated) config so
// runLogin can resolve host/name from it - `auth login` authenticates the
// already-existing active profile rather than creating one. Returns nothing;
// callers read back via f.Config() to assert merge behavior.
func seedActiveProfile(t *testing.T, name string, prof config.Profile) {
	t.Helper()
	cfg := &config.Config{
		CurrentProfile: name,
		Profiles:       map[string]config.Profile{name: prof},
	}
	require.NoError(t, config.Save(cfg))
}

func TestRunLogin_PasswordMode(t *testing.T) {
	iostreams.SetForTest(t)
	f, store := newTestFactoryWithConfig(t, scriptedPrompter{email: "a@b.c", password: "secret"})
	seedActiveProfile(t, "prod", config.Profile{Host: "https://kb.example.com"})
	svc := &fakeLoginService{resp: &sdk.LoginResponse{
		Success: true,
		Token:   "jwt-access",
		User:    &sdk.AuthUser{ID: "u1", Email: "a@b.c", TenantID: 7},
	}}
	require.NoError(t, runLogin(context.Background(), &LoginOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, svc))

	assert.Equal(t, "a@b.c", svc.got.email)
	assert.Equal(t, "secret", svc.got.password)

	got, _ := store.Get("prod", "access")
	assert.Equal(t, "jwt-access", got)

	cfg, _ := f.Config()
	assert.Equal(t, "https://kb.example.com", cfg.Profiles["prod"].Host, "host must be preserved from the seeded profile")
}

func TestRunLogin_WithToken(t *testing.T) {
	iostreams.SetForTest(t)
	f, store := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	seedActiveProfile(t, "ci", config.Profile{Host: "https://kb.example.com"})
	restore := stubAPIKeyValidator(func(_ context.Context, _, _ string) (*sdk.AuthUser, error) {
		return &sdk.AuthUser{ID: "u1", Email: "ci@example.com", TenantID: 7}, nil
	})
	defer restore()
	opts := &LoginOptions{
		WithToken:   true,
		StdinReader: strings.NewReader("  sk-1234  \n"),
	}
	require.NoError(t, runLogin(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil))
	got, _ := store.Get("ci", "api_key")
	assert.Equal(t, "sk-1234", got)
	cfg, _ := f.Config()
	assert.Equal(t, "ci@example.com", cfg.Profiles["ci"].User, "validator-returned user should be persisted")
	assert.Equal(t, uint64(7), cfg.Profiles["ci"].TenantID)
	assert.Equal(t, "https://kb.example.com", cfg.Profiles["ci"].Host, "host preserved from seeded profile")
}

func TestRunLogin_WithToken_JSONReportsAPIKeyMode(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	seedActiveProfile(t, "ci", config.Profile{Host: "https://kb.example.com"})
	restore := stubAPIKeyValidator(func(_ context.Context, _, _ string) (*sdk.AuthUser, error) {
		// API-key validation hits /auth/me, which DOES return a user. mode
		// must still be reported as api-key (derived from credential type,
		// not from "did the server return a user").
		return &sdk.AuthUser{ID: "u1", Email: "ci@example.com", TenantID: 7}, nil
	})
	defer restore()
	opts := &LoginOptions{WithToken: true, StdinReader: strings.NewReader("sk-1234")}
	require.NoError(t, runLogin(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, f, nil))

	var env struct {
		Data struct {
			Mode string `json:"mode"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env))
	assert.Equal(t, "api-key", env.Data.Mode, "api-key login must report mode=api-key, not bearer")
}

func TestRunLogin_WithToken_ServerRejects(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	seedActiveProfile(t, "ci", config.Profile{Host: "https://kb.example.com"})
	restore := stubAPIKeyValidator(func(_ context.Context, _, _ string) (*sdk.AuthUser, error) {
		// Use the SDK-format HTTP error message so ClassifyHTTPError detects
		// this as an HTTP 401, not a transport/network failure.
		return nil, errors.New("HTTP error 401: invalid api key")
	})
	defer restore()
	opts := &LoginOptions{
		WithToken:   true,
		StdinReader: strings.NewReader("sk-bad"),
	}
	err := runLogin(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthBadCredential, typed.Code,
		"server-side rejection must surface as auth.bad_credential, not persist the key")
}

// stubAPIKeyValidator swaps defaultAPIKeyValidator for the test and returns
// a restore func to defer.
func stubAPIKeyValidator(fn apiKeyValidator) func() {
	saved := defaultAPIKeyValidator
	defaultAPIKeyValidator = fn
	return func() { defaultAPIKeyValidator = saved }
}

func TestRunLogin_WithToken_Empty(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	seedActiveProfile(t, "ci", config.Profile{Host: "https://kb.example.com"})
	// Validator must NOT be called when stdin is empty - verify by setting
	// a panic-on-call sentinel.
	restore := stubAPIKeyValidator(func(_ context.Context, _, _ string) (*sdk.AuthUser, error) {
		t.Fatal("validator should not be called on empty stdin")
		return nil, nil
	})
	defer restore()
	opts := &LoginOptions{
		WithToken:   true,
		StdinReader: strings.NewReader(""),
	}
	err := runLogin(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.missing_flag")
}

func TestRunLogin_BadHost(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	// A non-http host stored on the active profile must be rejected by the
	// post-resolution validateHost check (config corruption guard).
	seedActiveProfile(t, "bad", config.Profile{Host: "ftp://nope"})
	err := runLogin(context.Background(), &LoginOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input.invalid_argument")
}

func TestRunLogin_LoginRefused(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, scriptedPrompter{email: "a@b.c", password: "x"})
	seedActiveProfile(t, "p", config.Profile{Host: "https://x"})
	svc := &fakeLoginService{resp: &sdk.LoginResponse{Success: false, Message: "bad password"}}
	err := runLogin(context.Background(), &LoginOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, svc)
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

// TestLogin_NoHostNoName asserts the --host / --name flags are gone: `auth
// login` now authenticates the active profile (create it via `profile add`).
func TestLogin_NoHostNoName(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	cmd := NewCmdLogin(f, nil)
	assert.Nil(t, cmd.Flags().Lookup("host"), "--host flag must be removed")
	assert.Nil(t, cmd.Flags().Lookup("name"), "--name flag must be removed")
	assert.NotNil(t, cmd.Flags().Lookup("with-token"), "--with-token must remain")
}

// TestLogin_NoActiveProfile_Errors asserts that with no active profile
// configured, login returns a clear auth.unauthenticated error pointing the
// user at `profile add`.
func TestLogin_NoActiveProfile_Errors(t *testing.T) {
	iostreams.SetForTest(t)
	f, _ := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	// No seedActiveProfile: empty config.
	err := runLogin(context.Background(), &LoginOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil)
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
	assert.Contains(t, err.Error(), "profile add")
}

// TestLogin_UsesActiveProfileHost asserts login persists credentials under the
// active profile using that profile's stored host, and that an existing User
// is NOT clobbered when the server (API-key validator) returns it unchanged.
func TestLogin_UsesActiveProfileHost(t *testing.T) {
	iostreams.SetForTest(t)
	f, store := newTestFactoryWithConfig(t, prompt.AgentPrompter{})
	// Pre-seed "prod" with a host AND a user that must survive re-login.
	seedActiveProfile(t, "prod", config.Profile{Host: "https://prod.example.com", User: "existing@owner.com"})
	restore := stubAPIKeyValidator(func(_ context.Context, host, _ string) (*sdk.AuthUser, error) {
		assert.Equal(t, "https://prod.example.com", host, "validator must be called with the active profile's host")
		// Server returns nil user (e.g. /auth/me gave no email) - must NOT
		// wipe the existing User.
		return &sdk.AuthUser{}, nil
	})
	defer restore()
	opts := &LoginOptions{WithToken: true, StdinReader: strings.NewReader("sk-prod")}
	require.NoError(t, runLogin(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, nil))

	got, _ := store.Get("prod", "api_key")
	assert.Equal(t, "sk-prod", got, "api key must be stored under the active profile")

	cfg, _ := f.Config()
	prof := cfg.Profiles["prod"]
	assert.Equal(t, "https://prod.example.com", prof.Host, "host must be preserved, not clobbered")
	assert.Equal(t, "existing@owner.com", prof.User, "existing User must NOT be wiped when server returns none")
	assert.NotEmpty(t, prof.APIKeyRef, "api-key ref must be set")
	assert.Equal(t, "prod", cfg.CurrentProfile, "active profile unchanged")
}
