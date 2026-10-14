package types

import "time"

// WeChatAuthSetting is a singleton. Secret columns contain AES-GCM ciphertext
// and are never serialized; management responses use a separate DTO.
type WeChatAuthSetting struct {
	ID              uint      `gorm:"primaryKey" json:"-"`
	Version         uint64    `                  json:"-"`
	PlatformID      string    `                  json:"-"`
	WebEnabled      bool      `                  json:"-"`
	WebAppID        string    `                  json:"-"`
	WebAppSecret    string    `                  json:"-"`
	WebCallbackURL  string    `                  json:"-"`
	MobileEnabled   bool      `                  json:"-"`
	MobileAppID     string    `                  json:"-"`
	MobileAppSecret string    `                  json:"-"`
	UpdatedBy       string    `                  json:"-"`
	UpdatedAt       time.Time `                  json:"-"`
}

// WeChatAppSettings exposes nonsecret configuration for one application.
type WeChatAppSettings struct {
	Enabled          bool   `json:"enabled"`
	AppID            string `json:"app_id"`
	CallbackURL      string `json:"callback_url,omitempty"`
	SecretConfigured bool   `json:"secret_configured"`
}

// WeChatSettingsResponse describes effective configuration and its source.
type WeChatSettingsResponse struct {
	Source          string            `json:"source"`
	Version         uint64            `json:"version"`
	PlatformID      string            `json:"platform_id"`
	Web             WeChatAppSettings `json:"web"`
	Mobile          WeChatAppSettings `json:"mobile"`
	EncryptionReady bool              `json:"encryption_ready"`
}

// WeChatAppSettingsUpdate distinguishes secret preservation from explicit replacement.
type WeChatAppSettingsUpdate struct {
	Enabled     bool   `json:"enabled"`
	AppID       string `json:"app_id"`
	CallbackURL string `json:"callback_url"`
	// Omitted/null preserves the effective secret. Empty explicitly clears it.
	AppSecret *string `json:"app_secret"`
}

// WeChatSettingsUpdate replaces a complete configuration at a known version.
type WeChatSettingsUpdate struct {
	Version    *uint64                  `json:"version"     binding:"required"`
	PlatformID string                   `json:"platform_id"`
	Web        *WeChatAppSettingsUpdate `json:"web"         binding:"required"`
	Mobile     *WeChatAppSettingsUpdate `json:"mobile"      binding:"required"`
}
