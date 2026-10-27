package admission

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const prefix = "weknora:sandbox:user:"

func newRedis(t *testing.T) (*RedisRegistry, *miniredis.Miniredis) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return NewRedis(client, prefix), mini
}

func registries(t *testing.T) map[string]Registry {
	r, _ := newRedis(t)
	return map[string]Registry{"redis": r, "memory": NewMemory()}
}

func waitClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expected the lease to be reported lost")
	}
}

func TestRegistryContract(t *testing.T) {
	for name, reg := range registries(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			machine := MachineScope("d-1")
			a := SessionMember("s1", "turn", "t1")
			b := SessionMember("s1", "pty", "p1")
			c := SessionMember("s2", "shell", "x1")
			la, err := reg.Hold(ctx, machine, a, time.Minute)
			require.NoError(t, err)
			lb, err := reg.Hold(ctx, machine, b, time.Minute)
			require.NoError(t, err)
			lc, err := reg.Hold(ctx, machine, c, time.Minute)
			require.NoError(t, err)

			n, err := reg.ActiveExcept(ctx, machine)
			require.NoError(t, err)
			require.Equal(t, 3, n)
			n, err = reg.ActiveExcept(ctx, machine, a)
			require.NoError(t, err)
			require.Equal(t, 2, n, "only the exact members passed are exempt; the same session's PTY still counts")

			n, err = reg.ActiveExcept(ctx, SessionScope("d-1", "s1"))
			require.NoError(t, err)
			require.Zero(t, n, "scopes are independent")

			barrier, err := reg.Close(ctx, machine, time.Minute)
			require.NoError(t, err)
			require.NoError(t, barrier.Check())
			_, err = reg.Hold(ctx, machine, SessionMember("s3", "file", "f"), time.Minute)
			require.ErrorIs(t, err, ErrClosed)
			_, err = reg.Close(ctx, machine, time.Minute)
			require.ErrorIs(t, err, ErrClosed, "a second closer backs off")

			la.Release()
			lb.Release()
			lc.Release()
			la.Release() // idempotent
			n, err = reg.ActiveExcept(ctx, machine)
			require.NoError(t, err)
			require.Zero(t, n)

			barrier.Release()
			l, err := reg.Hold(ctx, machine, SessionMember("s3", "file", "f2"), time.Minute)
			require.NoError(t, err)
			require.Equal(t, SessionMember("s3", "file", "f2"), l.Member())
			l.Release()
		})
	}
}

func TestRedisRenewNeverResurrectsAnExpiredMember(t *testing.T) {
	reg, mini := newRedis(t)
	ctx := context.Background()
	scope := MachineScope("d-1")
	lease, err := reg.Hold(ctx, scope, SessionMember("s", "shell", "1"), 300*time.Millisecond)
	require.NoError(t, err)
	defer lease.Release()

	// Redis TIME jumps past the lease: the member has expired server-side.
	mini.SetTime(time.Now().Add(time.Hour))
	waitClosed(t, lease.Lost())
	// The renew loop ticks every ttl/3. Wait past one more tick so a loop
	// that re-added the member after declaring it lost cannot hide.
	time.Sleep(200 * time.Millisecond)
	n, err := reg.ActiveExcept(ctx, scope)
	require.NoError(t, err)
	require.Zero(t, n, "an expired member must not be re-registered by its renew loop")
}

func TestRedisLeaseIsLostWhenRedisIsUnreachable(t *testing.T) {
	reg, mini := newRedis(t)
	lease, err := reg.Hold(
		context.Background(), MachineScope("d-1"), SessionMember("s", "shell", "1"), 300*time.Millisecond,
	)
	require.NoError(t, err)
	defer lease.Release()
	mini.Close()
	waitClosed(t, lease.Lost())
}

// stallHook makes every command hang until its context ends, the way a
// Redis behind a partition does.
type stallHook struct{ stall *atomic.Bool }

func (h stallHook) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h stallHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if h.stall.Load() {
			<-ctx.Done()
			return ctx.Err()
		}
		return next(ctx, cmd)
	}
}

func (h stallHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func newStallingRedis(t *testing.T) (*RedisRegistry, *atomic.Bool) {
	t.Helper()
	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	stall := &atomic.Bool{}
	client.AddHook(stallHook{stall: stall})
	return NewRedis(client, prefix), stall
}

func TestRedisLeaseIsLostAtItsLocalDeadlineWhileRenewStalls(t *testing.T) {
	reg, stall := newStallingRedis(t)
	ttl := 600 * time.Millisecond
	lease, err := reg.Hold(context.Background(), MachineScope("d-1"), SessionMember("s", "shell", "1"), ttl)
	require.NoError(t, err)
	defer lease.Release()

	stall.Store(true)
	stalledAt := time.Now()
	waitClosed(t, lease.Lost())
	// The last confirmed request was sent before stalledAt, so the deadline
	// is at most ttl-ttl/6 after it. Losing it only at Redis expiry (ttl) or
	// after the hung call returns would let another replica act first.
	require.Less(t, time.Since(stalledAt), ttl)
	require.ErrorIs(t, lease.Check(), ErrLeaseLost)
}

func TestRedisBarrierIsLostAtItsLocalDeadlineWhileRenewStalls(t *testing.T) {
	reg, stall := newStallingRedis(t)
	ttl := 600 * time.Millisecond
	barrier, err := reg.Close(context.Background(), MachineScope("d-1"), ttl)
	require.NoError(t, err)
	defer barrier.Release()

	stall.Store(true)
	stalledAt := time.Now()
	waitClosed(t, barrier.Lost())
	require.Less(t, time.Since(stalledAt), ttl)
	require.ErrorIs(t, barrier.Check(), ErrLeaseLost)
}

func TestBindCancelsWhenAnySignalIsLost(t *testing.T) {
	reg := NewMemory()
	ctx := context.Background()
	lease, err := reg.Hold(ctx, MachineScope("d"), SessionMember("s", "shell", "1"), time.Minute)
	require.NoError(t, err)
	barrier, err := reg.Close(ctx, SessionScope("d", "s"), time.Minute)
	require.NoError(t, err)

	bound, stop := Bind(ctx, lease, barrier)
	defer stop()
	require.NoError(t, bound.Err())
	reg.LoseBarrier(SessionScope("d", "s"))
	waitClosed(t, bound.Done())
	require.ErrorIs(t, context.Cause(bound), ErrLeaseLost)

	again, stopAgain := Bind(ctx, lease)
	stopAgain()
	require.ErrorIs(t, again.Err(), context.Canceled, "stop ends the watch and the context")
	require.NotErrorIs(t, context.Cause(again), ErrLeaseLost, "stop is not a loss")
}

func TestBindOnAnAlreadyLostSignalIsCancelledAtOnce(t *testing.T) {
	reg := NewMemory()
	scope := MachineScope("d")
	member := SessionMember("s", "shell", "1")
	lease, err := reg.Hold(context.Background(), scope, member, time.Minute)
	require.NoError(t, err)
	reg.ExpireMember(scope, member)
	bound, stop := Bind(context.Background(), lease)
	defer stop()
	require.ErrorIs(t, context.Cause(bound), ErrLeaseLost)
}

func TestRedisBarrierRenewsAndReportsLoss(t *testing.T) {
	reg, mini := newRedis(t)
	ctx := context.Background()
	scope := MachineScope("d-1")
	barrier, err := reg.Close(ctx, scope, 300*time.Millisecond)
	require.NoError(t, err)
	defer barrier.Release()

	// miniredis ages key TTLs only when FastForward is called, not during a
	// wall-clock sleep. Leave a short remainder of the initial TTL, wait for
	// a renew to PEXPIRE the key back to a full TTL, then jump farther than
	// that remainder. Without a renew the key is gone.
	mini.FastForward(250 * time.Millisecond)
	time.Sleep(250 * time.Millisecond) // renew interval is ttl/3 = 100ms
	mini.FastForward(100 * time.Millisecond)
	require.True(t, mini.Exists(prefix+"{d-1}:machine:closed"), "the barrier outlives its initial TTL while held")
	require.NoError(t, barrier.Check())

	mini.Del(prefix + "{d-1}:machine:closed")
	waitClosed(t, barrier.Lost())
	require.ErrorIs(t, barrier.Check(), ErrLeaseLost)
}

func TestRedisKeysShareTheMachineHashTag(t *testing.T) {
	reg, mini := newRedis(t)
	l, err := reg.Hold(context.Background(), SessionScope("abc", "s1"), SessionMember("s1", "x", "1"), time.Minute)
	require.NoError(t, err)
	defer l.Release()
	require.True(t, mini.Exists(prefix+"{abc}:session:s1:ops"))
}

func TestMemoryHooksSimulateLoss(t *testing.T) {
	reg := NewMemory()
	ctx := context.Background()
	scope := MachineScope("d")
	member := SessionMember("s", "shell", "1")
	lease, err := reg.Hold(ctx, scope, member, time.Minute)
	require.NoError(t, err)
	reg.ExpireMember(scope, member)
	waitClosed(t, lease.Lost())
	n, _ := reg.ActiveExcept(ctx, scope)
	require.Zero(t, n)

	barrier, err := reg.Close(ctx, scope, time.Minute)
	require.NoError(t, err)
	reg.LoseBarrier(scope)
	waitClosed(t, barrier.Lost())
	require.ErrorIs(t, barrier.Check(), ErrLeaseLost)
}

func TestReleaseIsIdempotentAndIsNotLoss(t *testing.T) {
	for name, reg := range registries(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			scope := MachineScope("d")
			lease, err := reg.Hold(ctx, scope, SessionMember("s", "shell", "1"), time.Minute)
			require.NoError(t, err)
			lease.Release()
			lease.Release()
			select {
			case <-lease.Lost():
				t.Fatal("release must not report the lease lost")
			default:
			}
			require.NoError(t, lease.Check())
			n, err := reg.ActiveExcept(ctx, scope)
			require.NoError(t, err)
			require.Zero(t, n)

			barrier, err := reg.Close(ctx, scope, time.Minute)
			require.NoError(t, err)
			barrier.Release()
			barrier.Release()
			select {
			case <-barrier.Lost():
				t.Fatal("release must not report the barrier lost")
			default:
			}
			require.NoError(t, barrier.Check())
			again, err := reg.Hold(ctx, scope, SessionMember("s", "shell", "2"), time.Minute)
			require.NoError(t, err)
			again.Release()
		})
	}
}

func TestConcurrentHoldAndRelease(t *testing.T) {
	for name, reg := range registries(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			scope := MachineScope("d-conc")
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					member := SessionMember("s", "shell", strconv.Itoa(i))
					for n := 0; n < 15; n++ {
						lease, err := reg.Hold(ctx, scope, member, time.Minute)
						if err != nil {
							t.Errorf("hold: %v", err)
							return
						}
						if _, err := reg.ActiveExcept(ctx, scope, member); err != nil {
							t.Errorf("count: %v", err)
						}
						lease.Release()
						lease.Release()
					}
				}(i)
			}
			wg.Wait()
			n, err := reg.ActiveExcept(ctx, scope)
			require.NoError(t, err)
			require.Zero(t, n)
		})
	}
}

// A short hold must not cut the key's lifetime below a longer member's
// deadline: once the key expires, ActiveExcept would see an idle machine while
// the long operation is still running.
func TestRedisShortHoldNeverShortensALongerLease(t *testing.T) {
	reg, mini := newRedis(t)
	ctx := context.Background()
	scope := MachineScope("d-ttl")
	long, err := reg.Hold(ctx, scope, SessionMember("a", "turn", "1"), 30*time.Minute)
	require.NoError(t, err)
	defer long.Release()
	short, err := reg.Hold(ctx, scope, SessionMember("b", "shell", "1"), 2*time.Minute)
	require.NoError(t, err)
	short.Release()

	ops := reg.key(scope, "ops")
	require.GreaterOrEqual(t, mini.TTL(ops), 30*time.Minute, "the key outlives its longest member")
	mini.FastForward(5 * time.Minute)
	n, err := reg.ActiveExcept(ctx, scope)
	require.NoError(t, err)
	require.Equal(t, 1, n, "the long lease is still registered")
}

func TestSealKeepsTheScopeClosedWithoutReportingLoss(t *testing.T) {
	ctx := context.Background()
	scope := ConfigCreateScope(7, "cfg-a")

	t.Run("memory", func(t *testing.T) {
		reg := NewMemory()
		barrier, err := reg.Close(ctx, scope, time.Minute)
		require.NoError(t, err)
		barrier.Seal()
		barrier.Seal()
		_, err = reg.Hold(ctx, scope, "create:again", time.Minute)
		require.ErrorIs(t, err, ErrClosed)
		_, err = reg.Close(ctx, scope, time.Minute)
		require.ErrorIs(t, err, ErrClosed)
		select {
		case <-barrier.Lost():
			t.Fatal("seal must not report the barrier lost")
		default:
		}
		require.NoError(t, barrier.Check())
	})

	t.Run("redis", func(t *testing.T) {
		reg, mini := newRedis(t)
		barrier, err := reg.Close(ctx, scope, 300*time.Millisecond)
		require.NoError(t, err)
		barrier.Seal()
		time.Sleep(400 * time.Millisecond)
		mini.FastForward(time.Second)
		key := prefix + "{config:7:cfg-a}:create:closed"
		require.True(t, mini.Exists(key), "seal persists the closed flag past its TTL")
		require.Equal(t, time.Duration(0), mini.TTL(key))
		_, err = reg.Hold(ctx, scope, "create:again", time.Minute)
		require.ErrorIs(t, err, ErrClosed)
		select {
		case <-barrier.Lost():
			t.Fatal("seal must not report the barrier lost")
		default:
		}
		require.NoError(t, barrier.Check())
	})
}
