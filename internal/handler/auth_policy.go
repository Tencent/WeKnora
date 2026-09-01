package handler

import (
	"context"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	authComplexPasswordEnabledSettingKey = "auth.complex_password_enabled"
	authComplexPasswordEnabledEnvName    = "WEKNORA_AUTH_COMPLEX_PASSWORD_ENABLED"
)

// resolveAuthComplexPasswordEnabled is the shared policy resolver used handlers auth and system.
func resolveAuthComplexPasswordEnabled(
	ctx context.Context,
	cfg *config.Config,
	settings interfaces.SystemSettingService,
) bool {
	enabled := false
	if cfg != nil && cfg.Tenant != nil {
		enabled = cfg.Auth.ComplexPasswordEnabled
	}
	if settings == nil {
		return enabled
	}
	return settings.GetBool(
		ctx,
		authComplexPasswordEnabledSettingKey,
		authComplexPasswordEnabledEnvName,
		enabled,
	)
}
