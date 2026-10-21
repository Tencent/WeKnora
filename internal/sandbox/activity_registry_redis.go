package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
)

// activityKeyGrace keeps the activity hash around briefly after its last
// deadline so a read racing the expiry still sees a well-formed key.
const activityKeyGrace = 60 * time.Second

// activityLiveCountLua defines now_ms() and live_count(key, now_ms): the
// latter deletes expired fields of the activity hash and returns how many
// remain. The turn-lease scripts in session_binding_redis.go prepend it so
// they can refuse a rebuild or a rewind while items are live.
const activityLiveCountLua = `
local function now_ms()
	local t = redis.call('TIME')
	return tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)
end
local function live_count(key, now)
	local raw = redis.call('HGETALL', key)
	local live = 0
	for i = 1, #raw, 2 do
		local v = cjson.decode(raw[i + 1])
		if tonumber(v['deadline_ms']) <= now then
			redis.call('HDEL', key, raw[i])
		else
			live = live + 1
		end
	end
	return live
end
`

// KEYS[1] activity hash. ARGV[1] id, ARGV[2] kind, ARGV[3] title,
// ARGV[4] started_at_ms, ARGV[5] lease_ms, ARGV[6] kind cap (<=0 unlimited),
// ARGV[7] key grace ms. Returns 1 on success, 0 when the cap is reached.
var addActivityScript = redis.NewScript(activityLiveCountLua + `
local now = now_ms()
live_count(KEYS[1], now)
local deadline = now + tonumber(ARGV[5])
local existing = redis.call('HGET', KEYS[1], ARGV[1])
if existing then
	local v = cjson.decode(existing)
	v['deadline_ms'] = deadline
	redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(v))
else
	local cap = tonumber(ARGV[6])
	if cap > 0 then
		local raw = redis.call('HGETALL', KEYS[1])
		local same = 0
		for i = 1, #raw, 2 do
			if cjson.decode(raw[i + 1])['kind'] == ARGV[2] then
				same = same + 1
			end
		end
		if same >= cap then
			return 0
		end
	end
	redis.call('HSET', KEYS[1], ARGV[1], cjson.encode({
		kind = ARGV[2], title = ARGV[3],
		started_at_ms = tonumber(ARGV[4]), deadline_ms = deadline,
	}))
end
local raw = redis.call('HGETALL', KEYS[1])
local maxd = deadline
for i = 1, #raw, 2 do
	local d = tonumber(cjson.decode(raw[i + 1])['deadline_ms'])
	if d > maxd then maxd = d end
end
redis.call('PEXPIRE', KEYS[1], maxd - now + tonumber(ARGV[7]))
return 1
`)

// KEYS[1] activity hash. ARGV[1] id, ARGV[2] lease_ms, ARGV[3] key grace ms.
// Returns 1 when renewed, 0 when the item is missing or expired.
var renewActivityScript = redis.NewScript(activityLiveCountLua + `
local now = now_ms()
live_count(KEYS[1], now)
local existing = redis.call('HGET', KEYS[1], ARGV[1])
if not existing then
	return 0
end
local v = cjson.decode(existing)
v['deadline_ms'] = now + tonumber(ARGV[2])
redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(v))
local raw = redis.call('HGETALL', KEYS[1])
local maxd = 0
for i = 1, #raw, 2 do
	local d = tonumber(cjson.decode(raw[i + 1])['deadline_ms'])
	if d > maxd then maxd = d end
end
redis.call('PEXPIRE', KEYS[1], maxd - now + tonumber(ARGV[3]))
return 1
`)

// KEYS[1] activity hash. Returns the live fields as a flat [id, json, ...] list.
var listActivityScript = redis.NewScript(activityLiveCountLua + `
live_count(KEYS[1], now_ms())
return redis.call('HGETALL', KEYS[1])
`)

type redisActivityValue struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	StartedAtMS int64  `json:"started_at_ms"`
	DeadlineMS  int64  `json:"deadline_ms"`
}

// RedisActivityRegistry is the multi-replica ActivityRegistry. It shares the
// binding store's client and hash tag so the turn-lease scripts can read it.
type RedisActivityRegistry struct {
	store *RedisSessionSandboxBindingStore
}

var _ interfaces.ActivityRegistry = (*RedisActivityRegistry)(nil)

// NewRedisActivityRegistry returns a registry backed by store's Redis client.
func NewRedisActivityRegistry(store *RedisSessionSandboxBindingStore) *RedisActivityRegistry {
	return &RedisActivityRegistry{store: store}
}

func (s *RedisSessionSandboxBindingStore) activityKey(key SessionSandboxKey) string {
	return "weknora:sandbox:session:{" + s.hashTag(key) + "}:activity"
}

func (r *RedisActivityRegistry) key(s interfaces.SessionRef) (string, error) {
	k := SessionSandboxKey{TenantID: s.TenantID, SessionID: s.SessionID}
	if err := k.Validate(); err != nil {
		return "", err
	}
	if strings.TrimSpace(s.SessionID) == "" {
		return "", errors.New("activity registry requires a session id")
	}
	return r.store.activityKey(k), nil
}

// Add implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) Add(
	ctx context.Context, s interfaces.SessionRef, a interfaces.BackgroundActivity, lease time.Duration, kindCap int,
) error {
	key, err := r.key(s)
	if err != nil {
		return err
	}
	ok, err := addActivityScript.Run(ctx, r.store.client, []string{key},
		a.ID, a.Kind, a.Title, a.StartedAt.UnixMilli(), lease.Milliseconds(), kindCap,
		activityKeyGrace.Milliseconds(),
	).Int64()
	if err != nil {
		return fmt.Errorf("add background activity: %w", err)
	}
	if ok == 0 {
		return interfaces.ErrActivityLimit
	}
	return nil
}

// Renew implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) Renew(
	ctx context.Context, s interfaces.SessionRef, id string, lease time.Duration,
) (bool, error) {
	key, err := r.key(s)
	if err != nil {
		return false, err
	}
	ok, err := renewActivityScript.Run(ctx, r.store.client, []string{key},
		id, lease.Milliseconds(), activityKeyGrace.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("renew background activity: %w", err)
	}
	return ok == 1, nil
}

// Remove implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) Remove(ctx context.Context, s interfaces.SessionRef, id string) error {
	key, err := r.key(s)
	if err != nil {
		return err
	}
	if err := r.store.client.HDel(ctx, key, id).Err(); err != nil {
		return fmt.Errorf("remove background activity: %w", err)
	}
	return nil
}

// List implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) List(
	ctx context.Context, s interfaces.SessionRef,
) ([]interfaces.BackgroundActivity, error) {
	key, err := r.key(s)
	if err != nil {
		return nil, err
	}
	flat, err := listActivityScript.Run(ctx, r.store.client, []string{key}).StringSlice()
	if err != nil {
		return nil, fmt.Errorf("list background activity: %w", err)
	}
	out := make([]interfaces.BackgroundActivity, 0, len(flat)/2)
	for i := 0; i+1 < len(flat); i += 2 {
		var v redisActivityValue
		if err := json.Unmarshal([]byte(flat[i+1]), &v); err != nil {
			return nil, fmt.Errorf("decode background activity %s: %w", flat[i], err)
		}
		out = append(out, interfaces.BackgroundActivity{
			ID: flat[i], Kind: v.Kind, Title: v.Title,
			StartedAt: time.UnixMilli(v.StartedAtMS).UTC(),
		})
	}
	return out, nil
}
