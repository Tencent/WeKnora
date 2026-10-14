package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrWeChatSettingsConflict prevents stale administrator forms overwriting credentials.
var ErrWeChatSettingsConflict = errors.New("WeChat settings changed; reload before saving")

const weChatSettingsLockKey = int64(1464550209)

// Serialize configuration changes with identity provisioning, including the
// first override where there is no singleton row to lock yet. SQLite's no-op
// UPDATE takes the writer lock; PostgreSQL uses a transaction-scoped advisory
// lock. Logins take it shared so they exclude configuration changes but not
// each other; only SaveSettings takes it exclusively.
func lockWeChatSettings(tx *gorm.DB, exclusive bool) error {
	if tx.Name() == "postgres" {
		if exclusive {
			return tx.Exec("SELECT pg_advisory_xact_lock(?)", weChatSettingsLockKey).Error
		}
		return tx.Exec("SELECT pg_advisory_xact_lock_shared(?)", weChatSettingsLockKey).Error
	}
	return tx.Exec("UPDATE we_chat_auth_settings SET version = version WHERE id = 1").Error
}

// Uses the DB-over-startup configuration and write-only secret design from
// Tencent/WeKnora#4133. Unlike a general setting, this row is never returned raw.
// Read/decryption failures fail closed instead of reviving old environment values.
func (s *WeChatAuthService) loadWeChatSettings(
	ctx context.Context,
) (*config.WeChatAuthConfig, *types.WeChatAuthSetting, error) {
	cfg := &config.WeChatAuthConfig{}
	if s.cfg != nil && s.cfg.WeChatAuth != nil {
		*cfg = *s.cfg.WeChatAuth
	}
	var row types.WeChatAuthSetting
	err := s.db.WithContext(ctx).First(&row, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cfg, nil, nil
	}
	if err != nil {
		return nil, nil, ErrWeChatUnavailable
	}
	// Keep ciphertext here. The admin read path only needs presence, allowing
	// recovery after a lost encryption key without disclosing either form.
	cfg = &config.WeChatAuthConfig{
		PlatformID: row.PlatformID,
		Web: config.WeChatAuthAppConfig{
			Enabled:     row.WebEnabled,
			AppID:       row.WebAppID,
			AppSecret:   row.WebAppSecret,
			CallbackURL: row.WebCallbackURL,
		},
		Mobile: config.WeChatAuthAppConfig{
			Enabled:   row.MobileEnabled,
			AppID:     row.MobileAppID,
			AppSecret: row.MobileAppSecret,
		},
	}
	return cfg, &row, nil
}

func (s *WeChatAuthService) resolveWeChatSettings(ctx context.Context) (*config.WeChatAuthConfig, error) {
	cfg, row, err := s.loadWeChatSettings(ctx)
	if err != nil {
		return nil, err
	}
	if row != nil {
		for _, app := range []*config.WeChatAuthAppConfig{&cfg.Web, &cfg.Mobile} {
			if !app.Enabled {
				continue
			}
			app.AppSecret, err = secutils.DecryptStoredSecret(app.AppSecret)
			if err != nil {
				return nil, ErrWeChatUnavailable
			}
		}
	}
	return cfg, nil
}

// GetSettings returns configuration metadata without secrets.
func (s *WeChatAuthService) GetSettings(ctx context.Context) (*types.WeChatSettingsResponse, error) {
	cfg, row, err := s.loadWeChatSettings(ctx)
	if err != nil {
		return nil, err
	}
	response := &types.WeChatSettingsResponse{
		Source: "deployment", PlatformID: cfg.PlatformID, EncryptionReady: secutils.GetAESKey() != nil,
		Web: types.WeChatAppSettings{
			Enabled:          cfg.Web.Enabled,
			AppID:            cfg.Web.AppID,
			CallbackURL:      cfg.Web.CallbackURL,
			SecretConfigured: cfg.Web.AppSecret != "",
		},
		Mobile: types.WeChatAppSettings{
			Enabled:          cfg.Mobile.Enabled,
			AppID:            cfg.Mobile.AppID,
			SecretConfigured: cfg.Mobile.AppSecret != "",
		},
	}
	if row != nil {
		response.Source = "database"
		response.Version = row.Version
	}
	return response, nil
}

// SaveSettings validates and atomically replaces the runtime configuration.
func (s *WeChatAuthService) SaveSettings(
	ctx context.Context,
	req *types.WeChatSettingsUpdate,
) (*types.WeChatSettingsResponse, error) {
	var response *types.WeChatSettingsResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockWeChatSettings(tx, true); err != nil {
			return ErrWeChatUnavailable
		}
		scoped := *s
		scoped.db = tx
		var err error
		response, err = scoped.saveSettings(ctx, req)
		return err
	})
	return response, err
}

func (s *WeChatAuthService) saveSettings(
	ctx context.Context,
	req *types.WeChatSettingsUpdate,
) (*types.WeChatSettingsResponse, error) {
	if req == nil || req.Version == nil || req.Web == nil || req.Mobile == nil {
		return nil, fmt.Errorf("complete settings and version are required")
	}
	old, row, err := s.loadWeChatSettings(ctx)
	if err != nil {
		return nil, err
	}
	version := uint64(0)
	if row != nil {
		version = row.Version
	}
	if version != *req.Version {
		return nil, ErrWeChatSettingsConflict
	}
	cfg := &config.WeChatAuthConfig{PlatformID: strings.TrimSpace(req.PlatformID)}
	for _, pair := range []struct {
		input *types.WeChatAppSettingsUpdate
		old   config.WeChatAuthAppConfig
		dest  *config.WeChatAuthAppConfig
	}{
		{req.Web, old.Web, &cfg.Web}, {req.Mobile, old.Mobile, &cfg.Mobile},
	} {
		app := pair.dest
		app.Enabled, app.AppID, app.CallbackURL = pair.input.Enabled, strings.TrimSpace(
			pair.input.AppID,
		), strings.TrimSpace(
			pair.input.CallbackURL,
		)
		if len(app.AppID) > 128 || len(app.CallbackURL) > 2048 {
			return nil, fmt.Errorf("AppID or callback URL is too long")
		}
		if pair.input.AppSecret == nil {
			if app.AppID != pair.old.AppID && pair.old.AppSecret != "" {
				return nil, fmt.Errorf("changing AppID requires replacing or clearing its AppSecret")
			}
			app.AppSecret = pair.old.AppSecret
			if row != nil {
				app.AppSecret, err = secutils.DecryptStoredSecret(app.AppSecret)
				if err != nil {
					return nil, fmt.Errorf("stored secret cannot be decrypted; replace or clear it")
				}
			}
		} else {
			app.AppSecret = strings.TrimSpace(*pair.input.AppSecret)
		}
		if len(app.AppSecret) > 512 || strings.HasPrefix(app.AppSecret, secutils.EncPrefix) {
			return nil, fmt.Errorf("invalid AppSecret")
		}
	}
	if err := config.ValidateWeChatConfig(cfg); err != nil {
		return nil, err
	}
	if cfg.Mobile.CallbackURL != "" {
		return nil, fmt.Errorf("mobile callback is configured in OpenSDK, not the backend")
	}
	// OpenID identities are namespaced by AppID. Replacing an AppID that
	// already has linked users would provision duplicate accounts for them.
	for _, pair := range [][2]string{{old.Web.AppID, cfg.Web.AppID}, {old.Mobile.AppID, cfg.Mobile.AppID}} {
		if pair[0] == "" || pair[0] == pair[1] {
			continue
		}
		var count int64
		if err := s.db.WithContext(ctx).Model(&types.ExternalIdentity{}).
			Where("provider = ? AND namespace = ?", "wechat_openid", pair[0]).
			Count(&count).Error; err != nil {
			return nil, ErrWeChatUnavailable
		}
		if count > 0 {
			return nil, fmt.Errorf("AppID cannot change after WeChat users have signed in with it")
		}
	}
	if cfg.PlatformID != old.PlatformID {
		var count int64
		if err := s.db.WithContext(ctx).Model(&types.ExternalIdentity{}).Where("provider = ?", "wechat_unionid").
			Count(&count).Error; err != nil {
			return nil, ErrWeChatUnavailable
		}
		if count > 0 {
			return nil, fmt.Errorf("platform namespace cannot change after UnionID identities exist")
		}
	}
	for _, app := range []*config.WeChatAuthAppConfig{&cfg.Web, &cfg.Mobile} {
		if app.AppSecret == "" {
			continue
		}
		if secutils.GetAESKey() == nil {
			return nil, fmt.Errorf("configure a 32-byte SYSTEM_AES_KEY before storing credentials")
		}
		app.AppSecret, err = secutils.EncryptAESGCM(app.AppSecret, secutils.GetAESKey())
		if err != nil {
			return nil, ErrWeChatUnavailable
		}
	}
	actor, _ := types.UserIDFromContext(ctx)
	next := &types.WeChatAuthSetting{
		ID: 1, Version: version + 1, PlatformID: cfg.PlatformID,
		WebEnabled:      cfg.Web.Enabled,
		WebAppID:        cfg.Web.AppID,
		WebAppSecret:    cfg.Web.AppSecret,
		WebCallbackURL:  cfg.Web.CallbackURL,
		MobileEnabled:   cfg.Mobile.Enabled,
		MobileAppID:     cfg.Mobile.AppID,
		MobileAppSecret: cfg.Mobile.AppSecret,
		UpdatedBy:       actor,
		UpdatedAt:       time.Now().UTC(),
	}
	var result *gorm.DB
	if row == nil {
		result = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(next)
	} else {
		result = s.db.WithContext(ctx).Model(&types.WeChatAuthSetting{}).Where("id = ? AND version = ?", 1, version).
			Select("*").Updates(next)
	}
	if result.Error != nil {
		return nil, ErrWeChatUnavailable
	}
	if result.RowsAffected != 1 {
		return nil, ErrWeChatSettingsConflict
	}
	return s.GetSettings(ctx)
}
