package datasource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newOAuthManagerTest(t *testing.T, upstream *httptest.Server) (*DataSourceOAuthManager, *types.DataSource) {
	t.Helper()
	t.Setenv("EDITION", "lite")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	t.Setenv("ONEDRIVE_CLIENT_ID", "client")
	t.Setenv("ONEDRIVE_CLIENT_SECRET", "secret")
	t.Setenv("ONEDRIVE_TENANT", "common")
	t.Setenv("ONEDRIVE_REDIRECT_URL", "http://127.0.0.1/callback")
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}, &types.DataSourceOAuthToken{}, &types.DataSourceItem{}))
	ds := &types.DataSource{TenantID: 9, KnowledgeBaseID: "kb", Name: "OneDrive", Type: types.ConnectorTypeOneDrive, Status: types.DataSourceStatusPaused}
	require.NoError(t, db.Create(ds).Error)
	manager := NewDataSourceOAuthManager(repository.NewDataSourceOAuthRepository(db), repository.NewDataSourceRepository(db), nil)
	manager.loginBase = upstream.URL
	manager.graphBase = upstream.URL
	manager.httpClient = upstream.Client()
	return manager, ds
}

func TestDataSourceOAuthAuthorizationAndRefresh(t *testing.T) {
	accountID := "account-1"
	var refreshCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/common/oauth2/v2.0/token":
			require.NoError(t, r.ParseForm())
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				require.NotEmpty(t, r.Form.Get("code_verifier"))
				fmt.Fprint(w, `{"access_token":"access-1","refresh_token":"refresh-1","token_type":"Bearer","scope":"Files.Read offline_access","expires_in":1}`)
			case "refresh_token":
				refreshCalls.Add(1)
				require.Equal(t, "refresh-1", r.Form.Get("refresh_token"))
				fmt.Fprint(w, `{"access_token":"access-2","refresh_token":"refresh-2","token_type":"Bearer","scope":"Files.Read offline_access","expires_in":3600}`)
			default:
				http.Error(w, "bad grant", http.StatusBadRequest)
			}
		case "/me/drive":
			require.Equal(t, "Bearer access-1", r.Header.Get("Authorization"))
			fmt.Fprintf(w, `{"id":"drive-1","owner":{"user":{"id":%q,"displayName":"Alice"}}}`, accountID)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	manager, ds := newOAuthManagerTest(t, upstream)

	authorizeURL, err := manager.AuthorizeURL(context.Background(), ds.TenantID, ds.ID, "admin", false)
	require.NoError(t, err)
	parsed, err := url.Parse(authorizeURL)
	require.NoError(t, err)
	require.Equal(t, "S256", parsed.Query().Get("code_challenge_method"))
	require.Equal(t, oneDriveScopes, parsed.Query().Get("scope"))
	state := parsed.Query().Get("state")
	require.NotEmpty(t, state)

	status, err := manager.CompleteAuthorization(context.Background(), state, "code", "")
	require.NoError(t, err)
	require.True(t, status.Authorized)
	require.Equal(t, "drive-1", status.AuthorizedDriveID)

	_, err = manager.CompleteAuthorization(context.Background(), state, "code", "")
	require.ErrorContains(t, err, "not found or expired")

	accesses := make([]string, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range accesses {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			accesses[index], errs[index] = manager.AccessToken(
				context.Background(), ds.TenantID, ds.ID, ds.ConnectionVersion,
			)
		}(i)
	}
	wg.Wait()
	for i := range accesses {
		require.NoError(t, errs[i])
		require.Equal(t, "access-2", accesses[i])
	}
	require.Equal(t, int32(1), refreshCalls.Load())
}

func TestDataSourceOAuthRejectsDifferentAccountWithoutReplacement(t *testing.T) {
	account := "first"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/token") {
			fmt.Fprint(w, `{"access_token":"access","refresh_token":"refresh","expires_in":3600}`)
			return
		}
		if r.URL.Path == "/me/drive" {
			fmt.Fprintf(w, `{"id":"drive","owner":{"user":{"id":%q}}}`, account)
			return
		}
		http.NotFound(w, r)
	}))
	defer upstream.Close()
	manager, ds := newOAuthManagerTest(t, upstream)

	complete := func() error {
		authorizeURL, err := manager.AuthorizeURL(context.Background(), ds.TenantID, ds.ID, "admin", false)
		if err != nil {
			return err
		}
		parsed, _ := url.Parse(authorizeURL)
		_, err = manager.CompleteAuthorization(context.Background(), parsed.Query().Get("state"), "code", "")
		return err
	}
	require.NoError(t, complete())
	account = "second"
	require.ErrorIs(t, complete(), ErrOAuthAccountMismatch)

	replaceURL, err := manager.AuthorizeURL(context.Background(), ds.TenantID, ds.ID, "admin", true)
	require.NoError(t, err)
	parsed, err := url.Parse(replaceURL)
	require.NoError(t, err)
	status, err := manager.CompleteAuthorization(context.Background(), parsed.Query().Get("state"), "code", "")
	require.NoError(t, err)
	require.True(t, status.ReplacedConnection)
	require.Equal(t, uint64(2), status.ConnectionVersion)
}

func TestDataSourceOAuthRequiresSharedStateOutsideLite(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	manager, ds := newOAuthManagerTest(t, upstream)
	t.Setenv("EDITION", "standard")
	_, err := manager.AuthorizeURL(context.Background(), ds.TenantID, ds.ID, "admin", false)
	require.ErrorContains(t, err, "requires Redis")
}

func TestDataSourceOAuthConfigurationFailsClosed(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	defer upstream.Close()
	manager, ds := newOAuthManagerTest(t, upstream)
	t.Setenv("SYSTEM_AES_KEY", "")
	_, err := manager.AuthorizeURL(context.Background(), ds.TenantID, ds.ID, "admin", false)
	require.ErrorContains(t, err, "SYSTEM_AES_KEY")
}

func TestOAuthRefreshSkewIsPositive(t *testing.T) {
	require.Greater(t, oauthRefreshSkew, time.Minute)
}

func TestOAuthInvalidGrantRequiresReauthorization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"interaction required"}`)
	}))
	defer upstream.Close()
	manager, ds := newOAuthManagerTest(t, upstream)
	require.NoError(t, manager.tokens.Save(context.Background(), &types.DataSourceOAuthToken{
		TenantID: ds.TenantID, DataSourceID: ds.ID, Provider: oneDriveProvider,
		AccessToken: "expired", RefreshToken: "refresh", ExpiresAt: time.Now().Add(-time.Hour),
		ProviderAccountID: "account", AuthorizedDriveID: "drive", AuthorizedByUserID: "admin",
		ConnectionVersion: ds.ConnectionVersion,
	}))
	_, err := manager.AccessToken(context.Background(), ds.TenantID, ds.ID, ds.ConnectionVersion)
	require.ErrorIs(t, err, ErrOAuthReauthorizationRequired)
}

func TestOAuthCallbackRejectsStaleConnectionVersionBeforeExchange(t *testing.T) {
	var tokenCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer upstream.Close()
	manager, ds := newOAuthManagerTest(t, upstream)
	authorizeURL, err := manager.AuthorizeURL(context.Background(), ds.TenantID, ds.ID, "admin", false)
	require.NoError(t, err)
	parsed, err := url.Parse(authorizeURL)
	require.NoError(t, err)
	require.NoError(t, manager.Revoke(context.Background(), ds.TenantID, ds.ID))
	_, err = manager.CompleteAuthorization(context.Background(), parsed.Query().Get("state"), "code", "")
	require.ErrorIs(t, err, ErrOAuthConnectionChanged)
	require.Zero(t, tokenCalls.Load())
}
