package redislock_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/Tencent/WeKnora/internal/common/redislock"
)

// RLOCK-F1: TryAcquire under Redis error returns (false, err) — no panic.
func TestTryAcquire_RedisError_ReturnsError(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	mr.SetError("boom")

	token, err := redislock.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	acquired, err := redislock.TryAcquire(context.Background(), client, "lock", token, time.Second)
	if acquired {
		t.Fatal("must not report acquired under Redis error")
	}
	if err == nil {
		t.Fatal("must surface Redis error")
	}
	mr.SetError("")
}

// RLOCK-F2: renew failure (Redis dies mid-callback) cancels the callback
// context and surfaces an ownership-lost style error.
func TestRenew_RedisError_CancelsCallback(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := redislock.WithRenewableLock(ctx, client, "renew-lock", time.Minute, 50*time.Millisecond, func(fnCtx context.Context) error {
		// Kill Redis while the callback runs: the next renew tick must fail and
		// cancel fnCtx via the ownership channel.
		mr.Close()
		<-fnCtx.Done()
		return nil
	})
	if err == nil {
		t.Fatal("renew-failure path must surface an error (ownership semantics)")
	}
}
