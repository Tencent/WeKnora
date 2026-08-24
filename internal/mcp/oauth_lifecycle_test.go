package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/require"
)

type lockedOAuthRepo struct {
	*fakeOAuthRepo
	mu sync.Mutex
}

func newLockedOAuthRepo() *lockedOAuthRepo {
	return &lockedOAuthRepo{fakeOAuthRepo: newFakeOAuthRepo()}
}

func cloneOAuthToken(token *types.MCPOAuthToken) *types.MCPOAuthToken {
	if token == nil {
		return nil
	}
	clone := *token
	if token.RefreshLeaseUntil != nil {
		leaseUntil := *token.RefreshLeaseUntil
		clone.RefreshLeaseUntil = &leaseUntil
	}
	return &clone
}

func (r *lockedOAuthRepo) GetTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) (*types.MCPOAuthToken, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneOAuthToken(r.tokens[fakeOAuthKey(tenantID, principal, serviceID)]), nil
}

func (r *lockedOAuthRepo) SaveTokenForPrincipal(_ context.Context, token *types.MCPOAuthToken) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	principal := types.Principal{Type: token.PrincipalType, ID: token.PrincipalID}.Normalize()
	token = cloneOAuthToken(token)
	token.UpdatedAt = time.Now()
	r.tokens[fakeOAuthKey(token.TenantID, principal, token.ServiceID)] = token
	return nil
}

func (r *lockedOAuthRepo) DeleteTokenForPrincipal(
	_ context.Context, tenantID uint64, principal types.Principal, serviceID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tokens, fakeOAuthKey(tenantID, principal, serviceID))
	return nil
}

func (r *lockedOAuthRepo) TryAcquireTokenRefreshLease(
	_ context.Context,
	tenantID uint64,
	principal types.Principal,
	serviceID, leaseID string,
	leaseUntil time.Time,
) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := r.tokens[fakeOAuthKey(tenantID, principal, serviceID)]
	if row == nil || (row.RefreshLeaseUntil != nil && row.RefreshLeaseUntil.After(time.Now())) {
		return false, nil
	}
	row.RefreshLeaseID = leaseID
	row.RefreshLeaseUntil = &leaseUntil
	row.UpdatedAt = time.Now()
	return true, nil
}

func (r *lockedOAuthRepo) ReleaseTokenRefreshLease(
	_ context.Context,
	tenantID uint64,
	principal types.Principal,
	serviceID, leaseID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := r.tokens[fakeOAuthKey(tenantID, principal, serviceID)]
	if row != nil && row.RefreshLeaseID == leaseID {
		row.RefreshLeaseID = ""
		row.RefreshLeaseUntil = nil
		row.UpdatedAt = time.Now()
	}
	return nil
}

func newOAuthLifecycleFixture(
	t *testing.T, tokenStatus int, tokenBody map[string]any,
) (*oauthRuntime, *lockedOAuthRepo, *atomic.Int32, func()) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/metadata":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                                "http://" + req.Host,
				"authorization_endpoint":                "http://" + req.Host + "/authorize",
				"token_endpoint":                        "http://" + req.Host + "/token",
				"response_types_supported":              []string{"code"},
				"token_endpoint_auth_methods_supported": []string{"none"},
			})
		case "/token":
			requests.Add(1)
			require.NoError(t, req.ParseForm())
			require.Equal(t, "refresh_token", req.Form.Get("grant_type"))
			require.Equal(t, "old-refresh", req.Form.Get("refresh_token"))
			w.WriteHeader(tokenStatus)
			_ = json.NewEncoder(w).Encode(tokenBody)
		default:
			http.NotFound(w, req)
		}
	}))

	repo := newLockedOAuthRepo()
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user-1"}
	row := &types.MCPOAuthToken{
		TenantID:      7,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		UserID:        principal.StorageID(),
		ServiceID:     "svc-1",
		AccessToken:   "old-access",
		RefreshToken:  "old-refresh",
		TokenType:     "Bearer",
		ExpiresAt:     time.Now().Add(-time.Minute),
		UpdatedAt:     time.Now().Add(-time.Hour),
	}
	repo.tokens[fakeOAuthKey(7, principal, "svc-1")] = row
	store := newDBTokenStore(repo, 7, principal, "svc-1")
	runtime := newOAuthRuntime(repo, 7, principal, "svc-1", server.URL, transport.OAuthConfig{
		ClientID:              "client-1",
		AuthServerMetadataURL: server.URL + "/metadata",
		TokenStore:            store,
		HTTPClient:            server.Client(),
	})
	return runtime, repo, &requests, server.Close
}

func TestOAuthRuntimeRefreshesExpiredToken(t *testing.T) {
	runtime, repo, requests, closeServer := newOAuthLifecycleFixture(t, http.StatusOK, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "rotated-refresh",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
	defer closeServer()

	require.NoError(t, runtime.ensureFresh(context.Background(), false, nil))
	require.EqualValues(t, 1, requests.Load())
	row, err := repo.GetTokenForPrincipal(context.Background(), 7, runtime.principal, "svc-1")
	require.NoError(t, err)
	require.Equal(t, "new-access", row.AccessToken)
	require.Equal(t, "rotated-refresh", row.RefreshToken)
	require.True(t, row.ExpiresAt.After(time.Now()))
}

func TestOAuthRuntimeDeletesPermanentlyInvalidRefreshToken(t *testing.T) {
	runtime, repo, requests, closeServer := newOAuthLifecycleFixture(t, http.StatusBadRequest, map[string]any{
		"error":             "invalid_grant",
		"error_description": "refresh token expired",
	})
	defer closeServer()

	err := runtime.ensureFresh(context.Background(), false, nil)
	var reauth *OAuthReauthorizationRequiredError
	require.ErrorAs(t, err, &reauth)
	require.EqualValues(t, 1, requests.Load())
	row, getErr := repo.GetTokenForPrincipal(context.Background(), 7, runtime.principal, "svc-1")
	require.NoError(t, getErr)
	require.Nil(t, row)
}

func TestOAuthRuntimePreservesTokenOnTemporaryRefreshFailure(t *testing.T) {
	runtime, repo, requests, closeServer := newOAuthLifecycleFixture(t, http.StatusServiceUnavailable, map[string]any{
		"error": "temporarily_unavailable",
	})
	defer closeServer()

	err := runtime.ensureFresh(context.Background(), false, nil)
	var temporary *OAuthRefreshTemporaryError
	require.ErrorAs(t, err, &temporary)
	require.EqualValues(t, 1, requests.Load())
	row, getErr := repo.GetTokenForPrincipal(context.Background(), 7, runtime.principal, "svc-1")
	require.NoError(t, getErr)
	require.NotNil(t, row)
	require.Equal(t, "old-refresh", row.RefreshToken)
}

func TestOAuthRuntimeSerializesRotatingRefreshToken(t *testing.T) {
	runtime, repo, requests, closeServer := newOAuthLifecycleFixture(t, http.StatusOK, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "rotated-refresh",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
	defer closeServer()

	const callers = 12
	start := make(chan struct{})
	errs := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs <- runtime.ensureFresh(context.Background(), false, nil)
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, requests.Load(), "a rotating refresh token must be consumed once")
	row, err := repo.GetTokenForPrincipal(context.Background(), 7, runtime.principal, "svc-1")
	require.NoError(t, err)
	require.Equal(t, "rotated-refresh", row.RefreshToken)
}

func TestOAuthCallRefreshesAndRetriesResource401Once(t *testing.T) {
	runtime, repo, requests, closeServer := newOAuthLifecycleFixture(t, http.StatusOK, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "rotated-refresh",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
	defer closeServer()
	row := repo.tokens[fakeOAuthKey(7, runtime.principal, "svc-1")]
	row.ExpiresAt = time.Now().Add(time.Hour)

	calls := 0
	result, err := oauthCall(context.Background(), &mcpGoClient{oauth: runtime}, func() (string, error) {
		calls++
		if calls == 1 {
			return "", &transport.OAuthAuthorizationRequiredError{Handler: runtime.handler}
		}
		return "ok", nil
	})
	require.NoError(t, err)
	require.Equal(t, "ok", result)
	require.Equal(t, 2, calls)
	require.EqualValues(t, 1, requests.Load())
}

func TestOAuthCallDoesNotRetryMoreThanOnce(t *testing.T) {
	runtime, repo, requests, closeServer := newOAuthLifecycleFixture(t, http.StatusOK, map[string]any{
		"access_token":  "new-access",
		"refresh_token": "rotated-refresh",
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
	defer closeServer()
	row := repo.tokens[fakeOAuthKey(7, runtime.principal, "svc-1")]
	row.ExpiresAt = time.Now().Add(time.Hour)

	calls := 0
	_, err := oauthCall(context.Background(), &mcpGoClient{oauth: runtime}, func() (string, error) {
		calls++
		return "", &transport.OAuthAuthorizationRequiredError{Handler: runtime.handler}
	})
	require.Error(t, err)
	require.Equal(t, 2, calls)
	require.EqualValues(t, 1, requests.Load())
}

func TestTokenStatusDoesNotTreatExpiredRowAsAuthorized(t *testing.T) {
	expired := &types.MCPOAuthToken{
		AccessToken:  "stale-access",
		RefreshToken: "refresh",
		ExpiresAt:    time.Now().Add(-time.Minute),
	}
	status := tokenStatus(expired, time.Now())
	require.False(t, status.Authorized)
	require.Equal(t, oauthStateRefreshable, status.State)
	require.True(t, status.RefreshAvailable)

	expired.RefreshToken = ""
	status = tokenStatus(expired, time.Now())
	require.False(t, status.Authorized)
	require.Equal(t, oauthStateReauthNeeded, status.State)
}

func TestOAuthRuntimeDoesNotExpireNonRefreshableTokenEarly(t *testing.T) {
	repo := newLockedOAuthRepo()
	principal := types.Principal{Type: types.PrincipalWebUser, ID: "user-1"}
	repo.tokens[fakeOAuthKey(7, principal, "svc-1")] = &types.MCPOAuthToken{
		TenantID:      7,
		PrincipalType: principal.Type,
		PrincipalID:   principal.ID,
		ServiceID:     "svc-1",
		AccessToken:   "access",
		ExpiresAt:     time.Now().Add(10 * time.Second),
	}
	runtime := &oauthRuntime{repo: repo, tenantID: 7, principal: principal, serviceID: "svc-1"}
	require.NoError(t, runtime.ensureFresh(context.Background(), false, nil))
	row, err := repo.GetTokenForPrincipal(context.Background(), 7, principal, "svc-1")
	require.NoError(t, err)
	require.NotNil(t, row)
}
