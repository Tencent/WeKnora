package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/config"
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
//  1. Adding an entry here.
//  2. (Optional) adding a SQL seed row in a new migration so the UI
//     shows the row even before any operator hits Update.
//  3. Replacing existing os.Getenv() reads with calls into the
//     service.
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
	// RequiresRestart marks keys whose value is bound at process startup
	// (e.g. asynq worker pool size). The UI shows a restart badge; the
	// service persists the flag on first write.
	RequiresRestart bool
}

// registry pins the set of legal keys. Expanding it is a deliberate,
// reviewable operation — the implicit contract is "every key here is
// safely runtime-tunable (no startup caching that would not honour
// the new value, no in-memory state bound at init time we cannot
// re-derive)".
var registry = map[string]settingSpec{
	// NOTE: file.max_size_mb is intentionally NOT registered. Although
	// the Go upload handlers accept a runtime override via
	// systemSettingSvc.GetInt, the actual upload limit is gated end-to-end
	// by three independent layers:
	//   1. nginx client_max_body_size (templated at container startup
	//      from the MAX_FILE_SIZE_MB env var; envsubst writes the
	//      computed value into nginx.conf; nginx is never reloaded
	//      during the container's lifetime).
	//   2. docreader gRPC max_send/recv_message_length (read from the
	//      MAX_FILE_SIZE_MB env at python startup).
	//   3. The frontend client-side check (utils/index.ts) reads
	//      window.__RUNTIME_CONFIG__.MAX_FILE_SIZE_MB which is
	//      written into /usr/share/nginx/html/config.js by the
	//      docker-entrypoint at container start.
	// Surfacing a UI knob whose effect is silently capped by nginx /
	// docreader / the in-page bundle is worse than not having it.
	// Keep MAX_FILE_SIZE_MB as a deploy-time env var until all four
	// layers can be reconfigured in lockstep without restarts.
	"ssrf.whitelist": {
		Type:     "string_list",
		EnvName:  "SSRF_WHITELIST",
		Default:  []string{},
		Category: "security",
		Description: "SSRF 防护白名单。可填入 example.com / *.foo.com / 10.0.0.0/8 / 2001:db8::1。" +
			"修改后立即生效。SSRF_WHITELIST_EXTRA 环境变量仍由部署方维护，不在此处覆盖。",
	},
	"auth.registration_mode": {
		Type:     "string",
		EnvName:  "", // No env fallback — handler passes cfg.Auth.RegistrationMode as default
		Default:  "self_serve",
		Enum:     []string{"self_serve", "invite_only"},
		Category: "auth",
		Description: "自助注册模式。self_serve = 任何人可注册账号；invite_only = 关闭公网注册，" +
			"仅 Owner/Admin 可邀请。修改后立即生效，但谨慎对待 self_serve（公网会接受 spam）。",
	},
	// tenant.max_owned_per_user caps how many tenants a single non-superuser
	// can create (and Own) via self-service POST /tenants. Read on every
	// request — UI edits take effect immediately, no restart required. The
	// EnvName is the same WEKNORA_TENANT_MAX_OWNED_PER_USER that
	// applyAuthAndTenantDefaults parses at boot, so a deployment that
	// hasn't created a DB row keeps reading from env exactly as before.
	// 0 = use the in-code default (10); negative = disable the cap entirely.
	"tenant.max_owned_per_user": {
		Type:     "int",
		EnvName:  "WEKNORA_TENANT_MAX_OWNED_PER_USER",
		Default:  int64(10),
		Category: "tenant",
		Description: "每个非超管用户通过自助创建可拥有的最大租户数。每次创建租户时实时读取，" +
			"修改后立即生效。0 表示使用内置默认值 10；负数表示完全关闭限制（不建议在公开部署使用）。",
	},
	// tenant.default_storage_quota_gb is the default storage quota (in GB)
	// applied to a newly-created tenant when the caller doesn't specify
	// one explicitly. Read at create time only — changing the value does
	// NOT retroactively resize already-existing tenants (they keep the
	// quota stored on their row at creation; superusers can edit
	// individual tenants via the existing tenant-update path).
	// 0 or negative = use the in-code default (10 GB).
	"tenant.default_storage_quota_gb": {
		Type:     "int",
		EnvName:  "WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_GB",
		Default:  int64(10),
		Category: "tenant",
		Description: "新建租户时默认分配的存储配额（GB），包含向量、原文、文本、索引等。" +
			"仅在创建时读取，修改后只对之后新建的租户生效，不会回写已存在的租户。" +
			"0 或负数表示使用内置默认值 10GB。",
	},
	// asynq.concurrency is the asynq worker pool size (parallel in-flight
	// tasks). Read once when the asynq server starts — changing it in the
	// UI requires a process restart to take effect. Mirrors
	// WEKNORA_ASYNQ_CONCURRENCY (default 32).
	"asynq.concurrency": {
		Type:            "int",
		EnvName:         "WEKNORA_ASYNQ_CONCURRENCY",
		Default:         int64(32),
		Category:        "worker",
		RequiresRestart: true,
		Description: "异步任务 worker 并发数（asynq 线程池大小）。" +
			"文档解析、嵌入等任务多为 I/O 等待，适当提高可缩短批量上传排队时间。" +
			"修改后需重启服务进程方可生效。",
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
	cfg   *config.Config

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
// when REDIS_ADDR is unset — the service still uses its local cache,
// but skips cross-replica pubsub invalidation.
func NewSystemSettingService(
	repo interfaces.SystemSettingRepository,
	audit interfaces.AuditLogService,
	rdb *redis.Client,
	cfg *config.Config,
) interfaces.SystemSettingService {
	s := &systemSettingService{
		repo:       repo,
		audit:      audit,
		rdb:        rdb,
		cfg:        cfg,
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
	spec, known := registry[key]
	if s.loaded.Load() {
		s.mu.RLock()
		row, ok := s.cache[key]
		s.mu.RUnlock()
		if ok && row != nil {
			if known && isBootstrapDefaultRow(row, spec) {
				return nil, false
			}
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
	if known && isBootstrapDefaultRow(row, spec) {
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

// List returns all known settings for the management UI. Persisted rows
// are enriched with registry metadata. Registry keys without a saved DB
// override are returned as virtual rows using the effective fallback
// value (ENV/config/default), so merely migrating the schema never
// changes runtime behaviour.
func (s *systemSettingService) List(ctx context.Context) ([]*types.SystemSetting, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*types.SystemSetting, len(rows))
	out := make([]*types.SystemSetting, 0, len(rows)+len(registry))
	for _, row := range rows {
		byKey[row.Key] = row
	}

	keys := make([]string, 0, len(registry))
	for key := range registry {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		spec := registry[key]
		if row := byKey[key]; row != nil {
			row.Enum = spec.Enum
			if isBootstrapDefaultRow(row, spec) {
				row.Value = s.fallbackJSONForSpec(key, spec)
			}
			out = append(out, row)
			delete(byKey, key)
			continue
		}
		out = append(out, s.virtualSetting(key, spec))
	}

	// Preserve out-of-band rows so operators can still see unexpected
	// data instead of having it disappear from the UI.
	extraKeys := make([]string, 0, len(byKey))
	for key := range byKey {
		extraKeys = append(extraKeys, key)
	}
	sort.Strings(extraKeys)
	for _, key := range extraKeys {
		out = append(out, byKey[key])
	}
	return out, nil
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
		if isBootstrapDefaultRow(row, spec) {
			row.Value = s.fallbackJSONForSpec(key, spec)
		}
		return row, nil
	}
	return s.virtualSetting(key, spec), nil
}

func (s *systemSettingService) virtualSetting(key string, spec settingSpec) *types.SystemSetting {
	category := spec.Category
	if category == "" {
		category = "general"
	}
	return &types.SystemSetting{
		Key:             key,
		Value:           s.fallbackJSONForSpec(key, spec),
		ValueType:       spec.Type,
		Category:        category,
		Description:     spec.Description,
		IsSecret:        false,
		RequiresRestart: spec.RequiresRestart,
		LastModifiedBy:  "",
		Enum:            spec.Enum,
	}
}

func (s *systemSettingService) fallbackJSONForSpec(key string, spec settingSpec) types.JSON {
	if spec.EnvName != "" {
		if raw := strings.TrimSpace(os.Getenv(spec.EnvName)); raw != "" {
			switch spec.Type {
			case "int":
				if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
					if encoded, err := encodeForType(spec.Type, n); err == nil {
						return encoded
					}
				}
			case "string":
				if encoded, err := encodeForType(spec.Type, raw); err == nil {
					return encoded
				}
			case "bool":
				if b, err := strconv.ParseBool(raw); err == nil {
					if encoded, err := encodeForType(spec.Type, b); err == nil {
						return encoded
					}
				}
			case "string_list":
				entries := make([]string, 0, 4)
				for _, entry := range strings.Split(raw, ",") {
					entry = strings.TrimSpace(entry)
					if entry != "" {
						entries = append(entries, entry)
					}
				}
				if encoded, err := encodeForType(spec.Type, entries); err == nil {
					return encoded
				}
			}
		}
	}
	if key == "auth.registration_mode" {
		mode := config.AuthRegistrationModeSelfServe
		if s.cfg != nil && s.cfg.Auth != nil {
			if configured := strings.TrimSpace(s.cfg.Auth.RegistrationMode); configured != "" {
				mode = configured
			}
		}
		if encoded, err := encodeForType(spec.Type, mode); err == nil {
			return encoded
		}
	}
	encoded, err := encodeDefault(spec)
	if err != nil {
		return types.JSON(`null`)
	}
	return encoded
}

// isBootstrapDefaultRow treats old migration/service seeded defaults as
// placeholders rather than operator-owned overrides. Those rows used an
// empty last_modified_by and the registry default value, so deployments
// that already ran the unsafe seed regain the intended ENV/config
// fallback behaviour until a SystemAdmin explicitly saves a value.
func isBootstrapDefaultRow(row *types.SystemSetting, spec settingSpec) bool {
	if row == nil || strings.TrimSpace(row.LastModifiedBy) != "" {
		return false
	}
	def, err := encodeDefault(spec)
	if err != nil {
		return false
	}
	return jsonEqual(row.Value, def)
}

func jsonEqual(a, b types.JSON) bool {
	var ca, cb bytes.Buffer
	if err := json.Compact(&ca, a); err != nil {
		return false
	}
	if err := json.Compact(&cb, b); err != nil {
		return false
	}
	return bytes.Equal(ca.Bytes(), cb.Bytes())
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
	//
	// Use a local name `str` rather than `s` to avoid shadowing the
	// outer `*systemSettingService` receiver — the previous version
	// relied on the shadow being unused inside the block, which was a
	// trap for future edits.
	if len(spec.Enum) > 0 && spec.Type == "string" {
		str, _ := rawValue.(string)
		allowed := false
		for _, opt := range spec.Enum {
			if str == opt {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("invalid value for %q: %q not in %v", key, str, spec.Enum)
		}
	}

	// Per-key structural validation. encodeForType already enforces the
	// value_type contract (int / string / bool / string_list); this hook
	// is for keys whose value carries an internal grammar that, if
	// silently malformed, would either fail to take effect at runtime
	// (e.g. SSRF whitelist parser drops bad CIDRs) or actively
	// mis-classify input. Reject with 400 so the UI can show a clear
	// inline error instead of "saved" + nothing happens.
	if err := validateRegistryEntry(key, rawValue); err != nil {
		return nil, fmt.Errorf("invalid value for %q: %w", key, err)
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
		requiresRestart = spec.RequiresRestart
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
	// Mirror the registry-derived enrichment that List/Get apply.
	// Without this, the persisted row hands back an empty Enum field
	// to the API client, which causes the management UI to swap a
	// t-select for a plain text input on the post-save patch and
	// display the raw enum value (e.g. "self_serve") until the next
	// full reload. Other registry-only fields (Type via ValueType is
	// already on the row; Description/Category are persisted) don't
	// need the same fix-up because they're stored on the row itself.
	persisted.Enum = spec.Enum

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

// Reset deletes the DB override for `key` so the resolver falls back
// to ENV / built-in default. Idempotent — deleting a key that was
// never persisted is treated as success (no audit row written, since
// nothing actually changed) so retries from the UI can't pile up
// noise. Mirrors Update's cache+pubsub+audit+side-effect plumbing on
// the success path so other replicas drop the entry too.
//
// Unknown keys still 400 — we don't want a typo on the URL to silently
// pretend it cleared something.
func (s *systemSettingService) Reset(ctx context.Context, key string) error {
	spec, ok := registry[key]
	if !ok {
		return fmt.Errorf("unknown setting key %q", key)
	}

	// Capture pre-image for the audit log before the row vanishes.
	prev, _ := s.repo.Get(ctx, key)

	deleted, err := s.repo.Delete(ctx, key)
	if err != nil {
		return fmt.Errorf("delete system setting %q: %w", key, err)
	}

	// Drop from the local cache regardless of `deleted` so a stale
	// entry from before the row existed (shouldn't happen, but cheap
	// to defend) gets cleared too.
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()

	// Side-effect bridges and pubsub fire even for no-op resets so
	// peers that may have a stale cached row converge. publishChange
	// is best-effort; reload() on the peer side handles "row absent"
	// by deleting the cache entry, which is what we want here.
	s.dispatchSideEffects(ctx, key)
	s.publishChange(ctx, key)

	// Only emit an audit row on real deletions. The new_value field
	// is intentionally null to flag this as a reset (vs an Update
	// which always writes a concrete new_value).
	if deleted && prev != nil {
		s.emitChangeAudit(ctx, key, spec.Type, prev.Value, nil)
	}
	return nil
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

// validateRegistryEntry runs key-specific structural validation that
// goes beyond the type contract enforced by encodeForType. It is called
// by Update right after the type/enum checks; nil error means the value
// is acceptable for persistence.
//
// Adding new validators:
//   - keep them strict (reject silently-broken input rather than fixing
//     it server-side — the user should see what they typed),
//   - keep them deterministic (no DNS lookups, no env reads),
//   - keep error messages user-facing (the handler surfaces them as the
//     400 body verbatim).
func validateRegistryEntry(key string, rawValue any) error {
	switch key {
	case "asynq.concurrency":
		n, err := coerceToPositiveInt64(rawValue)
		if err != nil {
			return err
		}
		if n <= 0 {
			return errors.New("concurrency must be a positive integer")
		}
	case "ssrf.whitelist":
		// Coerce into the same shape encodeForType produced. We don't
		// look at the encoded JSON because that's already canonicalised
		// — easier to validate the raw input the user typed.
		entries, err := coerceToStringSlice(rawValue)
		if err != nil {
			return err
		}
		return utils.ValidateSSRFWhitelistEntries(entries)
	}
	return nil
}

// coerceToPositiveInt64 accepts int / int64 / float64 from JSON decoding.
func coerceToPositiveInt64(rawValue any) (int64, error) {
	switch v := rawValue.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64:
		if v != float64(int64(v)) {
			return 0, errors.New("expected integer value")
		}
		return int64(v), nil
	default:
		return 0, fmt.Errorf("expected integer, got %T", rawValue)
	}
}

// coerceToStringSlice mirrors the input shapes accepted by
// encodeForType for "string_list": []any of strings, []string, or a
// comma-separated string. Returns the trimmed, empty-stripped result.
//
// Kept private because the only caller is validateRegistryEntry; the
// main encode path has its own (slightly different) coercion that
// preserves rejection of non-string elements at a specific index for
// clearer error messages.
func coerceToStringSlice(rawValue any) ([]string, error) {
	switch v := rawValue.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected string at index %d, got %T", i, item)
			}
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case []string:
		out := make([]string, 0, len(v))
		for _, s := range v {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case string:
		out := make([]string, 0, 4)
		for _, s := range strings.Split(v, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected string array, got %T", rawValue)
	}
}
