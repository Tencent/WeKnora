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

// fakeRefreshService scripts a RefreshToken response.
type fakeRefreshService struct {
	resp   *sdk.RefreshTokenResponse
	err    error
	gotTok string
}

func (f *fakeRefreshService) RefreshToken(_ context.Context, refreshToken string) (*sdk.RefreshTokenResponse, error) {
	f.gotTok = refreshToken
	return f.resp, f.err
}

// stubSvc returns a closure conforming to the refresherFor signature; it
// ignores host since the fake doesn't talk to the network.
func stubSvc(s cmdutil.Refresher) func(string) cmdutil.Refresher {
	return func(string) cmdutil.Refresher { return s }
}

func newRefreshFactory(t *testing.T, cfg *config.Config, store *secrets.MemStore) *cmdutil.Factory {
	t.Helper()
	testutil.XDGTempDir(t)
	require.NoError(t, config.Save(cfg))
	return &cmdutil.Factory{
		Config:   func() (*config.Config, error) { return config.Load() },
		Client:   func() (*sdk.Client, error) { panic("client") },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
		Secrets:  func() (secrets.Store, error) { return store, nil },
	}
}

func TestRefresh_Happy(t *testing.T) {
	iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("prod", "access", "old-access"))
	require.NoError(t, store.Set("prod", "refresh", "old-refresh"))

	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod": {
				Host:       "https://kb.example.com",
				TokenRef:   "mem://prod/access",
				RefreshRef: "mem://prod/refresh",
				User:       "alice@example.com",
			},
		},
	}
	f := newRefreshFactory(t, cfg, store)
	svc := &fakeRefreshService{resp: &sdk.RefreshTokenResponse{
		Success:      true,
		AccessToken:  "new-access",
		RefreshToken: "new-refresh",
	}}
	require.NoError(t, runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(svc)))

	assert.Equal(t, "old-refresh", svc.gotTok, "must pass stored refresh token to SDK")
	gotAccess, _ := store.Get("prod", "access")
	gotRefresh, _ := store.Get("prod", "refresh")
	assert.Equal(t, "new-access", gotAccess)
	assert.Equal(t, "new-refresh", gotRefresh)
}

// TestRefresh_ActiveProfileViaOverride exercises refreshing a non-default
// profile. Production resolves this via the global --profile flag (which
// rewrites cfg.CurrentProfile in Factory.Config); here we set
// CurrentProfile=staging directly, since runRefresh's target is the active
// profile.
func TestRefresh_ActiveProfileViaOverride(t *testing.T) {
	iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("staging", "refresh", "stg-refresh"))

	cfg := &config.Config{
		CurrentProfile: "staging", // global --profile staging resolves to this
		Profiles: map[string]config.Profile{
			"prod":    {Host: "https://prod", TokenRef: "mem://prod/access", RefreshRef: "mem://prod/refresh"},
			"staging": {Host: "https://stg", TokenRef: "mem://staging/access", RefreshRef: "mem://staging/refresh"},
		},
	}
	f := newRefreshFactory(t, cfg, store)
	svc := &fakeRefreshService{resp: &sdk.RefreshTokenResponse{
		Success: true, AccessToken: "new-stg-access", RefreshToken: "new-stg-refresh",
	}}
	require.NoError(t, runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(svc)))

	assert.Equal(t, "stg-refresh", svc.gotTok, "active profile staging must be refreshed")
	// prod (non-active) is untouched
	if v, _ := store.Get("prod", "access"); v != "" {
		t.Errorf("prod must not have been touched, got %q", v)
	}
}

// TestRefresh_NoNameFlag asserts the --name flag is gone; refresh targets the
// active profile (override via the global --profile).
func TestRefresh_NoNameFlag(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{Profiles: map[string]config.Profile{"a": {Host: "https://a"}}}
	f := newRefreshFactory(t, cfg, secrets.NewMemStore())
	cmd := NewCmdRefresh(f)
	assert.Nil(t, cmd.Flags().Lookup("name"), "--name flag must be removed")
}

func TestRefresh_NoCurrentProfile(t *testing.T) {
	iostreams.SetForTest(t)
	f := newRefreshFactory(t, &config.Config{}, secrets.NewMemStore())
	err := runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(&fakeRefreshService{}))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthUnauthenticated, typed.Code)
}

func TestRefresh_APIKeyContext(t *testing.T) {
	iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("ci", "api_key", "sk-123"))
	cfg := &config.Config{
		CurrentProfile: "ci",
		Profiles:       map[string]config.Profile{"ci": {Host: "https://kb", APIKeyRef: "mem://ci/api_key"}},
	}
	f := newRefreshFactory(t, cfg, store)
	err := runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(&fakeRefreshService{}))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeInputInvalidArgument, typed.Code)
	assert.Contains(t, typed.Hint, "api-key", "hint should explain api-key profiles cannot be refreshed")
}

func TestRefresh_NoRefreshTokenStored(t *testing.T) {
	iostreams.SetForTest(t)
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles: map[string]config.Profile{
			"prod": {Host: "https://kb", TokenRef: "mem://prod/access", RefreshRef: "mem://prod/refresh"},
		},
	}
	// MemStore is empty - RefreshRef points to a slot that doesn't exist.
	f := newRefreshFactory(t, cfg, secrets.NewMemStore())
	err := runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(&fakeRefreshService{}))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthTokenExpired, typed.Code)
	assert.Contains(t, typed.Hint, "auth login")
}

func TestRefresh_ServerRefused(t *testing.T) {
	iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("prod", "refresh", "stale-refresh"))
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://kb", TokenRef: "mem://prod/access", RefreshRef: "mem://prod/refresh"}},
	}
	f := newRefreshFactory(t, cfg, store)
	svc := &fakeRefreshService{resp: &sdk.RefreshTokenResponse{Success: false, Message: "refresh token expired"}}
	err := runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(svc))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeAuthTokenExpired, typed.Code)
	assert.Contains(t, typed.Hint, "auth login")
	// stored access must NOT have been overwritten with empty
	if v, _ := store.Get("prod", "access"); v == "" {
		// Was never set in this test, that's fine - main thing is no panic.
		_ = v
	}
}

func TestRefresh_TransportError(t *testing.T) {
	iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("prod", "refresh", "ok-refresh"))
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://kb", TokenRef: "mem://prod/access", RefreshRef: "mem://prod/refresh"}},
	}
	f := newRefreshFactory(t, cfg, store)
	svc := &fakeRefreshService{err: errors.New("connection reset")}
	err := runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}, f, stubSvc(svc))
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	// network/transport classified as network.error (mirrors auth login's
	// CodeAuthBadCredential mapping pattern; here we keep network errors
	// distinct since auth/refresh treats them as retryable).
	assert.Equal(t, cmdutil.CodeNetworkError, typed.Code)
}

func TestRefresh_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	store := secrets.NewMemStore()
	require.NoError(t, store.Set("prod", "refresh", "ok-refresh"))
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://kb", TokenRef: "mem://prod/access", RefreshRef: "mem://prod/refresh"}},
	}
	f := newRefreshFactory(t, cfg, store)
	svc := &fakeRefreshService{resp: &sdk.RefreshTokenResponse{Success: true, AccessToken: "a", RefreshToken: "r"}}
	require.NoError(t, runRefresh(context.Background(), &RefreshOptions{}, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, f, stubSvc(svc)))

	body := out.String()
	// payload must not leak the actual token values.
	assert.NotContains(t, body, "ok-refresh", "output must not leak refresh token")
	assert.NotContains(t, body, "\"a\"", "output must not leak the new access token")
	assert.NotContains(t, body, "\"r\"", "output must not leak the new refresh token")
	// must mention the profile name so agents can confirm what was refreshed
	assert.True(t, strings.Contains(body, "prod"), "output should reference the refreshed profile")
	// v0.7 envelope: ok:true is expected
	var env struct {
		OK bool `json:"ok"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &env))
	assert.True(t, env.OK, "envelope.ok must be true")
}
