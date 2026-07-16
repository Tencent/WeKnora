// Package redislock provides token-owned Redis locks with atomic renewal and
// release. Callers must treat loss of ownership as loss of exclusive access.
package redislock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const releaseTimeout = 5 * time.Second

var (
	releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('DEL', KEYS[1])
end
return 0
`)
	renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
	return redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
return 0
`)
)

// NewToken returns a random owner token suitable for compare-and-* scripts.
func NewToken() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", fmt.Errorf("generate redis lock token: %w", err)
	}
	return hex.EncodeToString(token[:]), nil
}

// TryAcquire attempts one SET NX acquisition.
func TryAcquire(
	ctx context.Context,
	client redis.UniversalClient,
	key string,
	token string,
	lease time.Duration,
) (bool, error) {
	if err := validateOperation(client, key, token, lease); err != nil {
		return false, err
	}
	acquired, err := client.SetNX(ctx, key, token, lease).Result()
	if err != nil {
		return false, fmt.Errorf("acquire redis lock %q: %w", key, err)
	}
	return acquired, nil
}

// Acquire waits until the lock is acquired or ctx is cancelled.
func Acquire(
	ctx context.Context,
	client redis.UniversalClient,
	key string,
	token string,
	lease time.Duration,
) error {
	for {
		acquired, err := TryAcquire(ctx, client, key, token, lease)
		if err != nil {
			return err
		}
		if acquired {
			return nil
		}

		timer := time.NewTimer(retryDelay())
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Release deletes the lock only if token still owns it.
func Release(
	ctx context.Context,
	client redis.UniversalClient,
	key string,
	token string,
) (bool, error) {
	if client == nil {
		return false, errors.New("redis client is required")
	}
	if key == "" || token == "" {
		return false, errors.New("redis lock key and token are required")
	}
	released, err := releaseScript.Run(ctx, client, []string{key}, token).Int64()
	if err != nil {
		return false, fmt.Errorf("release redis lock %q: %w", key, err)
	}
	return released != 0, nil
}

// Renew extends the lease only if token still owns the lock.
func Renew(
	ctx context.Context,
	client redis.UniversalClient,
	key string,
	token string,
	lease time.Duration,
) (bool, error) {
	if err := validateOperation(client, key, token, lease); err != nil {
		return false, err
	}
	renewed, err := renewScript.Run(
		ctx,
		client,
		[]string{key},
		token,
		lease.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("renew redis lock %q: %w", key, err)
	}
	return renewed != 0, nil
}

// WithRenewableLock acquires key, renews it while fn runs, and atomically
// releases it on exit. Renewal failure cancels the context passed to fn.
func WithRenewableLock(
	ctx context.Context,
	client redis.UniversalClient,
	key string,
	lease time.Duration,
	renewInterval time.Duration,
	fn func(context.Context) error,
) (resultErr error) {
	if fn == nil {
		return errors.New("redis lock callback is required")
	}
	if renewInterval <= 0 || renewInterval >= lease {
		return errors.New("redis lock renewal interval must be positive and shorter than the lease")
	}

	token, err := NewToken()
	if err != nil {
		return err
	}
	if err := Acquire(ctx, client, key, token, lease); err != nil {
		return err
	}

	lockCtx, cancelLock := context.WithCancel(ctx)
	renewCtx, stopRenewal := context.WithCancel(context.WithoutCancel(ctx))
	renewResult := make(chan error, 1)
	go renewLoop(
		renewCtx,
		cancelLock,
		client,
		key,
		token,
		lease,
		renewInterval,
		renewResult,
	)

	defer func() {
		stopRenewal()
		renewErr := <-renewResult
		cancelLock()

		releaseCtx, cancelRelease := context.WithTimeout(context.Background(), releaseTimeout)
		released, releaseErr := Release(releaseCtx, client, key, token)
		cancelRelease()
		if releaseErr == nil && !released && renewErr == nil {
			releaseErr = fmt.Errorf("redis lock ownership lost for %q", key)
		}

		resultErr = errors.Join(resultErr, renewErr, ctx.Err(), releaseErr)
	}()

	resultErr = fn(lockCtx)
	return resultErr
}

func renewLoop(
	ctx context.Context,
	cancelLock context.CancelFunc,
	client redis.UniversalClient,
	key string,
	token string,
	lease time.Duration,
	renewInterval time.Duration,
	result chan<- error,
) {
	ticker := time.NewTicker(renewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			result <- nil
			return
		case <-ticker.C:
			renewed, err := Renew(ctx, client, key, token, lease)
			if err != nil {
				if errors.Is(err, context.Canceled) && ctx.Err() != nil {
					result <- nil
					return
				}
				cancelLock()
				result <- err
				return
			}
			if !renewed {
				cancelLock()
				result <- fmt.Errorf("redis lock ownership lost for %q", key)
				return
			}
		}
	}
}

func validateOperation(
	client redis.UniversalClient,
	key string,
	token string,
	lease time.Duration,
) error {
	if client == nil {
		return errors.New("redis client is required")
	}
	if key == "" || token == "" {
		return errors.New("redis lock key and token are required")
	}
	if lease <= 0 {
		return errors.New("redis lock lease must be positive")
	}
	return nil
}

func retryDelay() time.Duration {
	var random [1]byte
	if _, err := rand.Read(random[:]); err != nil {
		return 50 * time.Millisecond
	}
	return 25*time.Millisecond + time.Duration(random[0])%50*time.Millisecond
}
