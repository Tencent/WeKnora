package admission_test

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/sandbox/admission"
)

// integrationEnv returns an integration dependency's address. Missing, the
// test is skipped — unless WEKNORA_REQUIRE_INTEGRATION=1, which turns the
// skip into a failure so an enablement run cannot pass by skipping.
func integrationEnv(t *testing.T, name string) string {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(name))
	if v != "" {
		return v
	}
	if os.Getenv("WEKNORA_REQUIRE_INTEGRATION") == "1" {
		t.Fatalf("%s is required when WEKNORA_REQUIRE_INTEGRATION=1", name)
	}
	t.Skipf("set %s to run against a real dependency", name)
	return ""
}

// realRedisPair dials WEKNORA_TEST_REDIS_ADDR twice. prefix isolates this run.
func realRedisPair(t *testing.T) (c1, c2 *admission.RedisRegistry, raw *redis.Client, prefix string) {
	t.Helper()
	addr := integrationEnv(t, "WEKNORA_TEST_REDIS_ADDR")
	prefix = "it-" + uuid.NewString() + ":"
	open := func() *redis.Client {
		rc := redis.NewClient(&redis.Options{Addr: addr})
		t.Cleanup(func() { _ = rc.Close() })
		return rc
	}
	left, right := open(), open()
	raw = open()
	return admission.NewRedis(left, prefix), admission.NewRedis(right, prefix), raw, prefix
}

// redisKey is RedisRegistry's key layout. The method is unexported; the
// integration test builds the same string so it can inject faults.
func redisKey(prefix string, scope admission.Scope, suffix string) string {
	return prefix + "{" + scope.Tag + "}:" + scope.Name + ":" + suffix
}

func TestRedisRealTwoClientsShareAdmission(t *testing.T) {
	c1, c2, _, _ := realRedisPair(t)
	ctx := t.Context()
	scope := admission.MachineScope("d-" + uuid.NewString())
	member := "s:a:pty:1"

	lease, err := c1.Hold(ctx, scope, member, time.Minute)
	require.NoError(t, err)
	defer lease.Release()

	barrier, err := c2.Close(ctx, scope, time.Minute)
	require.NoError(t, err)
	defer barrier.Release()

	n, err := c2.ActiveExcept(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	lease.Release()
	n, err = c2.ActiveExcept(ctx, scope)
	require.NoError(t, err)
	require.Zero(t, n)

	_, err = c1.Hold(ctx, scope, "s:a:pty:2", time.Minute)
	require.ErrorIs(t, err, admission.ErrClosed)

	barrier.Release()
	again, err := c1.Hold(ctx, scope, "s:a:pty:3", time.Minute)
	require.NoError(t, err)
	again.Release()
}

func TestRedisRealBarrierIsRenewedAndLossIsReported(t *testing.T) {
	c1, c2, raw, prefix := realRedisPair(t)
	ctx := t.Context()
	scope := admission.MachineScope("d-" + uuid.NewString())
	ttl := 2 * time.Second

	barrier, err := c2.Close(ctx, scope, ttl)
	require.NoError(t, err)
	defer barrier.Release()

	time.Sleep(3 * time.Second)
	require.NoError(t, barrier.Check(), "a held barrier is renewed past its initial TTL")

	closedKey := redisKey(prefix, scope, "closed")
	require.NoError(t, raw.Del(ctx, closedKey).Err())

	select {
	case <-barrier.Lost():
		require.ErrorIs(t, barrier.Check(), admission.ErrLeaseLost)
	case <-time.After(ttl):
		t.Fatal("the barrier kept reporting ownership after its key was deleted")
	}

	again, err := c1.Close(ctx, scope, time.Minute)
	require.NoError(t, err, "a lost barrier does not keep the scope closed")
	again.Release()
}

func TestRedisRealLostMemberIsNotResurrected(t *testing.T) {
	c1, _, raw, prefix := realRedisPair(t)
	ctx := t.Context()
	scope := admission.MachineScope("d-" + uuid.NewString())
	member := admission.SessionMember("s", "shell", "1")
	ttl := 2 * time.Second

	lease, err := c1.Hold(ctx, scope, member, ttl)
	require.NoError(t, err)
	defer lease.Release()

	opsKey := redisKey(prefix, scope, "ops")
	require.NoError(t, raw.ZRem(ctx, opsKey, member).Err())

	// Renewal ticks every ttl/3. Wait past one tick, and past the ttl/6
	// margin, so a loop that put the member back would be visible.
	select {
	case <-lease.Lost():
	case <-time.After(ttl):
		t.Fatal("a removed member was not reported lost")
	}
	time.Sleep(ttl/3 + 100*time.Millisecond)

	_, err = raw.ZScore(ctx, opsKey, member).Result()
	require.ErrorIs(t, err, redis.Nil, "renewal must not resurrect a removed member")
	require.ErrorIs(t, lease.Check(), admission.ErrLeaseLost)
}
