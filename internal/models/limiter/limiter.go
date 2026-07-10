// Package limiter governs outbound model calls by provider quota group.
package limiter

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var ErrRequestExceedsTPM = errors.New("estimated request tokens exceed the configured TPM capacity")

// Limits are evaluated for every admission so runtime/model configuration
// changes do not require rebuilding limiter state.
type Limits struct {
	MaxConcurrency                int
	RequestsPerMinute             int
	TokensPerMinute               int
	InteractiveConcurrencyReserve int
}

type Request struct {
	EstimatedTokens int
	Background      bool
}

// Permit holds a concurrency lease and the pre-reserved TPM charge. Complete
// must be called once; actualTokens<=0 keeps the conservative reservation.
type Permit struct {
	once     sync.Once
	complete func(actualTokens int)
}

func noopPermit() *Permit { return &Permit{complete: func(int) {}} }

func (p *Permit) Complete(actualTokens int) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.complete != nil {
			p.complete(actualTokens)
		}
	})
}

func (p *Permit) Release() { p.Complete(0) }

// ModelQuotaLimiter combines concurrency, RPM and TPM admission.
type ModelQuotaLimiter interface {
	Admit(ctx context.Context, key string, limits Limits, req Request) (*Permit, error)
	// Acquire is retained for callers/tests using the old semaphore API.
	Acquire(ctx context.Context, key string, limit int) (release func(), err error)
}

const (
	defaultLeaseTTL     = 5 * time.Minute
	defaultPollInterval = 200 * time.Millisecond
	keyPrefix           = "weknora:modelquota:"
)

// Redis token buckets and both concurrency counters are checked/updated in a
// single script. Rate buckets start full (one minute of burst), refill
// continuously, and are shared across every replica using the same Redis.
var admitScript = redis.NewScript(`
local now = tonumber(ARGV[1])
local lease_ttl = tonumber(ARGV[2])
local token = ARGV[3]
local max_conc = tonumber(ARGV[4])
local rpm = tonumber(ARGV[5])
local tpm = tonumber(ARGV[6])
local estimated = tonumber(ARGV[7])
local background = tonumber(ARGV[8])
local reserve = tonumber(ARGV[9])

redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now)
redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', now)
local total_conc = redis.call('ZCARD', KEYS[1])
local bg_conc = redis.call('ZCARD', KEYS[2])

if max_conc > 0 then
  if total_conc >= max_conc then return {0, 100} end
  local bg_limit = max_conc
  if reserve > 0 and max_conc > 1 then bg_limit = max_conc - math.min(reserve, max_conc - 1) end
  if background == 1 and bg_conc >= bg_limit then return {0, 100} end
end

local req_balance = rpm
local req_ts = now
if rpm > 0 then
  req_balance = tonumber(redis.call('HGET', KEYS[3], 'req_balance') or rpm)
  req_ts = tonumber(redis.call('HGET', KEYS[3], 'req_ts') or now)
  req_balance = math.min(rpm, req_balance + math.max(0, now - req_ts) * rpm / 60000)
end

local tok_balance = tpm
local tok_ts = now
if tpm > 0 then
  tok_balance = tonumber(redis.call('HGET', KEYS[3], 'tok_balance') or tpm)
  tok_ts = tonumber(redis.call('HGET', KEYS[3], 'tok_ts') or now)
  tok_balance = math.min(tpm, tok_balance + math.max(0, now - tok_ts) * tpm / 60000)
end

local wait_ms = 0
if rpm > 0 and req_balance < 1 then wait_ms = math.max(wait_ms, math.ceil((1 - req_balance) * 60000 / rpm)) end
if tpm > 0 and tok_balance < estimated then wait_ms = math.max(wait_ms, math.ceil((estimated - tok_balance) * 60000 / tpm)) end

if rpm > 0 then redis.call('HSET', KEYS[3], 'req_balance', req_balance, 'req_ts', now) end
if tpm > 0 then redis.call('HSET', KEYS[3], 'tok_balance', tok_balance, 'tok_ts', now) end
redis.call('PEXPIRE', KEYS[3], 120000)
if wait_ms > 0 then return {0, wait_ms} end

if rpm > 0 then redis.call('HSET', KEYS[3], 'req_balance', req_balance - 1) end
if tpm > 0 then redis.call('HSET', KEYS[3], 'tok_balance', tok_balance - estimated) end
if max_conc > 0 then
  redis.call('ZADD', KEYS[1], now + lease_ttl, token)
  redis.call('PEXPIRE', KEYS[1], lease_ttl * 2)
  if background == 1 then
    redis.call('ZADD', KEYS[2], now + lease_ttl, token)
    redis.call('PEXPIRE', KEYS[2], lease_ttl * 2)
  end
end
return {1, 0}
`)

var reconcileScript = redis.NewScript(`
redis.call('ZREM', KEYS[1], ARGV[1])
redis.call('ZREM', KEYS[2], ARGV[1])
local tpm = tonumber(ARGV[2])
local reserved = tonumber(ARGV[3])
local actual = tonumber(ARGV[4])
if tpm > 0 and actual > 0 then
  local balance = tonumber(redis.call('HGET', KEYS[3], 'tok_balance') or 0)
  balance = math.min(tpm, balance + reserved - actual)
  redis.call('HSET', KEYS[3], 'tok_balance', balance)
  redis.call('PEXPIRE', KEYS[3], 120000)
end
return 1
`)

type redisLimiter struct {
	rdb          *redis.Client
	ttl          time.Duration
	pollInterval time.Duration
}

func NewRedisLimiter(rdb *redis.Client) ModelQuotaLimiter {
	return &redisLimiter{rdb: rdb, ttl: defaultLeaseTTL, pollInterval: defaultPollInterval}
}

func (l *redisLimiter) Acquire(ctx context.Context, key string, limit int) (func(), error) {
	permit, err := l.Admit(ctx, key, Limits{MaxConcurrency: limit}, Request{})
	if err != nil {
		return nil, err
	}
	return permit.Release, nil
}

func (l *redisLimiter) Admit(ctx context.Context, key string, limits Limits, req Request) (*Permit, error) {
	if l == nil || l.rdb == nil || key == "" || limitsDisabled(limits) {
		return noopPermit(), nil
	}
	if limits.TokensPerMinute > 0 && req.EstimatedTokens > limits.TokensPerMinute {
		return nil, fmt.Errorf("%w: estimated=%d tpm=%d", ErrRequestExceedsTPM, req.EstimatedTokens, limits.TokensPerMinute)
	}

	base := redisKeyBase(key)
	keys := []string{base + ":inflight", base + ":background", base + ":buckets"}
	token := uuid.NewString()
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		now := time.Now().UnixMilli()
		background := 0
		if req.Background {
			background = 1
		}
		result, err := admitScript.Run(ctx, l.rdb, keys,
			now, l.ttl.Milliseconds(), token,
			limits.MaxConcurrency, limits.RequestsPerMinute, limits.TokensPerMinute,
			req.EstimatedTokens, background, limits.InteractiveConcurrencyReserve,
		).Int64Slice()
		if err != nil {
			return nil, fmt.Errorf("model quota admission failed for %s: %w", key, err)
		}
		if len(result) > 0 && result[0] == 1 {
			return l.hold(keys, token, limits, req.EstimatedTokens), nil
		}
		wait := l.pollInterval
		if len(result) > 1 && result[1] > 0 {
			wait = time.Duration(result[1]) * time.Millisecond
			if wait > time.Second {
				wait = time.Second
			}
		}
		timer.Reset(wait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// redisKeyBase hashes user-configured group names and wraps the digest in a
// Redis Cluster hash tag so all keys used by the atomic Lua script are placed
// in the same slot.
func redisKeyBase(key string) string {
	digest := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%s{%x}", keyPrefix, digest[:16])
}

func (l *redisLimiter) hold(keys []string, token string, limits Limits, reserved int) *Permit {
	stop := make(chan struct{})
	if limits.MaxConcurrency > 0 {
		go func() {
			ticker := time.NewTicker(l.ttl / 3)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					now := time.Now().UnixMilli()
					expires := float64(now + l.ttl.Milliseconds())
					bg := context.Background()
					_ = l.rdb.ZAddXX(bg, keys[0], redis.Z{Score: expires, Member: token}).Err()
					_ = l.rdb.ZAddXX(bg, keys[1], redis.Z{Score: expires, Member: token}).Err()
					_ = l.rdb.PExpire(bg, keys[0], l.ttl*2).Err()
					_ = l.rdb.PExpire(bg, keys[1], l.ttl*2).Err()
				}
			}
		}()
	}
	return &Permit{complete: func(actual int) {
		close(stop)
		if _, err := reconcileScript.Run(context.Background(), l.rdb, keys,
			token, limits.TokensPerMinute, reserved, actual).Result(); err != nil {
			logger.Warnf(context.Background(), "[ModelQuota] reconcile failed for token=%s: %v", token, err)
		}
	}}
}

func limitsDisabled(l Limits) bool {
	return l.MaxConcurrency <= 0 && l.RequestsPerMinute <= 0 && l.TokensPerMinute <= 0
}
