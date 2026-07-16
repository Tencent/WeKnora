package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		fn,
	)
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
