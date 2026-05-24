package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// pubsubChannelBase is the Redis channel base for system_settings change
// notifications. Mirrors the convention from approval/gate.go: optional
// suffix WEKNORA_REDIS_NAMESPACE so two deployments sharing one Redis
// instance don't cross-talk.
const pubsubChannelBase = "weknora:system_settings:changed"

// pubsubChannel resolves the effective channel name (with optional
// namespace suffix). Called both at publish time and inside the
// subscriber loop — keep it pure.
func pubsubChannel() string {
	if ns := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE")); ns != "" {
		return pubsubChannelBase + ":" + ns
	}
	return pubsubChannelBase
}

// changeMessage is the JSON payload published whenever a setting is
// updated. OriginID lets the publishing replica skip its own message
// (it already updated its local cache inline) — without it every
// publish would trigger a redundant DB roundtrip per replica.
type changeMessage struct {
	Key      string `json:"key"`
	OriginID string `json:"origin_id"`
}

// settingSpec is the in-code registry entry for a known system setting.
// The registry serves as the **only** authority on which keys are legal
// + what type they hold + what their ENV-fallback name is + what the
// built-in default is. Adding a new tunable is a matter of:
//   1. Adding an entry here.
//   2. (Optional) adding a SQL seed row in a new migration so the UI
//      shows the row even before any operator hits Update.
//   3. Replacing existing os.Getenv() reads with calls into the
//      service.
//
// Update rejects any key not in this registry — so the UI cannot inject
// arbitrary keys into the DB, even with an attacker-controlled body.
type settingSpec struct {
	// Type is one of "int" | "string" | "bool" | "string_list". Update
	// validates the payload's Go type against this; reads decode accordingly.
	Type string
	// EnvName is the legacy environment variable consulted when the DB
	// row is absent. Empty string means "no ENV fallback for this key"
	// (the caller passes the desired default explicitly via the GetXxx
	// def parameter — useful when the cfg already coerced it at startup).
	EnvName string
	// Default is the built-in fallback used when both DB and ENV miss.
	// Type must match the Type field (int → int64, string → string,
	// bool → bool, string_list → []string); the typed Get* methods cast
	// accordingly. Currently unused by the resolver (callers pass def
	// inline) but kept for future cfg-less callsites.
	Default any
	// Enum, when non-empty, restricts Update to values in this set.
	// Only meaningful for Type=="string". Other types ignore it.
	// Empty/nil means no restriction (free-form string).
	Enum []string
	// Category drives UI grouping. Stored on the row at first write;
	// the seed migration sets it explicitly so management UI can
	// render even before any Update.
	Category string
	// Description is shown in the UI under the key. Stored on the row
	// at first write (mirrors Category).
	Description string
}

// registry pins the set of legal keys. Expanding it is a deliberate,
// reviewable operation — the implicit contract is "every key here is
// safely runtime-tunable (no startup caching that would not honour
// the new value, no in-memory state bound at init time we cannot
// re-derive)".
var registry = map[string]settingSpec{
	"file.max_size_mb": {
		Type:    "int",
		EnvName: "MAX_FILE_SIZE_MB",
		Default: int64(50),
	},
	"ssrf.whitelist": {
		Type:    "string_list",
		EnvName: "SSRF_WHITELIST",
		Default: []string{},
		Category: "security",
		Description: "SSRF 防护白名单。可填入 example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1。" +
			"修改后立即生效。SSRF_WHITELIST_EXTRA 环境变量仍由部署方维护，不在此处覆盖。",
	},
	"auth.registration_mode": {
		Type:    "string",
		EnvName: "", // No env fallback — handler passes cfg.Auth.RegistrationMode as default
		Default: "self_serve",
		Enum:    []string{"self_serve", "invite_only"},
		Category: "auth",
		Description: "自助注册模式。self_serve = 任何人可注册账号；invite_only = 关闭公网注册，" +
			"仅 Owner/Admin 可邀请。修改后立即生效，但谨慎对待 self_serve（公网会接受 spam）。",
	},
}

// systemSettingService wires the repository, audit log, and (P2)
// the Redis client + an in-memory cache. Cache strategy is "preload
// at boot, invalidate via pubsub":
//
//   - On startup we async-load every row into `cache` (best-effort —
//     a DB hiccup just means a slower warmup, not a fatal error).
//   - GetXxx reads from cache (microsecond latency).
//   - Update writes DB → updates local cache → publishes a change
//     notification to Redis.
//   - Subscribers on every replica read the notification and re-fetch
//     the row from DB (NOT from the message payload — the message only
//     carries the key, never the value, so we never trust pubsub-as-
//     transport with config bytes).
//   - The publishing replica skips its own messages by matching
//     OriginID against its instanceID.
//
// When Redis is nil (lite mode / REDIS_ADDR unset), every code path
// degrades back to P1 behaviour: no cache invalidation, but local
// edits still take effect (since Update does write the local cache
// inline). This is the right behaviour for single-replica deployments.
type systemSettingService struct {
	repo  interfaces.SystemSettingRepository
	audit interfaces.AuditLogService
	rdb   *redis.Client // may be nil in lite mode

	// instanceID disambiguates this replica from its peers in the
	// pubsub stream. Generated once at construction; never changes.
	instanceID string

	// cache holds every known setting indexed by key. Populated by
	// loadCache (preload + after every pubsub message). All access
	// goes through `mu`. A nil entry means "we know there's no row
	// and the resolver should fall through to ENV/default".
	mu    sync.RWMutex
	cache map[string]*types.SystemSetting

	// loaded flips true once the initial preload finishes. Reads
	// before this point fall through to the DB so the very first
	// hot request after boot doesn't get a default-valued surprise.
	loaded atomic.Bool

	// subOnce guarantees SubscribeRedis can be called multiple times
	// without spawning duplicate goroutines (defensive — main only
	// calls it once).
	subOnce sync.Once
}

// NewSystemSettingService is the dig provider. audit may be nil
// (matches the tenantMemberService convention — tests that don't care
// about audit can pass nil and emitAudit no-ops). rdb may also be nil
// when REDIS_ADDR is unset — the service degrades gracefully to the
// P1 "no cache, every read hits DB" path.
func NewSystemSettingService(
	repo interfaces.SystemSettingRepository,
	audit interfaces.AuditLogService,
	rdb *redis.Client,
) interfaces.SystemSettingService {
	s := &systemSettingService{
		repo:       repo,
		audit:      audit,
		rdb:        rdb,
		instanceID: uuid.NewString(),
		cache:      make(map[string]*types.SystemSetting),
	}
	// Async preload — don't block container build / handler readiness
	// on a slow DB. The first few requests may miss cache and hit the
	// DB directly via the resolver fallback; that's a few ms each and
	// completes long before the cache is full.
	go s.preload(context.Background())
	return s
}

// preload populates the cache with every row from the system_settings
// table. Best-effort: a DB error here is logged and silently swallowed,
// because the resolver's DB-fallback path will still serve correct
// values (just slower). Logging the count gives operators a single line
// in the startup log they can grep for ("how many keys did P2 load?").
func (s *systemSettingService) preload(ctx context.Context) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		logger.Warnf(ctx, "[system_settings] preload failed, falling back to per-request DB reads: %v", err)
		return
	}
	s.mu.Lock()
	for _, row := range rows {
		s.cache[row.Key] = row
	}
	s.mu.Unlock()

	// Backfill: any registry key that doesn't yet have a DB row gets
	// inserted now with its built-in default. This makes the in-code
	// `registry` map the single source of truth — adding a new tunable
	// is a code change, no migration required, and the management UI
	// surfaces it the next time the server boots. Idempotent: existing
	// rows are never touched (Upsert would, but we skip when present).
	s.seedMissingFromRegistry(ctx)

	s.loaded.Store(true)
	s.mu.RLock()
	loadedCount := len(s.cache)
	s.mu.RUnlock()
	logger.Infof(ctx, "[system_settings] cache loaded %d keys (instance=%s)", loadedCount, s.instanceID[:8])

	// Side-effect bridges: any setting whose live value affects an
	// in-process subsystem needs to be pushed there after preload, so
	// the subsystem doesn't lag the cache by a full request cycle.
	// Add new bridges here as more env vars get migrated.
	s.applySSRFWhitelist(ctx)
}

// seedMissingFromRegistry inserts a default row for every registry key
// that doesn't already exist in the DB. Called from preload after the
// initial List, so that:
//
//   - New deployments (empty table) get every key seeded automatically.
//   - Existing deployments where a new key was added in code (without a
//     migration) automatically pick it up on the next server start.
//   - Hand-deleted rows are restored on next start (mild self-healing).
//
// Critically, this DOES NOT touch existing rows — operator edits via UI
// are preserved. Errors per-key are logged but never block other keys
// or fail the boot. The s.cache mutation runs under the write lock so a
// reader landing in the middle of seeding still sees a consistent view.
func (s *systemSettingService) seedMissingFromRegistry(ctx context.Context) {
	for key, spec := range registry {
		s.mu.RLock()
		_, exists := s.cache[key]
		s.mu.RUnlock()
		if exists {
			continue
		}
		encoded, err := encodeDefault(spec)
		if err != nil {
			logger.Warnf(ctx, "[system_settings] cannot encode default for %q: %v", key, err)
			continue
		}
		category := spec.Category
		if category == "" {
			category = "general"
		}
		row := &types.SystemSetting{
			Key:             key,
			Value:           encoded,
			ValueType:       spec.Type,
			Category:        category,
			Description:     spec.Description,
			IsSecret:        false, // P3+ may flip via spec; today every seed row is non-secret
			RequiresRestart: false,
			LastModifiedBy:  "", // empty = "seeded by system"
		}
		if err := s.repo.Upsert(ctx, row); err != nil {
			logger.Warnf(ctx, "[system_settings] seed %q failed: %v", key, err)
			continue
		}
		// Read back so we see DB-assigned id / timestamps (and so the
		// cache entry round-trips through the same JSON shape as a
		// hand-edited row would).
		persisted, err := s.repo.Get(ctx, key)
		if err != nil || persisted == nil {
			persisted = row
		}
		s.mu.Lock()
		s.cache[key] = persisted
		s.mu.Unlock()
		logger.Infof(ctx, "[system_settings] seeded missing key %q (type=%s, category=%s)",
			key, spec.Type, category)
	}
}

// encodeDefault produces the JSONB encoding for a spec's built-in
// default. Mirrors encodeForType but operates on already-typed Go
// values from registry so we never have to round-trip through `any`
// type assertions on the seed path. Returns an error when spec.Default
// is missing or its Go type doesn't match spec.Type — that's a code
// bug in the registry entry, surface it loudly rather than silently
// seeding the wrong shape.
func encodeDefault(spec settingSpec) (types.JSON, error) {
	switch spec.Type {
	case "int":
		var n int64
		switch v := spec.Default.(type) {
		case int:
			n = int64(v)
		case int64:
			n = v
		case float64:
			n = int64(v)
		default:
			return nil, fmt.Errorf("registry spec for int has wrong default type %T", spec.Default)
		}
		b, _ := json.Marshal(n)
		return types.JSON(b), nil
	case "string":
		v, ok := spec.Default.(string)
		if !ok {
			return nil, fmt.Errorf("registry spec for string has wrong default type %T", spec.Default)
		}
		b, _ := json.Marshal(v)
		return types.JSON(b), nil
	case "bool":
		v, ok := spec.Default.(bool)
		if !ok {
			return nil, fmt.Errorf("registry spec for bool has wrong default type %T", spec.Default)
		}
		b, _ := json.Marshal(v)
		return types.JSON(b), nil
	case "string_list":
		switch v := spec.Default.(type) {
		case []string:
			if v == nil {
				v = []string{}
			}
			b, _ := json.Marshal(v)
			return types.JSON(b), nil
		case nil:
			return types.JSON(`[]`), nil
		default:
			return nil, fmt.Errorf("registry spec for string_list has wrong default type %T", spec.Default)
		}
	default:
		return nil, errors.New("unknown declared type: " + spec.Type)
	}
}

// reload re-fetches a single key from DB and updates the cache. Called
// from the pubsub subscriber loop after another replica publishes a
// change. A repo.Get(nil) result removes the entry — the row must have
// been deleted by an out-of-band tool (P1 has no Delete endpoint, but
// hand-edits still work).
func (s *systemSettingService) reload(ctx context.Context, key string) {
	row, err := s.repo.Get(ctx, key)
	if err != nil {
		logger.Warnf(ctx, "[system_settings] reload %q failed: %v", key, err)
		return
	}
	s.mu.Lock()
	if row == nil {
		delete(s.cache, key)
	} else {
		s.cache[key] = row
	}
	s.mu.Unlock()

	// Push any side-effect bridges for the changed key. Bridges are
	// idempotent — calling them on every reload (even when the change
	// is to a different key) is fine and lets us avoid plumbing a
	// per-key dispatch table.
	s.dispatchSideEffects(ctx, key)
}

// dispatchSideEffects fans out post-Update / post-reload work to
// subsystems whose state depends on a system_setting. Each bridge
// looks up its own keys and decides whether to act — this keeps the
// dispatcher trivial as we add more.
func (s *systemSettingService) dispatchSideEffects(ctx context.Context, changedKey string) {
	switch changedKey {
	case "ssrf.whitelist":
		s.applySSRFWhitelist(ctx)
	}
}

// applySSRFWhitelist resolves the active ssrf.whitelist via the 3-tier
// resolver and pushes the result (merged with SSRF_WHITELIST_EXTRA)
// to utils.SetSSRFWhitelistFromRaw. SSRF_WHITELIST_EXTRA stays env-only:
// it's typically set by docker-compose / k8s for sidecar service names
// and shouldn't be subject to UI accidents.
//
// Called at preload (initial sync), after Update (this replica's edit),
// and after reload (peer's edit via pubsub).
func (s *systemSettingService) applySSRFWhitelist(ctx context.Context) {
	list := s.GetStringList(ctx, "ssrf.whitelist", "SSRF_WHITELIST", []string{})
	primary := strings.Join(list, ",")
	extra := strings.TrimSpace(os.Getenv("SSRF_WHITELIST_EXTRA"))
	merged := primary
	if extra != "" {
		if merged == "" {
			merged = extra
		} else {
			merged = merged + "," + extra
		}
	}
	utils.SetSSRFWhitelistFromRaw(merged)
	logger.Infof(ctx, "[system_settings] SSRF whitelist applied (%d primary entries, extra=%v)",
		len(list), extra != "")
}

// publishChange fans the change out to peers. Best-effort: a Redis
// outage logs a warning but does not fail the Update — the DB write
// already succeeded and our local cache is up-to-date. Other replicas
// will pick up the new value on their next preload (e.g. restart) or
// when their own resolver detects a stale cache via fallback.
func (s *systemSettingService) publishChange(ctx context.Context, key string) {
	if s.rdb == nil {
		return
	}
	payload, err := json.Marshal(changeMessage{Key: key, OriginID: s.instanceID})
	if err != nil {
		logger.Warnf(ctx, "[system_settings] marshal change for %q: %v", key, err)
		return
	}
	pubCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.rdb.Publish(pubCtx, pubsubChannel(), payload).Err(); err != nil {
		logger.Warnf(ctx, "[system_settings] publish %q: %v", key, err)
	}
}

// NewSystemSettingService is the dig provider. audit may be nil
// (matches the tenantMemberService convention — tests that don't care
// about audit can pass nil and emitAudit no-ops).
//
// Compatibility shim: the real ctor lives above (with rdb). Keeping this
// alternate signature would break dig (two providers for one type), so
// it is intentionally NOT exported separately. Tests that don't have a
// Redis client should pass nil — the service detects nil and degrades
// to the P1 "no cache, no pubsub" path.

// resolveRaw runs the 3-tier fallback ladder for an arbitrary key and
// returns either the raw DB value bytes (when present), or nil with
// the boolean fromDB=false to signal the caller to consult ENV / default.
//
// P2: cache-first. If the preload finished and the cache has an entry
// for this key, return it. Cache misses (key absent) are AUTHORITATIVE
// when loaded.IsTrue — preload populated every existing row, and any
// subsequent Update would have updated the cache inline. So a miss
// after preload means the row genuinely doesn't exist and we should
// skip the DB query entirely. Before preload finishes we still consult
// the DB to avoid a "cold-start serves defaults" surprise.
//
// Errors at the DB layer degrade to ENV/default with a warning log —
// upstream business code (file upload, etc.) gets a usable answer
// instead of a 500. This is the deliberate degradation policy spelled
// out in the interface comment.
func (s *systemSettingService) resolveRaw(ctx context.Context, key string) (raw types.JSON, fromDB bool) {
	if s.loaded.Load() {
		s.mu.RLock()
		row, ok := s.cache[key]
		s.mu.RUnlock()
		if ok && row != nil {
			return row.Value, true
		}
		// Cache populated and key not present → authoritative miss.
		return nil, false
	}
	// Pre-warmup path: hit the DB so a request that lands in the
	// startup window doesn't get the env/default surprise.
	row, err := s.repo.Get(ctx, key)
	if err != nil {
		logger.Warnf(ctx, "[system_settings] resolve %q failed, falling through to env/default: %v", key, err)
		return nil, false
	}
	if row == nil {
		return nil, false
	}
	return row.Value, true
}

// GetInt resolves an int64 setting. Priority: DB > ENV > def. Returns
// def on every error path so business code never has to handle the
// "the settings store is broken" case.
func (s *systemSettingService) GetInt(ctx context.Context, key string, envName string, def int64) int64 {
	if raw, ok := s.resolveRaw(ctx, key); ok {
		// Try canonical number form first.
		var n int64
		if err := json.Unmarshal(raw, &n); err == nil {
			return n
		}
		// Tolerate `"42"` so hand-edited rows still work.
		var quoted string
		if err := json.Unmarshal(raw, &quoted); err == nil {
			if v, err := strconv.ParseInt(quoted, 10, 64); err == nil {
				return v
			}
		}
		logger.Warnf(ctx, "[system_settings] %q: cannot parse %s as int, falling back", key, string(raw))
	}
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
	}
	return def
}

// GetString resolves a string setting. Same priority + degradation as GetInt.
func (s *systemSettingService) GetString(ctx context.Context, key string, envName string, def string) string {
	if raw, ok := s.resolveRaw(ctx, key); ok {
		var v string
		if err := json.Unmarshal(raw, &v); err == nil {
			return v
		}
		logger.Warnf(ctx, "[system_settings] %q: cannot parse %s as string, falling back", key, string(raw))
	}
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			return v
		}
	}
	return def
}

// GetBool resolves a bool setting. Tolerates legacy ENV values like
// "1", "0", "yes", "no" via strconv.ParseBool. Same priority + degradation.
func (s *systemSettingService) GetBool(ctx context.Context, key string, envName string, def bool) bool {
	if raw, ok := s.resolveRaw(ctx, key); ok {
		var v bool
		if err := json.Unmarshal(raw, &v); err == nil {
			return v
		}
		logger.Warnf(ctx, "[system_settings] %q: cannot parse %s as bool, falling back", key, string(raw))
	}
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				return b
			}
		}
	}
	return def
}

// GetStringList resolves a []string setting. Priority: DB > ENV > def.
//
// At the ENV level the value is parsed as a comma-separated string
// (matches the legacy SSRF_WHITELIST format and means operators don't
// have to learn a new convention to migrate). Whitespace around each
// entry is trimmed; empty entries are dropped. The returned slice is
// always non-nil so callers can iterate without a nil check.
//
// Same degradation policy as the other Get*: a DB-layer error logs a
// warning and falls through to ENV/default, so consumer paths
// (SSRF check, etc.) never have to handle "settings store broken".
func (s *systemSettingService) GetStringList(ctx context.Context, key string, envName string, def []string) []string {
	if raw, ok := s.resolveRaw(ctx, key); ok {
		var v []string
		if err := json.Unmarshal(raw, &v); err == nil {
			if v == nil {
				v = []string{}
			}
			return v
		}
		logger.Warnf(ctx, "[system_settings] %q: cannot parse %s as string_list, falling back", key, string(raw))
	}
	if envName != "" {
		if raw := os.Getenv(envName); raw != "" {
			out := make([]string, 0, 4)
			for _, entry := range strings.Split(raw, ",") {
				entry = strings.TrimSpace(entry)
				if entry != "" {
					out = append(out, entry)
				}
			}
			return out
		}
	}
	if def == nil {
		return []string{}
	}
	return def
}

// List returns all rows for the management UI. Pass-through to repo,
// then enriched with the in-code registry's `Enum` so the UI can render
// a select. Rows whose key isn't in the registry (out-of-band hand-edits)
// pass through untouched — UI will fall back to a free-form input.
func (s *systemSettingService) List(ctx context.Context) ([]*types.SystemSetting, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		if spec, ok := registry[r.Key]; ok {
			r.Enum = spec.Enum
		}
	}
	return rows, nil
}

// Get returns one row by key. Used by the management UI's "load before
// edit" pattern. Returns (nil, nil) when missing (unknown-key handling
// is done at the handler layer for nicer 404 vs 200-with-default UX).
//
// Enriches the row with registry-side `Enum` for the same UI reason
// as List.
func (s *systemSettingService) Get(ctx context.Context, key string) (*types.SystemSetting, error) {
	spec, ok := registry[key]
	if !ok {
		return nil, fmt.Errorf("unknown setting key %q", key)
	}
	row, err := s.repo.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	if row != nil {
		row.Enum = spec.Enum
	}
	return row, nil
}

// Update validates and persists a new value. Steps:
//  1. Look up the registry spec — reject unknown keys with 400 semantics.
//  2. Coerce + validate the rawValue against spec.Type. Numeric inputs
//     from JSON unmarshalling arrive as float64; we accept both int64
//     and float64 for "int" and round-trip through strconv to surface
//     rejection of e.g. floats like 3.14 cleanly.
//  3. Build the SystemSetting row, write via repo.Upsert.
//  4. Emit an audit log carrying old + new values for forensics.
//
// Returns the persisted row (re-read from DB so updated_at /
// last_modified_by are fresh).
func (s *systemSettingService) Update(ctx context.Context, key string, rawValue any) (*types.SystemSetting, error) {
	spec, ok := registry[key]
	if !ok {
		return nil, fmt.Errorf("unknown setting key %q", key)
	}

	encoded, err := encodeForType(spec.Type, rawValue)
	if err != nil {
		return nil, fmt.Errorf("invalid value for %q (expected %s): %w", key, spec.Type, err)
	}

	// Enum check: only meaningful for "string". Compare the decoded
	// string against the registry-declared whitelist. Done after
	// encodeForType so we know the raw value passed type validation.
	if len(spec.Enum) > 0 && spec.Type == "string" {
		s, _ := rawValue.(string)
		allowed := false
		for _, opt := range spec.Enum {
			if s == opt {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("invalid value for %q: %q not in %v", key, s, spec.Enum)
		}
	}

	// Capture pre-image for the audit log — pulled fresh, not from
	// any cache, so concurrent admin edits race-fairly (last writer
	// wins, audit reflects what was actually replaced).
	prev, _ := s.repo.Get(ctx, key)
	var oldValue types.JSON
	var category, description string
	var isSecret, requiresRestart bool
	if prev != nil {
		oldValue = prev.Value
		category = prev.Category
		description = prev.Description
		isSecret = prev.IsSecret
		requiresRestart = prev.RequiresRestart
	} else {
		// First-write path: derive category/description from registry
		// so the row matches the seeded migration shape. Operators can
		// hand-edit description in the DB if they want richer copy.
		category = spec.Category
		if category == "" {
			category = "general"
		}
		description = spec.Description
	}

	row := &types.SystemSetting{
		Key:             key,
		Value:           encoded,
		ValueType:       spec.Type,
		Category:        category,
		Description:     description,
		IsSecret:        isSecret,
		RequiresRestart: requiresRestart,
		LastModifiedBy:  auditActor(ctx),
	}
	if err := s.repo.Upsert(ctx, row); err != nil {
		return nil, fmt.Errorf("upsert system setting %q: %w", key, err)
	}

	// Re-read so caller sees DB-side defaults (id, updated_at) populated.
	persisted, err := s.repo.Get(ctx, key)
	if err != nil || persisted == nil {
		// Don't fail the operation just because the read-back hiccuped —
		// the upsert already succeeded. Return the optimistic value.
		persisted = row
	}

	// Update local cache inline so this replica's next read sees the
	// new value without waiting for the pubsub roundtrip. Other replicas
	// pick it up via publishChange below.
	s.mu.Lock()
	s.cache[key] = persisted
	s.mu.Unlock()

	// Push to side-effect bridges (e.g. utils.SetSSRFWhitelistFromRaw).
	s.dispatchSideEffects(ctx, key)

	s.publishChange(ctx, key)
	s.emitChangeAudit(ctx, key, spec.Type, oldValue, encoded)
	return persisted, nil
}

// SubscribeRedis starts a single goroutine that subscribes to the
// pubsub channel and refreshes the local cache when peers publish
// changes. Idempotent (subOnce). When Redis is nil (lite mode) returns
// nil immediately — single-replica deployments don't need pubsub
// because Update already writes the local cache inline.
//
// The subscriber loop runs until ctx is cancelled (server shutdown).
// On Redis disconnection we reconnect with exponential backoff up to
// 30s, mirroring the approval/gate.go convention so operators see the
// same recovery behaviour across pubsub-using subsystems.
func (s *systemSettingService) SubscribeRedis(ctx context.Context) error {
	if s.rdb == nil {
		logger.Infof(ctx, "[system_settings] Redis not configured, skipping pubsub (single-replica mode)")
		return nil
	}
	s.subOnce.Do(func() {
		go s.runSubscribeLoop(ctx)
	})
	return nil
}

// runSubscribeLoop is the long-running goroutine spawned by
// SubscribeRedis. Reconnects on transient errors; exits on ctx.Done().
func (s *systemSettingService) runSubscribeLoop(ctx context.Context) {
	channel := pubsubChannel()
	logger.Infof(ctx, "[system_settings] subscribed to %s (instance=%s)", channel, s.instanceID[:8])

	const maxBackoff = 30 * time.Second
	backoff := time.Second
	for {
		// ctx may already be cancelled (server shutting down before
		// pubsub became active).
		if ctx.Err() != nil {
			return
		}
		sub := s.rdb.Subscribe(ctx, channel)
		// Verify the subscription is active so a publish-and-disconnect
		// race doesn't silently drop the first message.
		if _, err := sub.Receive(ctx); err != nil {
			logger.Warnf(ctx, "[system_settings] subscribe %s: %v (retry in %s)", channel, err, backoff)
			_ = sub.Close()
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
			continue
		}
		backoff = time.Second // reset after a healthy connection
		ch := sub.Channel()
		s.consumeMessages(ctx, ch)
		_ = sub.Close()
		// consumeMessages returns either because ctx is done or the
		// subscription was torn down; loop back and try again.
	}
}

// consumeMessages drains the pubsub channel, dispatching to reload()
// for every key the peer says changed. Returns when the channel
// closes (Redis disconnect) or ctx is done.
func (s *systemSettingService) consumeMessages(ctx context.Context, ch <-chan *redis.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var m changeMessage
			if err := json.Unmarshal([]byte(msg.Payload), &m); err != nil {
				logger.Warnf(ctx, "[system_settings] bad pubsub payload: %v", err)
				continue
			}
			// Skip our own publish — the local cache is already fresh
			// (Update wrote it inline). Without this every Update would
			// trigger a redundant DB roundtrip on the publishing replica.
			if m.OriginID == s.instanceID {
				continue
			}
			s.reload(ctx, m.Key)
		}
	}
}

// emitChangeAudit writes one audit row per successful Update. Best-
// effort — a nil audit service or a write failure does not bubble up.
// This mirrors tenantMemberService.emitAudit's failure semantics: the
// business op (config update) succeeds even if audit is broken.
func (s *systemSettingService) emitChangeAudit(
	ctx context.Context, key, valueType string, oldValue, newValue types.JSON,
) {
	if s.audit == nil {
		return
	}
	details, _ := json.Marshal(map[string]any{
		"key":        key,
		"value_type": valueType,
		"old_value":  json.RawMessage(oldValue),
		"new_value":  json.RawMessage(newValue),
	})
	_ = s.audit.Log(ctx, &types.AuditLog{
		// tenant_id=0 marks the row as system-scope (the audit_logs
		// table itself is tenant-scoped; 0 is the convention for
		// platform-wide events).
		TenantID:    0,
		ActorUserID: auditActor(ctx),
		ActorRole:   "system_admin",
		Action:      types.AuditActionSystemSettingChanged,
		TargetType:  "system_setting",
		TargetID:    key,
		Outcome:     types.AuditOutcomeSuccess,
		Details:     types.JSON(details),
	})
}

// encodeForType validates rawValue against the declared type and
// returns the canonical JSON encoding for the DB. Rejects type
// mismatches (e.g. passing "abc" for an int field) with a clear error
// the handler can surface to the UI.
func encodeForType(declared string, rawValue any) (types.JSON, error) {
	switch declared {
	case "int":
		var n int64
		switch v := rawValue.(type) {
		case int:
			n = int64(v)
		case int32:
			n = int64(v)
		case int64:
			n = v
		case float64:
			// JSON unmarshalling delivers numbers as float64; reject
			// non-integer floats (e.g. 3.14) cleanly rather than
			// silently truncating.
			if v != float64(int64(v)) {
				return nil, fmt.Errorf("expected integer, got %v", v)
			}
			n = int64(v)
		case string:
			parsed, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("expected integer, got %q", v)
			}
			n = parsed
		default:
			return nil, fmt.Errorf("expected integer, got %T", rawValue)
		}
		b, _ := json.Marshal(n)
		return types.JSON(b), nil
	case "string":
		v, ok := rawValue.(string)
		if !ok {
			return nil, fmt.Errorf("expected string, got %T", rawValue)
		}
		b, _ := json.Marshal(v)
		return types.JSON(b), nil
	case "bool":
		v, ok := rawValue.(bool)
		if !ok {
			return nil, fmt.Errorf("expected bool, got %T", rawValue)
		}
		b, _ := json.Marshal(v)
		return types.JSON(b), nil
	case "string_list":
		// Accept either a JSON array of strings (the canonical UI shape
		// — t-tag-input emits string[]) or a single comma-separated
		// string (operator pasting from a legacy ENV value). Reject
		// arrays containing non-strings to avoid silently coercing
		// `[1, 2]` into `["1", "2"]` — that hides typos.
		var entries []string
		switch v := rawValue.(type) {
		case []any:
			entries = make([]string, 0, len(v))
			for i, item := range v {
				s, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("expected string at index %d, got %T", i, item)
				}
				s = strings.TrimSpace(s)
				if s != "" {
					entries = append(entries, s)
				}
			}
		case []string:
			entries = make([]string, 0, len(v))
			for _, s := range v {
				s = strings.TrimSpace(s)
				if s != "" {
					entries = append(entries, s)
				}
			}
		case string:
			for _, s := range strings.Split(v, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					entries = append(entries, s)
				}
			}
			if entries == nil {
				entries = []string{}
			}
		default:
			return nil, fmt.Errorf("expected string array, got %T", rawValue)
		}
		b, _ := json.Marshal(entries)
		return types.JSON(b), nil
	default:
		return nil, errors.New("unknown declared type: " + declared)
	}
}
