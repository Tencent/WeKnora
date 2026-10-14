package service

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func weChatSettingsRequest(r *types.WeChatSettingsResponse) *types.WeChatSettingsUpdate {
	return &types.WeChatSettingsUpdate{
		Version: &r.Version, PlatformID: r.PlatformID,
		Web: &types.WeChatAppSettingsUpdate{
			Enabled:     r.Web.Enabled,
			AppID:       r.Web.AppID,
			CallbackURL: r.Web.CallbackURL,
		},
		Mobile: &types.WeChatAppSettingsUpdate{Enabled: r.Mobile.Enabled, AppID: r.Mobile.AppID},
	}
}

func TestWeChatSettingsSecretLifecycleAndPriority(t *testing.T) {
	s := newWeChatTestService(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	initial, err := s.GetSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, "deployment", initial.Source)
	require.True(t, initial.Web.SecretConfigured)
	req := weChatSettingsRequest(initial)
	saved, err := s.SaveSettings(ctx, req)
	require.NoError(t, err)
	require.Equal(t, "database", saved.Source)
	var row types.WeChatAuthSetting
	require.NoError(t, s.db.First(&row, 1).Error)
	require.True(t, strings.HasPrefix(row.WebAppSecret, secutils.EncPrefix))
	require.NotContains(t, row.WebAppSecret, "web-secret")
	for _, value := range []any{saved, row} {
		b, err := json.Marshal(value)
		require.NoError(t, err)
		for _, secret := range []string{"web-secret", "mobile-secret", row.WebAppSecret, row.MobileAppSecret} {
			require.NotContains(t, string(b), secret)
		}
	}
	_, err = s.SaveSettings(ctx, req)
	require.ErrorIs(t, err, ErrWeChatSettingsConflict)
	// Blank is an explicit clear, not a fallback to the original env secret.
	emptySecret := ""
	req = weChatSettingsRequest(saved)
	req.Web.AppSecret, req.Web.Enabled = &emptySecret, false
	saved, err = s.SaveSettings(ctx, req)
	require.NoError(t, err)
	require.False(t, saved.Web.SecretConfigured)
	web, mobile := s.Enabled(ctx)
	require.False(t, web)
	require.True(t, mobile)
	resolved, err := s.resolveWeChatSettings(ctx)
	require.NoError(t, err)
	require.Empty(t, resolved.Web.AppSecret)
	require.Equal(t, "mobile-secret", resolved.Mobile.AppSecret)
	replacement := "replacement-secret"
	req = weChatSettingsRequest(saved)
	req.Web.AppSecret, req.Web.Enabled = &replacement, true
	_, err = s.SaveSettings(ctx, req)
	require.NoError(t, err)
	resolved, err = s.resolveWeChatSettings(ctx)
	require.NoError(t, err)
	require.Equal(t, replacement, resolved.Web.AppSecret)
}

func TestWeChatSettingsRejectsInvalidAndMismatchedCredentials(t *testing.T) {
	s := newWeChatTestService(t)
	t.Setenv("SYSTEM_AES_KEY", "")
	initial, err := s.GetSettings(context.Background())
	require.NoError(t, err)
	_, err = s.SaveSettings(context.Background(), weChatSettingsRequest(initial))
	require.ErrorContains(t, err, "SYSTEM_AES_KEY")
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	for _, mutate := range []func(*types.WeChatSettingsUpdate){
		func(r *types.WeChatSettingsUpdate) { r.Web.AppID = "other-app" },
		func(r *types.WeChatSettingsUpdate) {
			r.Web.CallbackURL = "http://example.com/api/v1/auth/wechat/callback"
		},
		func(r *types.WeChatSettingsUpdate) { r.PlatformID = strings.Repeat("a", 129) },
		func(r *types.WeChatSettingsUpdate) { r.Mobile.CallbackURL = "https://example.com/" },
		func(r *types.WeChatSettingsUpdate) { secret := "enc:v1:injected"; r.Web.AppSecret = &secret },
	} {
		req := weChatSettingsRequest(initial)
		mutate(req)
		_, err := s.SaveSettings(context.Background(), req)
		require.Error(t, err)
	}
	var count int64
	require.NoError(t, s.db.Model(&types.WeChatAuthSetting{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestWeChatSettingsFailClosedAndRecoverLostKey(t *testing.T) {
	s := newWeChatTestService(t)
	ctx := context.Background()
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	initial, err := s.GetSettings(ctx)
	require.NoError(t, err)
	saved, err := s.SaveSettings(ctx, weChatSettingsRequest(initial))
	require.NoError(t, err)
	t.Setenv("SYSTEM_AES_KEY", "fedcba9876543210fedcba9876543210")
	_, err = s.CreateAttempt(ctx, "web", WeChatChallenge(strings.Repeat("v", 43)))
	require.ErrorIs(t, err, ErrWeChatUnavailable)
	metadata, err := s.GetSettings(ctx)
	require.NoError(t, err, "admin must be able to repair credentials")
	require.True(t, metadata.Web.SecretConfigured)
	_, err = s.SaveSettings(ctx, weChatSettingsRequest(saved))
	require.ErrorContains(t, err, "cannot be decrypted")
	req := weChatSettingsRequest(saved)
	web, mobile := "new-web", "new-mobile"
	req.Web.AppSecret, req.Mobile.AppSecret = &web, &mobile
	_, err = s.SaveSettings(ctx, req)
	require.NoError(t, err)
	_, err = s.CreateAttempt(ctx, "web", WeChatChallenge(strings.Repeat("v", 43)))
	require.NoError(t, err)
	require.NoError(t, s.db.Migrator().DropTable(&types.WeChatAuthSetting{}))
	_, err = s.CreateAttempt(ctx, "web", WeChatChallenge(strings.Repeat("v", 43)))
	require.ErrorIs(t, err, ErrWeChatUnavailable, "DB error must not restore deployment defaults")
}

func TestWeChatSettingsInvalidatesPendingAttemptsAndProtectsIdentityNamespace(t *testing.T) {
	s := newWeChatTestService(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	attempt := weChatTestAttempt(t, s, "web")
	initial, err := s.GetSettings(ctx)
	require.NoError(t, err)
	req := weChatSettingsRequest(initial)
	req.Web.CallbackURL = "https://new.example.com/api/v1/auth/wechat/callback"
	saved, err := s.SaveSettings(ctx, req)
	require.NoError(t, err)
	_, _, err = s.Exchange(ctx, "web", attempt, true, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatAttempt)
	response, _, err := s.Exchange(ctx, "web", weChatTestAttempt(t, s, "web"), true, types.TenantProvisioningTenantless)
	require.NoError(t, err)
	require.Equal(t, "wechat", response.User.AuthSource)
	// Preferences/password updates cannot rewrite the creation source.
	response.User.AuthSource = "local"
	require.NoError(t, apprepo.NewUserRepository(s.db).UpdateUser(ctx, response.User))
	stored, err := apprepo.NewUserRepository(s.db).GetUserByID(ctx, response.User.ID)
	require.NoError(t, err)
	require.Equal(t, "wechat", stored.ToUserInfo().AuthSource)
	req = weChatSettingsRequest(saved)
	req.PlatformID = "another-platform"
	_, err = s.SaveSettings(ctx, req)
	require.ErrorContains(t, err, "namespace cannot change")
	// Replacing a used AppID would orphan its OpenID identities and silently
	// provision duplicate accounts for returning users.
	saved, err = s.GetSettings(ctx)
	require.NoError(t, err)
	req = weChatSettingsRequest(saved)
	replacement := "replacement-secret"
	req.Web.AppID, req.Web.AppSecret = "other-web-app", &replacement
	_, err = s.SaveSettings(ctx, req)
	require.ErrorContains(t, err, "AppID cannot change")
	req = weChatSettingsRequest(saved)
	req.Mobile.AppID, req.Mobile.AppSecret = "other-mobile-app", &replacement
	_, err = s.SaveSettings(ctx, req)
	require.NoError(t, err, "an AppID without linked users can still be replaced")
}

func TestWeChatSettingsChangeDuringProviderExchangeDoesNotProvision(t *testing.T) {
	s := newWeChatTestService(t)
	t.Setenv("SYSTEM_AES_KEY", "0123456789abcdef0123456789abcdef")
	ctx := context.Background()
	s.httpClient.Transport = weChatRoundTripper(func(_ *http.Request) (*http.Response, error) {
		initial, err := s.GetSettings(ctx)
		require.NoError(t, err)
		req := weChatSettingsRequest(initial)
		req.PlatformID = "changed-during-exchange"
		_, err = s.SaveSettings(ctx, req)
		require.NoError(t, err)
		return &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader(`{"openid":"user","unionid":"union","access_token":"token"}`)),
		}, nil
	})
	_, _, err := s.Exchange(ctx, "web", weChatTestAttempt(t, s, "web"), true, types.TenantProvisioningTenantless)
	require.ErrorIs(t, err, ErrWeChatAttempt)
	var users int64
	require.NoError(t, s.db.Model(&types.User{}).Count(&users).Error)
	require.Zero(t, users)
}

func TestWeChatSettingsAccountSourcesAreServerControlled(t *testing.T) {
	s := newWeChatTestService(t)
	users := apprepo.NewUserRepository(s.db)
	us := &userService{userRepo: users, config: s.cfg}
	var req types.RegisterRequest
	require.NoError(
		t,
		json.Unmarshal(
			[]byte(
				`{"username":"local","email":"local@example.com","password":"Known-password-123!",`+
					`"auth_source":"wechat"}`,
			),
			&req,
		),
	)
	require.Empty(t, req.AuthSource, "the public API must not accept a provider source")
	req.TenantProvisioning = types.TenantProvisioningTenantless
	local, err := us.Register(context.Background(), &req)
	require.NoError(t, err)
	require.Equal(t, "local", local.AuthSource)
	oidc, err := us.provisionOIDCUser(
		context.Background(),
		&types.OIDCUserInfo{Username: "oidc", Email: "oidc@example.com"},
		types.TenantProvisioningTenantless,
	)
	require.NoError(t, err)
	require.Equal(t, "oidc", oidc.AuthSource)
	stored, err := users.GetUserByID(context.Background(), oidc.ID)
	require.NoError(t, err)
	require.Equal(t, "oidc", stored.AuthSource)
}

func TestWeChatSettingsPostgresMigrationRoundtrip(t *testing.T) {
	if os.Getenv("WEKNORA_WECHAT_TEST_POSTGRES_DSN") == "" {
		t.Skip("set WEKNORA_WECHAT_TEST_POSTGRES_DSN for PostgreSQL migrations")
	}
	s := newWeChatTestService(t)
	down, err := os.ReadFile("../../../migrations/versioned/000120_wechat_auth.down.sql")
	require.NoError(t, err)
	require.NoError(t, s.db.Exec(string(down)).Error)
	require.False(t, s.db.Migrator().HasColumn("users", "auth_source"))
	require.False(t, s.db.Migrator().HasTable(&types.WeChatAuthSetting{}))
	require.NoError(
		t,
		s.db.Exec(
			`INSERT INTO users (id, username, email, password_hash, preferences)
VALUES ('legacy', 'legacy', 'legacy@example.com', 'hash', '{"oidc_only_login":true}')`,
		).Error,
	)
	up, err := os.ReadFile("../../../migrations/versioned/000120_wechat_auth.up.sql")
	require.NoError(t, err)
	require.NoError(t, s.db.Exec(string(up)).Error)
	var user types.User
	require.NoError(t, s.db.Where("id = ?", "legacy").First(&user).Error)
	require.Equal(t, "oidc", user.AuthSource)
}
