package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

// WeChatAuthConfig separates website and native application credentials.
// PlatformID is an operator-defined, stable namespace for one Open Platform
// account. Configure it only when both applications belong to that account.
type WeChatAuthConfig struct {
	PlatformID string              `yaml:"platform_id" json:"platform_id"`
	Web        WeChatAuthAppConfig `yaml:"web"         json:"web"`
	Mobile     WeChatAuthAppConfig `yaml:"mobile"      json:"mobile"`
}

// WeChatAuthAppConfig holds one website or native application configuration.
type WeChatAuthAppConfig struct {
	Enabled     bool   `yaml:"enabled"      json:"enabled"`
	AppID       string `yaml:"app_id"       json:"app_id"`
	AppSecret   string `yaml:"app_secret"   json:"-"`
	CallbackURL string `yaml:"callback_url" json:"callback_url,omitempty"`
}

func applyWeChatEnvOverrides(cfg *Config) {
	if cfg.WeChatAuth == nil {
		cfg.WeChatAuth = &WeChatAuthConfig{}
	}
	if v, ok := os.LookupEnv("WECHAT_AUTH_PLATFORM_ID"); ok {
		cfg.WeChatAuth.PlatformID = strings.TrimSpace(v)
	}
	for name, app := range map[string]*WeChatAuthAppConfig{
		"WEB":    &cfg.WeChatAuth.Web,
		"MOBILE": &cfg.WeChatAuth.Mobile,
	} {
		prefix := "WECHAT_AUTH_" + name + "_"
		if v, ok := os.LookupEnv(prefix + "ENABLED"); ok {
			app.Enabled = strings.EqualFold(strings.TrimSpace(v), "true")
		}
		for suffix, dest := range map[string]*string{
			"APP_ID":       &app.AppID,
			"APP_SECRET":   &app.AppSecret,
			"CALLBACK_URL": &app.CallbackURL,
		} {
			if v, ok := os.LookupEnv(prefix + suffix); ok {
				*dest = strings.TrimSpace(v)
			}
		}
	}
}

// ValidateWeChatConfig validates both deployment and runtime settings.
func ValidateWeChatConfig(cfg *WeChatAuthConfig) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.PlatformID) > 128 {
		return fmt.Errorf("wechat_auth.platform_id must be at most 128 bytes")
	}
	for name, app := range map[string]WeChatAuthAppConfig{"web": cfg.Web, "mobile": cfg.Mobile} {
		if !app.Enabled {
			continue
		}
		if strings.TrimSpace(app.AppID) == "" || len(app.AppID) > 128 || strings.TrimSpace(app.AppSecret) == "" {
			return fmt.Errorf("wechat_auth.%s requires app_id and app_secret", name)
		}
		if name == "web" {
			u, err := url.Parse(app.CallbackURL)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" ||
				u.Fragment != "" ||
				u.Path != "/api/v1/auth/wechat/callback" {
				return fmt.Errorf(
					"wechat_auth.web.callback_url must be an HTTPS URL ending in " +
						"/api/v1/auth/wechat/callback without query or fragment",
				)
			}
		}
	}
	return nil
}
