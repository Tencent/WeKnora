package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type weChatRoundTripper func(*http.Request) (*http.Response, error)

func (f weChatRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newWeChatTestService(t *testing.T) *WeChatAuthService {
	t.Helper()
	t.Setenv("STORAGE_TYPE", "local")
	dialector := sqlite.Open(":memory:")
	postgresDSN := os.Getenv("WEKNORA_WECHAT_TEST_POSTGRES_DSN")
	if postgresDSN != "" {
		admin, err := gorm.Open(postgres.Open(postgresDSN), &gorm.Config{})
		require.NoError(t, err)
		schema := "wechat_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			conn, err := admin.DB()
			require.NoError(t, err)
			_ = conn.Close()
		})
		u, err := url.Parse(postgresDSN)
		require.NoError(t, err)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dialector = postgres.Open(u.String())
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(
		t,
		db.AutoMigrate(
			&types.User{},
			&types.Tenant{},
			&types.StorageBackend{},
			&types.TenantMember{},
			&types.AuthToken{},
		),
	)
	if postgresDSN == "" {
		require.NoError(
			t,
			db.AutoMigrate(&types.ExternalIdentity{}, &types.WeChatLoginAttempt{}, &types.WeChatAuthSetting{}),
		)
	} else {
		require.NoError(t, db.Migrator().DropColumn(&types.User{}, "AuthSource"))
		migration, err := os.ReadFile("../../../migrations/versioned/000120_wechat_auth.up.sql")
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(migration)).Error)
	}
	cfg := &config.Config{
		WeChatAuth: &config.WeChatAuthConfig{
			PlatformID: "platform",
			Web: config.WeChatAuthAppConfig{
				Enabled:     true,
				AppID:       "web-app",
				AppSecret:   "web-secret",
				CallbackURL: "https://example.com/api/v1/auth/wechat/callback",
			},
			Mobile: config.WeChatAuthAppConfig{Enabled: true, AppID: "mobile-app", AppSecret: "mobile-secret"},
		},
	}
	s := NewWeChatAuthService(db, cfg, nil)
	s.httpClient = &http.Client{Transport: weChatRoundTripper(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "https", r.URL.Scheme)
		require.Equal(t, "api.weixin.qq.com", r.URL.Host)
		require.Equal(t, "/sns/oauth2/access_token", r.URL.Path)
		require.Equal(t, "authorization_code", r.URL.Query().Get("grant_type"))
		appID := r.URL.Query().Get("appid")
		expectedSecret := "web-secret"
		if appID == "mobile-app" {
			expectedSecret = "mobile-secret"
		}
		require.Equal(t, expectedSecret, r.URL.Query().Get("secret"))
		body, _ := json.Marshal(
			map[string]any{"openid": appID + "-openid", "unionid": "union", "access_token": "provider-secret"},
		)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     make(http.Header),
		}, nil
	})}
	return s
}

func weChatTestAttempt(t *testing.T, s *WeChatAuthService, client string) *types.WeChatExchangeRequest {
	t.Helper()
	verifier, err := WeChatRandomVerifier()
	require.NoError(t, err)
	a, err := s.CreateAttempt(context.Background(), client, WeChatChallenge(verifier))
	require.NoError(t, err)
	return &types.WeChatExchangeRequest{
		AttemptID:    a.AttemptID,
		State:        a.State,
		Code:         "one-time-code",
		CodeVerifier: verifier,
	}
}

func TestWeChatLoginProvisionRepeatAndCrossClient(t *testing.T) {
	s := newWeChatTestService(t)
	ctx := context.Background()
	first, isNew, err := s.Exchange(
		ctx,
		"web",
		weChatTestAttempt(t, s, "web"),
		true,
		types.TenantProvisioningCreatePersonal,
	)
	require.NoError(t, err)
	require.True(t, isNew)
	require.NotNil(t, first.ActiveTenant)
	require.NotNil(t, first.ActiveTenant.DefaultStorageBackendID)
	require.Len(t, first.Memberships, 1)
	require.Equal(t, types.TenantRoleOwner, first.Memberships[0].Role)
	require.False(t, first.User.IsSystemAdmin)
	require.False(t, first.User.CanAccessAllTenants)
	require.True(t, *first.User.Preferences.OidcOnlyLogin)
	for _, client := range []string{"web", "mobile"} {
		repeat, isNew, err := s.Exchange(
			ctx,
			client,
			weChatTestAttempt(t, s, client),
			false,
			types.TenantProvisioningCreatePersonal,
		)
		require.NoError(t, err)
		require.False(t, isNew)
		require.Equal(t, first.User.ID, repeat.User.ID)
		require.Equal(t, first.ActiveTenant.ID, repeat.ActiveTenant.ID)
	}
	for _, table := range []string{"users", "tenants", "tenant_members", "storage_backends"} {
		var count int64
		require.NoError(t, s.db.Table(table).Count(&count).Error)
		require.EqualValues(t, 1, count, table)
	}
	// Tokens enter the same validation / refresh path as password logins.
	us := &userService{userRepo: apprepo.NewUserRepository(s.db), tokenRepo: apprepo.NewAuthTokenRepository(s.db)}
	u, tenantID, err := us.ValidateToken(ctx, first.Token)
	require.NoError(t, err)
	require.Equal(t, first.User.ID, u.ID)
	require.Equal(t, first.ActiveTenant.ID, tenantID)
}

func TestWeChatAttemptBindingAndReplay(t *testing.T) {
	for _, mutation := range []string{"state", "verifier", "client", "expiry", "appid"} {
		t.Run(mutation, func(t *testing.T) {
			s := newWeChatTestService(t)
			var calls int
			s.httpClient.Transport = weChatRoundTripper(
				func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("should not call") },
			)
			req := weChatTestAttempt(t, s, "mobile")
			client := "mobile"
			switch mutation {
			case "state":
				req.State = "wrong"
			case "verifier":
				req.CodeVerifier = strings.Repeat("x", 43)
			case "client":
				client = "web"
			case "expiry":
				require.NoError(
					t,
					s.db.Model(&types.WeChatLoginAttempt{}).
						Where("id = ?", req.AttemptID).
						Update("expires_at", time.Now().UTC().Add(-time.Minute)).
						Error,
				)
			case "appid":
				s.cfg.WeChatAuth.Mobile.AppID = "changed-app"
			}
			_, _, err := s.Exchange(context.Background(), client, req, true, types.TenantProvisioningTenantless)
			require.ErrorIs(t, err, ErrWeChatAttempt)
			require.Zero(t, calls)
		})
	}
	s := newWeChatTestService(t)
	req := weChatTestAttempt(t, s, "mobile")
	_, _, err := s.Exchange(context.Background(), "mobile", req, true, types.TenantProvisioningTenantless)
	require.NoError(t, err)
	_, _, err = s.Exchange(context.Background(), "mobile", req, true, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatAttempt)
}

func TestWeChatConcurrentAttemptConsumedOnce(t *testing.T) {
	s := newWeChatTestService(t)
	req := weChatTestAttempt(t, s, "mobile")
	var calls atomic.Int32
	s.httpClient.Transport = weChatRoundTripper(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("upstream unavailable")
	})
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = s.Exchange(context.Background(), "mobile", req, true, types.TenantProvisioningTenantless)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}

func TestWeChatRegistrationAndDisabledAccount(t *testing.T) {
	s := newWeChatTestService(t)
	ctx := context.Background()
	_, _, err := s.Exchange(ctx, "mobile", weChatTestAttempt(t, s, "mobile"), false, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatRegistration)
	first, _, err := s.Exchange(
		ctx,
		"mobile",
		weChatTestAttempt(t, s, "mobile"),
		true,
		types.TenantProvisioningTenantless,
	)
	require.NoError(t, err)
	require.Nil(t, first.ActiveTenant)
	require.Empty(t, first.Memberships)
	require.NoError(t, s.db.Model(&types.User{}).Where("id = ?", first.User.ID).Update("is_active", false).Error)
	_, _, err = s.Exchange(ctx, "mobile", weChatTestAttempt(t, s, "mobile"), true, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatAccount)
	require.NoError(t, s.db.Delete(&types.User{}, "id = ?", first.User.ID).Error)
	_, _, err = s.Exchange(ctx, "mobile", weChatTestAttempt(t, s, "mobile"), true, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatAccount)
}

func TestWeChatProvisioningRollsBackOnTokenFailure(t *testing.T) {
	s := newWeChatTestService(t)
	require.NoError(
		t,
		s.db.Exec(
			`CREATE TRIGGER fail_wechat_token BEFORE INSERT ON auth_tokens
BEGIN SELECT RAISE(ABORT, 'token failure'); END`,
		).Error,
	)
	_, _, err := s.Exchange(
		context.Background(),
		"web",
		weChatTestAttempt(t, s, "web"),
		true,
		types.TenantProvisioningCreatePersonal,
	)
	require.Error(t, err)
	for _, table := range []string{
		"users",
		"tenants",
		"tenant_members",
		"storage_backends",
		"external_identities",
		"auth_tokens",
	} {
		var count int64
		require.NoError(t, s.db.Table(table).Count(&count).Error)
		require.Zero(t, count, table)
	}
}

func TestWeChatMissingUnionAndConflictingAccounts(t *testing.T) {
	s := newWeChatTestService(t)
	ctx := context.Background()
	transport := s.httpClient.Transport
	s.httpClient.Transport = weChatRoundTripper(func(r *http.Request) (*http.Response, error) {
		resp, err := transport.RoundTrip(r)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(
				strings.NewReader(strings.ReplaceAll(string(b), `"unionid":"union"`, `"unionid":""`)),
			)
		}
		return resp, err
	})
	a, _, err := s.Exchange(ctx, "web", weChatTestAttempt(t, s, "web"), true, types.TenantProvisioningTenantless)
	require.NoError(t, err)
	b, _, err := s.Exchange(ctx, "mobile", weChatTestAttempt(t, s, "mobile"), true, types.TenantProvisioningTenantless)
	require.NoError(t, err)
	require.NotEqual(t, a.User.ID, b.User.ID)
	s.httpClient.Transport = transport
	_, _, err = s.Exchange(ctx, "web", weChatTestAttempt(t, s, "web"), false, types.TenantProvisioningTenantless)
	require.NoError(t, err)
	_, _, err = s.Exchange(ctx, "mobile", weChatTestAttempt(t, s, "mobile"), false, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatAccount)
}

func TestWeChatProviderFailuresAreRedacted(t *testing.T) {
	for _, body := range []string{
		`{"errcode":40029,"errmsg":"secret-code"}`,
		`{}`,
		`{"access_token":"secret"}`,
		`invalid-json`,
	} {
		t.Run(body, func(t *testing.T) {
			s := newWeChatTestService(t)
			s.httpClient.Transport = weChatRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			_, _, err := s.Exchange(
				context.Background(),
				"mobile",
				weChatTestAttempt(t, s, "mobile"),
				true,
				types.TenantProvisioningTenantless,
			)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
		})
	}
	s := newWeChatTestService(t)
	s.httpClient.Transport = weChatRoundTripper(
		func(*http.Request) (*http.Response, error) { return nil, errors.New("mobile-secret") },
	)
	_, _, err := s.Exchange(
		context.Background(),
		"mobile",
		weChatTestAttempt(t, s, "mobile"),
		true,
		types.TenantProvisioningTenantless,
	)
	require.ErrorIs(t, err, ErrWeChatUnavailable)
	require.NotContains(t, err.Error(), "secret")
}

func TestWeChatWebCompletionRequiresBrowserAndIsSingleUse(t *testing.T) {
	s := newWeChatTestService(t)
	ctx := context.Background()
	response, _, err := s.Exchange(ctx, "web", weChatTestAttempt(t, s, "web"), true, types.TenantProvisioningTenantless)
	require.NoError(t, err)
	verifier, err := WeChatRandomVerifier()
	require.NoError(t, err)
	ticket, err := s.StoreWebResult(ctx, response, verifier)
	require.NoError(t, err)
	_, err = s.CompleteWebResult(ctx, ticket, strings.Repeat("x", 43))
	require.ErrorIs(t, err, ErrWeChatAttempt)
	result, err := s.CompleteWebResult(ctx, ticket, verifier)
	require.NoError(t, err)
	require.Equal(t, response.Token, result.Token)
	require.Equal(t, response.RefreshToken, result.RefreshToken)
	_, err = s.CompleteWebResult(ctx, ticket, verifier)
	require.ErrorIs(t, err, ErrWeChatAttempt)

	ticket, err = s.StoreWebResult(ctx, response, verifier)
	require.NoError(t, err)
	require.NoError(
		t,
		s.db.Model(&types.WeChatLoginAttempt{}).
			Where("id = ?", ticket).
			Update("expires_at", time.Now().UTC().Add(-time.Minute)).
			Error,
	)
	_, err = s.CompleteWebResult(ctx, ticket, verifier)
	require.ErrorIs(t, err, ErrWeChatAttempt)

	ticket, err = s.StoreWebResult(ctx, response, verifier)
	require.NoError(t, err)
	require.NoError(t, apprepo.NewAuthTokenRepository(s.db).RevokeTokensByUserID(ctx, response.User.ID))
	_, err = s.CompleteWebResult(ctx, ticket, verifier)
	require.ErrorIs(t, err, ErrWeChatAccount)
}
