package im

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// Fail only acquisition: Redis becomes usable again for subsequent commands.
// Releasing an unowned slot would then decrement another worker's counter.
type gateAcquisitionFailure struct{ cancel context.CancelFunc }

func (gateAcquisitionFailure) DialHook(next redis.DialHook) redis.DialHook { return next }

func (gateAcquisitionFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (h gateAcquisitionFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "evalsha" || cmd.Name() == "eval" {
			if h.cancel != nil {
				h.cancel()
			}
			return errors.New("fixture acquisition failure")
		}
		return next(ctx, cmd)
	}
}

func TestQAQueueReleasesOnlyAcquiredGlobalSlots(t *testing.T) {
	for _, mode := range []string{"acquired", "fail open", "canceled during acquisition"} {
		t.Run(mode, func(t *testing.T) {
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
			t.Cleanup(func() { _ = client.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantCount, wantCalls := "0", 1
			if mode != "acquired" {
				require.NoError(t, server.Set(RedisKeyGlobalGate, "1"))
				hook := gateAcquisitionFailure{}
				if mode == "canceled during acquisition" {
					hook.cancel = cancel
					wantCalls = 0
				}
				client.AddHook(hook)
				wantCount = "1"
			}
			calls := 0
			q := newQAQueue(1, 2, 3, 1, func(*qaRequest) { calls++ }, client)
			_, err := q.Enqueue(&qaRequest{
				ctx: ctx, cancel: cancel, userKey: "user", msg: &IncomingMessage{UserID: "user"},
			})
			require.NoError(t, err)
			q.Stop()
			q.runWorker(0)
			require.Equal(t, wantCalls, calls, "only live requests should retain fail-open execution")
			count, err := server.Get(RedisKeyGlobalGate)
			require.NoError(t, err)
			require.Equal(t, wantCount, count, "a request that bypassed Redis must not release another worker's slot")
			require.Zero(t, q.Metrics().ActiveWorkers)
		})
	}
}
