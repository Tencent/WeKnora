package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// SystemSettingRepository is the storage layer for the platform-wide
// system_settings table. All methods are system-scoped — there is no
// tenant_id; rows are global to the deployment.
type SystemSettingRepository interface {
	// Get fetches a row by key. Returns (nil, nil) when the key is not
	// present — callers fall back to ENV / default at the service layer.
	Get(ctx context.Context, key string) (*types.SystemSetting, error)
	// List returns every row. Used by the management UI to render the
	// settings page; no pagination yet (the registry is small — single
	// digits in P1, expected to stay double-digits long-term).
	List(ctx context.Context) ([]*types.SystemSetting, error)
	// Upsert writes a row keyed by Key. Insert if missing, update if
	// present. Used by SystemSettingService.Update on every save.
	Upsert(ctx context.Context, s *types.SystemSetting) error
	// Delete removes the row by key. Returns (true, nil) if a row was
	// deleted, (false, nil) if no row matched (idempotent — service
	// layer treats this as a no-op for audit purposes). Real DB errors
	// surface as (_, err).
	Delete(ctx context.Context, key string) (bool, error)
}

// SystemSettingService exposes both the 3-tier resolver (used by
// production code paths that consume settings) and the management CRUD
// (used by the SystemAdmin UI).
//
// 3-tier resolver priority: DB > ENV > built-in default. The service
// owns the registry of legal keys; reading or writing an unknown key
// returns an error from Update / falls through to default for Get.
//
// P1 ships with no in-memory cache: every GetXxx hits the DB. The
// callers (file-upload size check, etc.) are not on a hot path so the
// extra ~1ms is negligible. P2 may add Redis pubsub + a TTL cache;
// the SubscribeRedis hook below is a placeholder for that.
type SystemSettingService interface {
	// GetInt returns the resolved int64 value for `key`.
	//
	// envName is the legacy environment-variable name to consult when
	// the DB row is absent ("" means the key has no ENV fallback).
	// def is the built-in default used when both DB and ENV miss.
	//
	// Errors at the DB layer degrade gracefully: the function logs a
	// warning and falls through to ENV / default rather than returning
	// an error to upstream business code (which would have to bubble
	// it through every caller — we'd rather mis-serve a request with
	// the default than 500 a file upload).
	GetInt(ctx context.Context, key string, envName string, def int64) int64
	GetString(ctx context.Context, key string, envName string, def string) string
	GetBool(ctx context.Context, key string, envName string, def bool) bool
	// GetStringList resolves a comma-separated list of strings. envName
	// is treated as a comma-separated string at the ENV level (mirrors
	// the legacy SSRF_WHITELIST format). The slice returned is always
	// non-nil so callers can iterate without a nil check.
	GetStringList(ctx context.Context, key string, envName string, def []string) []string

	// List, Get, Update are the management-CRUD surface called by the
	// SystemAdmin handlers (gated to user.is_system_admin = true at the
	// router layer). Update emits an audit log on success.
	List(ctx context.Context) ([]*types.SystemSetting, error)
	Get(ctx context.Context, key string) (*types.SystemSetting, error)
	// Update writes a new value for `key`, validating that:
	//   1. key is in the in-code registry (rejects 400 otherwise — UI
	//      cannot inject arbitrary keys),
	//   2. rawValue's Go type matches the registry's expected type
	//      (e.g. int64 / float64 for "int", string for "string", bool
	//      for "bool"),
	//   3. the actor is captured from ctx (UserIDFromContext) and
	//      written to last_modified_by + the audit log.
	// Returns the persisted row on success.
	Update(ctx context.Context, key string, rawValue any) (*types.SystemSetting, error)
	// Reset removes the DB override for `key` so the resolver falls
	// back to ENV / built-in default. Idempotent: deleting a key that
	// was never persisted returns nil. Emits an audit row on actual
	// deletions, invalidates the local cache, and publishes to peers.
	// Returns an error only when the DB write itself fails; callers
	// must NOT translate "no row" into a 404 — that's the success
	// path for an idempotent reset.
	Reset(ctx context.Context, key string) error

	// SubscribeRedis is a P2 hook: when implemented, it will subscribe
	// to a "weknora:system_settings:changed" channel and invalidate any
	// in-memory cache on receipt. P1 implementations may return a no-op
	// because there is no cache yet. Keeping the method in the interface
	// now means P2 can drop in a real implementation without changing
	// the container wiring.
	SubscribeRedis(ctx context.Context) error
}
