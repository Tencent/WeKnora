package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeStatusService struct {
	resp *sdk.CurrentUserResponse
	err  error
}

func (f *fakeStatusService) GetCurrentUser(_ context.Context) (*sdk.CurrentUserResponse, error) {
	return f.resp, f.err
}

func newCurrentUserResponse(user *sdk.AuthUser, tenant *sdk.AuthTenant) *sdk.CurrentUserResponse {
	r := &sdk.CurrentUserResponse{Success: true}
	r.Data.User = user
	r.Data.Tenant = tenant
	return r
}

func TestRunStatus_HumanOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	testutil.XDGTempDir(t)
	require.NoError(t, config.Save(&config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {Host: "https://kb.example.com", TenantID: 7},
		},
	}))
	f := &cmdutil.Factory{
		Config:   func() (*config.Config, error) { return config.Load() },
		Prompter: func() prompt.Prompter { return prompt.AgentPrompter{} },
	}
	svc := &fakeStatusService{
		resp: newCurrentUserResponse(
			&sdk.AuthUser{ID: "u1", Email: "alice@example.com", TenantID: 7},
			&sdk.AuthTenant{ID: 7, Name: "Acme"},
		),
	}
	require.NoError(t, runStatus(context.Background(), nil, f, svc))
	got := out.String()
	assert.Contains(t, got, "context: prod")
	assert.Contains(t, got, "host:    https://kb.example.com")
	assert.Contains(t, got, "alice@example.com")
	assert.Contains(t, got, "Acme")
}

func TestRunStatus_JSONOutput(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	testutil.XDGTempDir(t)
	require.NoError(t, config.Save(&config.Config{
		CurrentContext: "prod",
		Contexts:       map[string]config.Context{"prod": {Host: "https://x"}},
	}))
	f := &cmdutil.Factory{Config: func() (*config.Config, error) { return config.Load() }}
	svc := &fakeStatusService{resp: newCurrentUserResponse(&sdk.AuthUser{ID: "u1", Email: "a@b.c", TenantID: 7}, nil)}
	require.NoError(t, runStatus(context.Background(), &cmdutil.JSONOptions{}, f, svc))
	got := out.String()
	assert.True(t, strings.HasPrefix(strings.TrimSpace(got), `{"context":"prod"`), "expected bare object, got: %q", got)
	assert.NotContains(t, got, `"ok":`)
	assert.Contains(t, got, `"email":"a@b.c"`)
}

func TestRunStatus_NoSDKClient(t *testing.T) {
	iostreams.SetForTest(t)
	err := runStatus(context.Background(), nil, &cmdutil.Factory{}, nil)
	require.Error(t, err)
	assert.True(t, cmdutil.IsAuthError(err))
}

func TestRunStatus_SDKError_Transport(t *testing.T) {
	iostreams.SetForTest(t)
	testutil.XDGTempDir(t)
	require.NoError(t, config.Save(&config.Config{CurrentContext: "p", Contexts: map[string]config.Context{"p": {Host: "https://x"}}}))
	f := &cmdutil.Factory{Config: func() (*config.Config, error) { return config.Load() }}
	err := runStatus(context.Background(), nil, f, &fakeStatusService{err: assert.AnError})
	require.Error(t, err)
	// Non-HTTP errors (DNS / TCP) are transport problems, not auth problems -
	// classify network.error so retry logic / exit code 7 / IsTransient apply.
	assert.True(t, cmdutil.IsTransient(err), "expected transient/network classification, got %v", err)
}

func TestRunStatus_SDKError_HTTP401(t *testing.T) {
	iostreams.SetForTest(t)
	testutil.XDGTempDir(t)
	require.NoError(t, config.Save(&config.Config{CurrentContext: "p", Contexts: map[string]config.Context{"p": {Host: "https://x"}}}))
	f := &cmdutil.Factory{Config: func() (*config.Config, error) { return config.Load() }}
	err := runStatus(context.Background(), nil, f, &fakeStatusService{err: errors.New("HTTP error 401: invalid token")})
	require.Error(t, err)
	assert.True(t, cmdutil.IsAuthError(err))
}
