package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWeChatConfiguration(t *testing.T) {
	cfg := &Config{}
	require.NoError(t, ValidateConfig(cfg))
	t.Setenv("WECHAT_AUTH_WEB_ENABLED", "true")
	t.Setenv("WECHAT_AUTH_WEB_APP_ID", "web-app")
	t.Setenv("WECHAT_AUTH_WEB_APP_SECRET", "sensitive-secret")
	t.Setenv("WECHAT_AUTH_WEB_CALLBACK_URL", "https://example.com/api/v1/auth/wechat/callback")
	t.Setenv("WECHAT_AUTH_PLATFORM_ID", "open-platform")
	applyWeChatEnvOverrides(cfg)
	require.NoError(t, ValidateConfig(cfg))
	require.Equal(t, "web-app", cfg.WeChatAuth.Web.AppID)
	require.Equal(t, "open-platform", cfg.WeChatAuth.PlatformID)
	b, err := json.Marshal(cfg.WeChatAuth)
	require.NoError(t, err)
	require.NotContains(t, string(b), "sensitive-secret")
	for _, callback := range []string{
		"http://example.com/api/v1/auth/wechat/callback",
		"https://example.com/other",
		"https://user:pass@example.com/api/v1/auth/wechat/callback",
		"https://example.com/api/v1/auth/wechat/callback?next=bad",
	} {
		cfg.WeChatAuth.Web.CallbackURL = callback
		require.Error(t, ValidateConfig(cfg), callback)
	}
	cfg.WeChatAuth.Web.Enabled = false
	cfg.WeChatAuth.Mobile.Enabled = true
	require.Error(t, ValidateConfig(cfg))
}
