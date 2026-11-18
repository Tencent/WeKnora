package admission

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/common/redislock"
	"github.com/redis/go-redis/v9"
)

const redisNowLua = "local t = redis.call('TIME'); " +
	"local now = tonumber(t[1]) * 1000 + math.floor(tonumber(t[2]) / 1000)"

// KEYS[1] ops zset, KEYS[2] closed flag. ARGV[1] member, ARGV[2] ttl ms.
var holdScript = redis.NewScript(redisNowLua + `
if redis.call('EXISTS', KEYS[2]) == 1 then return redis.error_reply('ERR_SCOPE_CLOSED') end
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZADD', KEYS[1], now + tonumber(ARGV[2]), ARGV[1])
-- The zset holds every member's lease; only ever extend its lifetime, or a
-- short hold would expire the key under a longer, still-live member.
local keep = tonumber(ARGV[2]) * 2
if redis.call('PTTL', KEYS[1]) < keep then redis.call('PEXPIRE', KEYS[1], keep) end
return 1
`)

// Renewal only extends a member that is still live. An expired member is
// removed and reported lost: another replica may already have acted on its
// absence, so it must never come back. Renewing while the scope is closed is
// allowed — it protects work the closer is waiting for.
var renewScript = redis.NewScript(redisNowLua + `
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if (not score) or tonumber(score) <= now then
  redis.call('ZREM', KEYS[1], ARGV[1])
  return 0
end
redis.call('ZADD', KEYS[1], 'XX', now + tonumber(ARGV[2]), ARGV[1])
-- The zset holds every member's lease; only ever extend its lifetime, or a
-- short hold would expire the key under a longer, still-live member.
local keep = tonumber(ARGV[2]) * 2
if redis.call('PTTL', KEYS[1]) < keep then redis.call('PEXPIRE', KEYS[1], keep) end
return 1
`)

// ARGV: exempt members.
var activeExceptScript = redis.NewScript(redisNowLua + `
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
local members = redis.call('ZRANGEBYSCORE', KEYS[1], now, '+inf')
local skip = {}
for i = 1, #ARGV do skip[ARGV[i]] = true end
local n = 0
for _, m in ipairs(members) do
  if not skip[m] then n = n + 1 end
end
return n
`)

// RedisRegistry is the distributed admission registry.
type RedisRegistry struct {
	client redis.UniversalClient
	prefix string
}

// NewRedis builds a registry whose keys start with keyPrefix, e.g.
// "weknora:sandbox:user:", matching the user binding keys.
func NewRedis(client redis.UniversalClient, keyPrefix string) *RedisRegistry {
	return &RedisRegistry{client: client, prefix: keyPrefix}
}

func (r *RedisRegistry) key(scope Scope, suffix string) string {
	return r.prefix + "{" + scope.Tag + "}:" + scope.Name + ":" + suffix
}

// Hold registers member until Release or until the lease can no longer be renewed.
func (r *RedisRegistry) Hold(ctx context.Context, scope Scope, member string, ttl time.Duration) (Lease, error) {
	if ttl <= 0 {
		ttl = DefaultHoldTTL
	}
	ops, closed := r.key(scope, "ops"), r.key(scope, "closed")
	sent := time.Now()
	if err := holdScript.Run(ctx, r.client, []string{ops, closed}, member, ttl.Milliseconds()).Err(); err != nil {
		if strings.Contains(err.Error(), "ERR_SCOPE_CLOSED") {
			return nil, ErrClosed
		}
		return nil, fmt.Errorf("admission: hold %s: %w", member, err)
	}
	l := &lease{signal: newSignal(), member: member}
	l.extend(sent, ttl)
	renewCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	l.release = func() {
		stop()
		relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.client.ZRem(relCtx, ops, member).Err()
	}
	go r.renewLease(renewCtx, l, ops, ttl)
	return l, nil
}

// renewLease confirms the lease every ttl/3. Each request is bounded by the
// local deadline; a failed or hung renewal leaves the deadline where it was,
// and the deadline timer reports the loss on time.
func (r *RedisRegistry) renewLease(ctx context.Context, l *lease, ops string, ttl time.Duration) {
	ticker := time.NewTicker(renewInterval(ttl))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.Lost():
			return
		case <-ticker.C:
			sent := time.Now()
			callCtx, cancel := context.WithDeadline(ctx, l.currentDeadline())
			n, err := renewScript.Run(callCtx, r.client, []string{ops}, l.member, ttl.Milliseconds()).Int()
			cancel()
			switch {
			case err == nil && n == 1:
				l.extend(sent, ttl)
			case err == nil:
				l.markLost()
				return
			}
		}
	}
}

// ActiveExcept counts members of scope other than except.
func (r *RedisRegistry) ActiveExcept(ctx context.Context, scope Scope, except ...string) (int, error) {
	args := make([]any, len(except))
	for i, m := range except {
		args[i] = m
	}
	n, err := activeExceptScript.Run(ctx, r.client, []string{r.key(scope, "ops")}, args...).Int()
	if err != nil {
		return 0, fmt.Errorf("admission: count %s/%s: %w", scope.Tag, scope.Name, err)
	}
	return n, nil
}

// Close stops new holds on scope until the barrier is released.
func (r *RedisRegistry) Close(ctx context.Context, scope Scope, ttl time.Duration) (Barrier, error) {
	token, err := redislock.NewToken()
	if err != nil {
		return nil, err
	}
	key := r.key(scope, "closed")
	sent := time.Now()
	ok, err := redislock.TryAcquire(ctx, r.client, key, token, ttl)
	if err != nil {
		return nil, fmt.Errorf("admission: close %s/%s: %w", scope.Tag, scope.Name, err)
	}
	if !ok {
		return nil, ErrClosed
	}
	b := &barrier{signal: newSignal()}
	b.extend(sent, ttl)
	renewCtx, stop := context.WithCancel(context.WithoutCancel(ctx))
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		ticker := time.NewTicker(renewInterval(ttl))
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-b.Lost():
				return
			case <-ticker.C:
				sent := time.Now()
				callCtx, cancel := context.WithDeadline(renewCtx, b.currentDeadline())
				renewed, err := redislock.Renew(callCtx, r.client, key, token, ttl)
				cancel()
				switch {
				case err == nil && renewed:
					b.extend(sent, ttl)
				case err == nil:
					b.markLost()
					return
				}
			}
		}
	}()
	// Wait for the renewal goroutine to leave before touching the key, so a
	// PEXPIRE cannot land after PERSIST and put the TTL back.
	finishKey := func(persist bool) {
		stop()
		<-renewDone
		relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if persist {
			_ = r.client.Persist(relCtx, key).Err()
			return
		}
		_, _ = redislock.Release(relCtx, r.client, key, token)
	}
	b.release = func() { finishKey(false) }
	b.seal = func() { finishKey(true) }
	return b, nil
}

// renewInterval is how often a holder reconfirms. It stays inside the
// deadline (ttl - ttl/6) so one hung renewal still loses the lease on time.
func renewInterval(ttl time.Duration) time.Duration {
	every := ttl / 3
	if every <= 0 {
		return time.Millisecond
	}
	return every
}

var _ Registry = (*RedisRegistry)(nil)
