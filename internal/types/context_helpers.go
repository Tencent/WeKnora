package types

import "context"

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

// SessionTenantIDFromContext extracts the session-owner tenant ID from ctx.
// Falls back to TenantIDFromContext when the session key is absent.
func SessionTenantIDFromContext(ctx context.Context) (uint64, bool) {
	v, ok := ctx.Value(SessionTenantIDContextKey).(uint64)
	if ok && v != 0 {
		return v, true
	}
	return TenantIDFromContext(ctx)
}
