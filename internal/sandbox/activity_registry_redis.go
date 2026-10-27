package sandbox

import (
	"context"
	"encoding/json"
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

// Every write refreshes the hash until its latest member expires. A shared
// machine mirrors each session's leases in a machine hash, in the same script
// and Redis slot, so sibling sessions cannot rebuild underneath them.
const activityWriteLua = `
local function save_activity(key, id, value, now, grace)
	redis.call('HSET', key, id, cjson.encode(value))
	local raw = redis.call('HGETALL', key)
	local maxd = now
	for i = 1, #raw, 2 do
		local d = tonumber(cjson.decode(raw[i + 1])['deadline_ms'])
		if d > maxd then maxd = d end
	end
	redis.call('PEXPIRE', key, maxd - now + grace)
end
`

// KEYS: session activity, machine activity, rewind, machine close, session close.
// ARGV[1] id, ARGV[2] kind, ARGV[3] title,
// ARGV[4] started_at_ms, ARGV[5] lease_ms, ARGV[6] kind cap (<=0 unlimited),
// ARGV[7] key grace ms, ARGV[8] machine member. Returns 1 on success, 0 at the cap.
var addActivityScript = redis.NewScript(activityLiveCountLua + activityWriteLua + `
local now = now_ms()
live_count(KEYS[1], now)
local deadline = now + tonumber(ARGV[5])
local existing = redis.call('HGET', KEYS[1], ARGV[1])
local v
if existing then
	v = cjson.decode(existing)
	v['deadline_ms'] = deadline
else
	if redis.call('EXISTS', KEYS[3]) == 1 then
		return redis.error_reply('ERR_SESSION_REWIND_LOCKED')
	end
	if redis.call('EXISTS', KEYS[4]) == 1 or redis.call('EXISTS', KEYS[5]) == 1 then
		return redis.error_reply('ERR_SCOPE_CLOSED')
	end
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
	v = {
		kind = ARGV[2], title = ARGV[3],
		started_at_ms = tonumber(ARGV[4]), deadline_ms = deadline,
	}
end
save_activity(KEYS[1], ARGV[1], v, now, tonumber(ARGV[7]))
if KEYS[2] ~= KEYS[1] then
	live_count(KEYS[2], now)
	save_activity(KEYS[2], ARGV[8], v, now, tonumber(ARGV[7]))
end
return 1
`)

// KEYS: session activity, machine activity.
// ARGV: id, lease_ms, key grace ms, machine member.
// Returns 1 when renewed, 0 when the item is missing or expired.
var renewActivityScript = redis.NewScript(activityLiveCountLua + activityWriteLua + `
local now = now_ms()
live_count(KEYS[1], now)
local existing = redis.call('HGET', KEYS[1], ARGV[1])
if not existing then
	if KEYS[2] ~= KEYS[1] then redis.call('HDEL', KEYS[2], ARGV[4]) end
	return 0
end
local v = cjson.decode(existing)
v['deadline_ms'] = now + tonumber(ARGV[2])
save_activity(KEYS[1], ARGV[1], v, now, tonumber(ARGV[3]))
if KEYS[2] ~= KEYS[1] then
	live_count(KEYS[2], now)
	save_activity(KEYS[2], ARGV[4], v, now, tonumber(ARGV[3]))
end
return 1
`)

var removeActivityScript = redis.NewScript(`
redis.call('HDEL', KEYS[1], ARGV[1])
if KEYS[2] ~= KEYS[1] then redis.call('HDEL', KEYS[2], ARGV[2]) end
return 1
`)

var countActivityScript = redis.NewScript(activityLiveCountLua + `
return live_count(KEYS[1], now_ms())
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
	store       *RedisSessionSandboxBindingStore
	allocations AllocationDirectory
}

var _ interfaces.ActivityRegistry = (*RedisActivityRegistry)(nil)

// NewRedisActivityRegistry returns a registry backed by store's Redis client.
func NewRedisActivityRegistry(store *RedisSessionSandboxBindingStore) *RedisActivityRegistry {
	return &RedisActivityRegistry{store: store}
}

// WithAllocations uses each session's recorded placement to address shared
// machine leases. Configure it before using the registry.
func (r *RedisActivityRegistry) WithAllocations(allocations AllocationDirectory) *RedisActivityRegistry {
	r.allocations = allocations
	return r
}

func (s *RedisSessionSandboxBindingStore) activityKey(key SessionSandboxKey) string {
	if key.IsUserScoped() && key.SessionID != "" {
		return s.keyBase(key) + ":session:" + key.SessionID + ":activity"
	}
	return s.keyBase(key) + ":activity"
}

func (r *RedisActivityRegistry) key(ctx context.Context, s interfaces.SessionRef) (SessionSandboxKey, error) {
	k := SessionSandboxKey{TenantID: s.TenantID, SessionID: s.SessionID}
	if err := k.Validate(); err != nil {
		return k, err
	}
	if r.allocations != nil {
		a, found, err := r.allocations.SessionAllocation(ctx, s.SessionID)
		if err != nil {
			return k, fmt.Errorf("read background work allocation: %w", err)
		}
		if found && a.IsUser() {
			k.User = a.Machine
		}
	}
	return k, k.Validate()
}

func (r *RedisActivityRegistry) keys(key SessionSandboxKey) []string {
	return []string{r.store.activityKey(key), r.store.activityKey(key.Machine())}
}

func activityMachineMember(key SessionSandboxKey, id string) string {
	return key.SessionID + ":" + id
}

// hasBackgroundActivity includes every sibling session on a shared machine.
func (s *RedisSessionSandboxBindingStore) hasBackgroundActivity(
	ctx context.Context, key SessionSandboxKey,
) (bool, error) {
	n, err := countActivityScript.Run(ctx, s.client, []string{s.activityKey(key.Machine())}).Int64()
	return n > 0, err
}

// Add implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) Add(
	ctx context.Context, s interfaces.SessionRef, a interfaces.BackgroundActivity, lease time.Duration, kindCap int,
) error {
	key, err := r.key(ctx, s)
	if err != nil {
		return err
	}
	keys := append(r.keys(key), r.store.rewindKey(key))
	if key.IsUserScoped() {
		base := r.store.keyBase(key)
		keys = append(keys, base+":machine:closed", base+":session:"+key.SessionID+":closed")
	} else {
		keys = append(keys, r.store.rewindKey(key), r.store.rewindKey(key))
	}
	ok, err := addActivityScript.Run(ctx, r.store.client, keys,
		a.ID, a.Kind, a.Title, a.StartedAt.UnixMilli(), lease.Milliseconds(), kindCap,
		activityKeyGrace.Milliseconds(), activityMachineMember(key, a.ID),
	).Int64()
	if err != nil {
		if isRewindLockedRedisErr(err) {
			return ErrSessionRewindLocked
		}
		if strings.Contains(err.Error(), "ERR_SCOPE_CLOSED") {
			return ErrSessionWorkspaceBusy
		}
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
	key, err := r.key(ctx, s)
	if err != nil {
		return false, err
	}
	ok, err := renewActivityScript.Run(ctx, r.store.client, r.keys(key),
		id, lease.Milliseconds(), activityKeyGrace.Milliseconds(), activityMachineMember(key, id),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("renew background activity: %w", err)
	}
	return ok == 1, nil
}

// Remove implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) Remove(ctx context.Context, s interfaces.SessionRef, id string) error {
	key, err := r.key(ctx, s)
	if err != nil {
		return err
	}
	err = removeActivityScript.Run(ctx, r.store.client, r.keys(key), id, activityMachineMember(key, id)).Err()
	if err != nil {
		return fmt.Errorf("remove background activity: %w", err)
	}
	return nil
}

// List implements interfaces.ActivityRegistry.
func (r *RedisActivityRegistry) List(
	ctx context.Context, s interfaces.SessionRef,
) ([]interfaces.BackgroundActivity, error) {
	key, err := r.key(ctx, s)
	if err != nil {
		return nil, err
	}
	flat, err := listActivityScript.Run(ctx, r.store.client, []string{r.store.activityKey(key)}).StringSlice()
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
