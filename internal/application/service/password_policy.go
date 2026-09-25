package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	authComplexPasswordEnabledSettingKey = "auth.complex_password_enabled"
	authComplexPasswordEnabledEnvName    = "WEKNORA_AUTH_COMPLEX_PASSWORD_ENABLED"

	upperChars           = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	lowerChars           = "abcdefghijklmnopqrstuvwxyz"
	digitChars           = "0123456789"
	passwordSpecialChars = "!@#$%^&*()_+-=[]{}|;:,.<>?"
	allChars             = upperChars + lowerChars + digitChars + passwordSpecialChars
)

// ResolveComplexPasswordEnabled is the shared runtime resolver for the
// complex-password switch. Priority matches other auth tunables:
// DB system_settings > ENV > cfg.Auth (startup YAML/env) > false.
func ResolveComplexPasswordEnabled(
	ctx context.Context,
	cfg *config.Config,
	settings interfaces.SystemSettingService,
) bool {
	enabled := false
	if cfg != nil && cfg.Auth != nil {
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

// IsPasswordPolicyError reports whether err is a documented password-policy
// failure. Handlers map both the simple and complex sentinels to the same
// machine-readable details token so the SPA can localize them.
func IsPasswordPolicyError(err error) bool {
	return errors.Is(err, ErrPasswordPolicy) || errors.Is(err, ErrComplexPasswordPolicy)
}

// ValidatePasswordPolicy keeps registration, self-service rotation and
// administrative password resets aligned with the SPA form. Letter and
// digit classes are ASCII so they match the frontend regexes; special
// characters are the documented whitelist. Password bytes are never
// logged or included in the returned error.
func ValidatePasswordPolicy(password string, complexPasswordEnabled bool) error {
	length := utf8.RuneCountInString(password)
	if length < 8 || length > 32 {
		return ErrPasswordPolicy
	}

	var (
		hasUpper   bool
		hasLower   bool
		hasDigit   bool
		hasSpecial bool
	)

	for _, r := range password {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case strings.ContainsRune(passwordSpecialChars, r):
			hasSpecial = true
		}
	}

	if complexPasswordEnabled {
		if !hasUpper || !hasLower || !hasDigit || !hasSpecial {
			return ErrComplexPasswordPolicy
		}
		return nil
	}
	if (!hasUpper && !hasLower) || !hasDigit {
		return ErrPasswordPolicy
	}
	return nil
}
