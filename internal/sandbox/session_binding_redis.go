package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/common/redislock"
)

const (
	redisLifecycleLockLease         = 60 * time.Second
	redisLifecycleLockRenewInterval = 20 * time.Second
)

var deleteBindingIfMatchScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local value = cjson.decode(raw)
local provider = value['provider']
if provider == ARGV[1] and value['sandbox_id'] == ARGV[2] then
	return redis.call('DEL', KEYS[1])
end
return 0
`)

var markBindingStaleIfMatchScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local value = cjson.decode(raw)
if value['provider'] ~= ARGV[1] or value['sandbox_id'] ~= ARGV[2] then
	return 0
end
redis.call('SET', KEYS[1], ARGV[3])
return 1
`)

// Patches only traffic_access_token so a concurrent stale-mark cannot be
// overwritten by a full-document replace. ARGV[3] is the new token.
//
// The field is rewritten in the stored JSON text rather than via
// cjson.encode of the whole object: Redis's cjson turns integers into
// x.0, and encoding/json then refuses those into uint64 (tenant_id).
var replaceTrafficTokenIfMatchScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local value = cjson.decode(raw)
if value['provider'] ~= ARGV[1] or value['sandbox_id'] ~= ARGV[2] then
	return 0
end
if value['traffic_access_token'] == ARGV[3] then
	return 0
end
local encoded = string.gsub(cjson.encode(ARGV[3]), '%%', '%%%%')
local updated, n = string.gsub(raw, '"traffic_access_token"%s*:%s*".-"', '"traffic_access_token":'..encoded, 1)
if n == 0 then
	updated, n = string.gsub(raw, '}(%s*)$', ',"traffic_access_token":'..encoded..'}%1', 1)
	if n == 0 then
		return 0
	end
end
redis.call('SET', KEYS[1], updated)
return 1
`)

// sessionTurnLeaseTTL bounds a leaked turn if EndSessionTurn never runs
// (process crash). After it expires the next resolve may rebuild a stale
// image, which is what we want once no turn is actually using the sandbox.
const sessionTurnLeaseTTL = 30 * time.Minute

var beginTurnScript = redis.NewScript(`
local refs = redis.call('HINCRBY', KEYS[1], 'refs', 1)
if refs == 1 then
	redis.call('HSET', KEYS[1], 'rebuild', '1')
end
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return refs
`)

var endTurnScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
local refs = redis.call('HINCRBY', KEYS[1], 'refs', -1)
if refs <= 0 then
	redis.call('DEL', KEYS[1])
	return 0
end
return refs
`)

var consumeTurnRebuildScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then return 0 end
redis.call('HSET', KEYS[1], 'rebuild', '0')
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return 1
`)

// redisBindingScanCount is the SCAN batch size. Bindings are one small key per
// live session, so a workspace's whole set is normally a single batch.
const redisBindingScanCount = 200

// RedisSessionSandboxBindingStore is the authoritative distributed store for
// persistent remote-session bindings.
type RedisSessionSandboxBindingStore struct {
	client            redis.UniversalClient
	namespace         string
	lockLease         time.Duration
	lockRenewInterval time.Duration
}

// NewRedisSessionSandboxBindingStore creates a fail-closed Redis store.
func NewRedisSessionSandboxBindingStore(
	client redis.UniversalClient,
	namespace string,
) (*RedisSessionSandboxBindingStore, error) {
	if client == nil {
		return nil, errors.New("sandbox binding Redis client is required")
	}
	namespace = strings.TrimSpace(namespace)
	if err := validateRedisNamespace(namespace); err != nil {
		return nil, err
	}
	return &RedisSessionSandboxBindingStore{
		client:            client,
		namespace:         namespace,
		lockLease:         redisLifecycleLockLease,
		lockRenewInterval: redisLifecycleLockRenewInterval,
	}, nil
}

// Get returns the current binding, or nil when the session is unbound.
func (s *RedisSessionSandboxBindingStore) Get(
	ctx context.Context,
	key SessionSandboxKey,
) (*SessionSandboxBinding, error) {
	if err := key.Validate(); err != nil {
		return nil, err
	}
	raw, err := s.client.Get(ctx, s.bindingKey(key)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get sandbox binding: %w", err)
	}

	var binding SessionSandboxBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return nil, fmt.Errorf("decode sandbox binding: %w", err)
	}
	if err := binding.Validate(key); err != nil {
		return nil, fmt.Errorf("validate sandbox binding: %w", err)
	}
	return &binding, nil
}

// Create stores a validated current-schema binding with SET NX and no
// expiration.
func (s *RedisSessionSandboxBindingStore) Create(
	ctx context.Context,
	key SessionSandboxKey,
	binding SessionSandboxBinding,
) (bool, error) {
	if err := binding.Validate(key); err != nil {
		return false, err
	}
	raw, err := json.Marshal(binding)
	if err != nil {
		return false, fmt.Errorf("encode sandbox binding: %w", err)
	}
	created, err := s.client.SetNX(ctx, s.bindingKey(key), raw, 0).Result()
	if err != nil {
		return false, fmt.Errorf("create sandbox binding: %w", err)
	}
	return created, nil
}

// DeleteIfMatch atomically deletes only the expected provider and sandbox ID.
func (s *RedisSessionSandboxBindingStore) DeleteIfMatch(
	ctx context.Context,
	key SessionSandboxKey,
	provider RemoteProvider,
	sandboxID string,
) (bool, error) {
	if err := validateBindingMatch(key, provider, sandboxID); err != nil {
		return false, err
	}
	deleted, err := deleteBindingIfMatchScript.Run(
		ctx,
		s.client,
		[]string{s.bindingKey(key)},
		string(provider),
		sandboxID,
	).Int64()
	if err != nil {
		return false, fmt.Errorf("delete sandbox binding: %w", err)
	}
	return deleted != 0, nil
}

// ReplaceTrafficTokenIfMatch patches the inbound credential only while the
// stored binding still names expected's provider and sandbox.
func (s *RedisSessionSandboxBindingStore) ReplaceTrafficTokenIfMatch(
	ctx context.Context,
	key SessionSandboxKey,
	expected SessionSandboxBinding,
	token string,
) (bool, error) {
	if err := validateBindingMatch(key, expected.Provider, expected.SandboxID); err != nil {
		return false, err
	}
	if token == "" {
		return false, nil
	}
	wrote, err := replaceTrafficTokenIfMatchScript.Run(
		ctx,
		s.client,
		[]string{s.bindingKey(key)},
		string(expected.Provider),
		expected.SandboxID,
		token,
	).Int64()
	if err != nil {
		return false, fmt.Errorf("replace sandbox inbound token: %w", err)
	}
	return wrote != 0, nil
}

// WithLifecycleLock serializes create, recover, replace, and delete transitions
// across all WeKnora processes sharing Redis.
func (s *RedisSessionSandboxBindingStore) WithLifecycleLock(
	ctx context.Context,
	key SessionSandboxKey,
	fn func(context.Context) error,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if fn == nil {
		return errors.New("sandbox lifecycle lock callback is required")
	}
	return redislock.WithRenewableLock(
		ctx,
		s.client,
		s.lockKey(key),
		s.lockLease,
		s.lockRenewInterval,
		func(lockCtx context.Context) error {
			return fn(withLifecycleOwnershipContext(
				lockCtx,
				redislock.OwnershipContext(lockCtx),
			))
		},
	)
}

// InvalidateByConfig marks every binding of one workspace's config stale.
func (s *RedisSessionSandboxBindingStore) InvalidateByConfig(
	ctx context.Context,
	tenantID uint64,
	configID string,
) (int, error) {
	return invalidateBindingsByConfig(ctx, s, tenantID, configID)
}

// listTenantBindingKeys SCANs the workspace's binding keys.
//
// SCAN rather than a maintained index: the index would be a second key that
// every create and delete has to keep in step across processes, and a drifted
// index silently under-reports exactly when it matters. The cost is bounded
// because the pattern is anchored on the workspace's own prefix.
//
// A single-node Redis (what the container wires) answers this completely. On a
// Redis Cluster, SCAN reaches one node, so bindings living on the others would
// go unmarked and their sessions would keep the previous image until they end.
func (s *RedisSessionSandboxBindingStore) listTenantBindingKeys(
	ctx context.Context,
	tenantID uint64,
) ([]SessionSandboxKey, error) {
	prefix := fmt.Sprintf(
		"weknora:sandbox:session:{%s:%d:", s.namespace, tenantID,
	)
	const suffix = "}:binding"
	pattern := escapeRedisGlob(prefix) + "*" + suffix

	var keys []SessionSandboxKey
	var cursor uint64
	for {
		batch, next, err := s.client.Scan(ctx, cursor, pattern, redisBindingScanCount).Result()
		if err != nil {
			return nil, fmt.Errorf("scan sandbox bindings: %w", err)
		}
		for _, raw := range batch {
			sessionID := strings.TrimSuffix(strings.TrimPrefix(raw, prefix), suffix)
			key := SessionSandboxKey{TenantID: tenantID, SessionID: sessionID}
			if key.Validate() != nil {
				continue
			}
			keys = append(keys, key)
		}
		if next == 0 {
			return keys, nil
		}
		cursor = next
	}
}

// markBindingStale writes the marked binding back only while the stored one
// still names the same sandbox.
func (s *RedisSessionSandboxBindingStore) markBindingStale(
	ctx context.Context,
	key SessionSandboxKey,
	expected SessionSandboxBinding,
	staleAt time.Time,
) (bool, error) {
	if err := validateBindingMatch(key, expected.Provider, expected.SandboxID); err != nil {
		return false, err
	}
	marked := expected
	marked.StaleAt = &staleAt
	payload, err := json.Marshal(marked)
	if err != nil {
		return false, fmt.Errorf("encode stale sandbox binding: %w", err)
	}
	wrote, err := markBindingStaleIfMatchScript.Run(
		ctx,
		s.client,
		[]string{s.bindingKey(key)},
		string(expected.Provider),
		expected.SandboxID,
		payload,
	).Int64()
	if err != nil {
		return false, fmt.Errorf("mark sandbox binding stale: %w", err)
	}
	return wrote != 0, nil
}

// escapeRedisGlob quotes the characters SCAN's MATCH treats as wildcards. The
// namespace is operator-supplied and only screened for braces and control
// characters, so a namespace containing "*" would otherwise widen the pattern
// past the workspace it is meant to anchor.
func escapeRedisGlob(literal string) string {
	var out strings.Builder
	out.Grow(len(literal))
	for _, r := range literal {
		switch r {
		case '\\', '*', '?', '[', ']', '^':
			out.WriteByte('\\')
		}
		out.WriteRune(r)
	}
	return out.String()
}

// BeginTurn opens a chat-turn lease. The first increment of a session's
// refcount allows the next resolve to rebuild a stale sandbox.
func (s *RedisSessionSandboxBindingStore) BeginTurn(
	ctx context.Context,
	key SessionSandboxKey,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	ttlMS := sessionTurnLeaseTTL.Milliseconds()
	if ttlMS <= 0 {
		ttlMS = (30 * time.Minute).Milliseconds()
	}
	if err := beginTurnScript.Run(ctx, s.client, []string{s.turnKey(key)}, ttlMS).Err(); err != nil {
		return fmt.Errorf("begin sandbox turn lease: %w", err)
	}
	return nil
}

// EndTurn releases one chat-turn lease. The last release drops the lease so
// a later resolve may rebuild a stale sandbox immediately.
func (s *RedisSessionSandboxBindingStore) EndTurn(
	ctx context.Context,
	key SessionSandboxKey,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if err := endTurnScript.Run(ctx, s.client, []string{s.turnKey(key)}).Err(); err != nil {
		return fmt.Errorf("end sandbox turn lease: %w", err)
	}
	return nil
}

// TurnState reports whether a chat turn is open and whether its first
// resolve may still rebuild a stale sandbox.
func (s *RedisSessionSandboxBindingStore) TurnState(
	ctx context.Context,
	key SessionSandboxKey,
) (bool, bool, error) {
	if err := key.Validate(); err != nil {
		return false, false, err
	}
	values, err := s.client.HGetAll(ctx, s.turnKey(key)).Result()
	if err != nil {
		return false, false, fmt.Errorf("read sandbox turn lease: %w", err)
	}
	if len(values) == 0 {
		return false, false, nil
	}
	_ = s.client.PExpire(ctx, s.turnKey(key), sessionTurnLeaseTTL).Err()
	refs, _ := strconv.Atoi(values["refs"])
	if refs <= 0 {
		return false, false, nil
	}
	return true, values["rebuild"] == "1", nil
}

// ConsumeTurnRebuild spends the one rebuild allowed for the current turn.
func (s *RedisSessionSandboxBindingStore) ConsumeTurnRebuild(
	ctx context.Context,
	key SessionSandboxKey,
) error {
	if err := key.Validate(); err != nil {
		return err
	}
	if err := consumeTurnRebuildScript.Run(
		ctx, s.client, []string{s.turnKey(key)}, sessionTurnLeaseTTL.Milliseconds(),
	).Err(); err != nil {
		return fmt.Errorf("consume sandbox turn rebuild: %w", err)
	}
	return nil
}

func (s *RedisSessionSandboxBindingStore) turnKey(key SessionSandboxKey) string {
	return "weknora:sandbox:session:{" + s.hashTag(key) + "}:turn"
}

func (s *RedisSessionSandboxBindingStore) bindingKey(key SessionSandboxKey) string {
	return "weknora:sandbox:session:{" + s.hashTag(key) + "}:binding"
}

func (s *RedisSessionSandboxBindingStore) lockKey(key SessionSandboxKey) string {
	// Keep the historical suffix used by the saved multi-node Cube
	// implementation so rolling upgrades serialize on the same lock.
	return "weknora:sandbox:session:{" + s.hashTag(key) + "}:create-lock"
}

func (s *RedisSessionSandboxBindingStore) hashTag(key SessionSandboxKey) string {
	return fmt.Sprintf("%s:%d:%s", s.namespace, key.TenantID, key.SessionID)
}

var (
	_ tenantBindingScanner  = (*RedisSessionSandboxBindingStore)(nil)
	_ sessionTurnLeaseStore = (*RedisSessionSandboxBindingStore)(nil)
)

func validateRedisNamespace(namespace string) error {
	if strings.ContainsAny(namespace, "{}") {
		return errors.New("WEKNORA_REDIS_NAMESPACE must not contain braces")
	}
	for _, r := range namespace {
		if unicode.IsControl(r) {
			return errors.New("WEKNORA_REDIS_NAMESPACE must not contain control characters")
		}
	}
	return nil
}
