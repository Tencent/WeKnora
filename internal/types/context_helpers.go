package types

import (
	"context"
	"os"
	"strings"
)

// EnvLanguage returns the WEKNORA_LANGUAGE environment variable value, or empty string if unset.
func EnvLanguage() string {
	return strings.TrimSpace(os.Getenv("WEKNORA_LANGUAGE"))
}

// DefaultLanguage returns the configured default language locale.
// It reads the WEKNORA_LANGUAGE environment variable; if unset, falls back to "zh-CN".
func DefaultLanguage() string {
	if lang := EnvLanguage(); lang != "" {
		return lang
	}
	return "zh-CN"
}

// TenantIDFromContext extracts the tenant ID from ctx.
// Returns (0, false) when the key is absent or the value is not uint64.
func TenantIDFromContext(ctx context.Context) (uint64, bool) {
	v, ok := ctx.Value(TenantIDContextKey).(uint64)
	return v, ok
}

// MustTenantIDFromContext extracts the tenant ID from ctx, panicking if missing.
func MustTenantIDFromContext(ctx context.Context) uint64 {
	v, ok := TenantIDFromContext(ctx)
	if !ok {
		panic("types.TenantIDContextKey not set in context")
	}
	return v
}

// TenantInfoFromContext extracts the *Tenant from ctx.
func TenantInfoFromContext(ctx context.Context) (*Tenant, bool) {
	v, ok := ctx.Value(TenantInfoContextKey).(*Tenant)
	return v, ok && v != nil
}

// RequestIDFromContext extracts the request ID string from ctx.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(RequestIDContextKey).(string)
	return v, ok && v != ""
}

// UserIDFromContext extracts the user ID string from ctx.
func UserIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(UserIDContextKey).(string)
	return v, ok && v != ""
}

// IsSyntheticUserID reports whether id refers to the synthetic system
// user that the X-API-Key auth path attaches to each tenant
// (User.ID = "system-<tenantID>"). These users have no real human
// behind them and no tenant_members row, so RBAC ownership matching
// against them is never meaningful — service-layer code that records
// "the creator" should skip storing this ID and treat the resource as
// tenant-owned instead.
//
// Kept minimal on purpose: the prefix and "all digits afterwards"
// invariant comes from middleware/auth.go's User construction for the
// API-key branch. If that prefix changes, update both sides.
func IsSyntheticUserID(id string) bool {
	const prefix = "system-"
	if len(id) <= len(prefix) {
		return false
	}
	if id[:len(prefix)] != prefix {
		return false
	}
	for _, r := range id[len(prefix):] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// TenantRoleFromContext extracts the caller's TenantRole in the currently
// active tenant. Returns TenantRoleViewer when the key is absent so that
// callers fail closed (least privilege) if the auth middleware did not
// populate the role for some reason.
func TenantRoleFromContext(ctx context.Context) TenantRole {
	v, ok := ctx.Value(TenantRoleContextKey).(TenantRole)
	if !ok || !v.IsValid() {
		return TenantRoleViewer
	}
	return v
}

// IsSystemAdminFromContext extracts the system admin flag from ctx.
// Returns false (fail-closed) when the key is absent.
func IsSystemAdminFromContext(ctx context.Context) bool {
	v, ok := ctx.Value(SystemAdminContextKey).(bool)
	if !ok {
		return false
	}
	return v
}

// SessionTenantIDFromContext extracts the session-owner tenant ID from ctx.
// Falls back to TenantIDFromContext when the session key is absent.
func SessionTenantIDFromContext(ctx context.Context) (uint64, bool) {
	v, ok := ctx.Value(SessionTenantIDContextKey).(uint64)
	if ok && v != 0 {
		return v, true
	}
	return TenantIDFromContext(ctx)
}

// LanguageFromContext extracts the language locale string from ctx (e.g. "zh-CN", "en-US").
// Returns ("zh-CN", false) when the key is absent.
func LanguageFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(LanguageContextKey).(string)
	return v, ok && v != ""
}

// LanguageNameFromContext returns the human-readable language name for use in prompts.
// e.g. "zh-CN" -> "Chinese (Simplified)", "en-US" -> "English", "ko-KR" -> "Korean"
// Falls back to DefaultLanguage() (WEKNORA_LANGUAGE env, then "zh-CN").
func LanguageNameFromContext(ctx context.Context) string {
	lang, ok := LanguageFromContext(ctx)
	if !ok {
		lang = DefaultLanguage()
	}
	return LanguageLocaleName(lang)
}

// LanguageLocaleName maps a locale code to a human-readable language name for LLM prompts.
func LanguageLocaleName(locale string) string {
	switch locale {
	case "zh-CN", "zh", "zh-Hans":
		return "Chinese (Simplified)"
	case "zh-TW", "zh-HK", "zh-Hant":
		return "Chinese (Traditional)"
	case "en-US", "en", "en-GB":
		return "English"
	case "ko-KR", "ko":
		return "Korean"
	case "ja-JP", "ja":
		return "Japanese"
	case "ru-RU", "ru":
		return "Russian"
	case "fr-FR", "fr":
		return "French"
	case "de-DE", "de":
		return "German"
	case "es-ES", "es":
		return "Spanish"
	case "pt-BR", "pt":
		return "Portuguese"
	default:
		// For unknown locales, return the locale itself
		return locale
	}
}
