package im

import (
	"context"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type userReservationFailure struct{}

func (userReservationFailure) DialHook(next redis.DialHook) redis.DialHook { return next }

func (userReservationFailure) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (userReservationFailure) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "incr" {
			return errors.New("fixture reservation failure")
		}
		return next(ctx, cmd)
	}
}

func TestQAQueueUserReservationOwnership(t *testing.T) {
	for _, failed := range []bool{false, true} {
		for _, path := range []string{"processed", "removed", "canceled", "closed", "full"} {
			name := "confirmed/" + path
			if failed {
				name = "bypassed/" + path
			}
			t.Run(name, func(t *testing.T) {
				server := miniredis.RunT(t)
				client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
				t.Cleanup(func() { _ = client.Close() })
				const user = "same-user"
				key := RedisKeyQueueUser + user
				require.NoError(t, server.Set(key, "1")) // Another instance's existing request.
				if failed {
					client.AddHook(userReservationFailure{})
				}
				calls := 0
				q := newQAQueue(1, 1, 3, 0, func(*qaRequest) { calls++ }, client)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if path == "closed" {
					q.Stop()
				}
				if path == "full" {
					q.maxSize = 0
				}
				_, err := q.Enqueue(&qaRequest{
					ctx: ctx, cancel: cancel, userKey: user, msg: &IncomingMessage{UserID: user},
				})
				switch path {
				case "closed", "full":
					require.Error(t, err)
				case "removed":
					require.NoError(t, err)
					require.True(t, q.Remove(user))
				default:
					require.NoError(t, err)
					if path == "canceled" {
						cancel()
					}
					q.Stop()
					q.runWorker(0)
				}
				count, err := server.Get(key)
				require.NoError(t, err)
				require.Equal(t, "1", count, "cleanup may release only this request's confirmed reservation")
				expectedCalls := 0
				if path == "processed" {
					expectedCalls = 1
				}
				require.Equal(t, expectedCalls, calls)
			})
		}
	}
}

func TestQAQueueLocalUserLimitAfterRemoval(t *testing.T) {
	calls := 0
	q := newQAQueue(1, 2, 1, 0, func(*qaRequest) { calls++ }, nil)
	newRequest := func() *qaRequest {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		return &qaRequest{ctx: ctx, cancel: cancel, userKey: "user", msg: &IncomingMessage{UserID: "user"}}
	}
	_, err := q.Enqueue(newRequest())
	require.NoError(t, err)
	_, err = q.Enqueue(newRequest())
	require.Error(t, err, "the existing local per-user limit must remain enforced")
	require.True(t, q.Remove("user"))
	_, err = q.Enqueue(newRequest())
	require.NoError(t, err)
	q.Stop()
	q.runWorker(0)
	require.Equal(t, 1, calls)
	require.Zero(t, q.Metrics().Depth)
}
