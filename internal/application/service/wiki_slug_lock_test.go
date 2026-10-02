package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newWikiSlugLockTestService(t *testing.T) *wikiIngestService {
	t.Helper()

	mini := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mini.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &wikiIngestService{redisClient: client}
}

// TestWikiSlugLockScriptsRequireOwnership pins the two ownership rules the
// rewrite depends on: renewal and release must both be no-ops for a caller
// that no longer owns the key. Without them a holder that overran its TTL
// would (a) push the TTL of a lock it had already lost and (b) delete the NEW
// owner's lock mid-write — re-opening the lost-update race the lock exists to
// close, and letting a third reducer in behind it.
func TestWikiSlugLockScriptsRequireOwnership(t *testing.T) {
	svc := newWikiSlugLockTestService(t)
	ctx := context.Background()
	key := wikiSlugLockPrefix + "kb-1:entity/warren-buffett"
	ttlSeconds := int(wikiSlugLockTTL.Seconds())

	require.NoError(t, svc.redisClient.Set(ctx, key, "owner-a", wikiSlugLockTTL).Err())

	// A stale holder must not renew...
	renewed, err := svc.redisClient.Eval(ctx, wikiSlugLockRenewScript,
		[]string{key}, "owner-b", ttlSeconds).Int()
	require.NoError(t, err)
	assert.Equal(t, 0, renewed)

	// ...nor release.
	released, err := svc.redisClient.Eval(ctx, wikiSlugLockReleaseScript, []string{key}, "owner-b").Int()
	require.NoError(t, err)
	assert.Equal(t, 0, released)

	owner, err := svc.redisClient.Get(ctx, key).Result()
	require.NoError(t, err)
	assert.Equal(t, "owner-a", owner, "a non-owner must not be able to touch the lock")

	// The owner itself can do both.
	renewed, err = svc.redisClient.Eval(ctx, wikiSlugLockRenewScript,
		[]string{key}, "owner-a", ttlSeconds).Int()
	require.NoError(t, err)
	assert.Equal(t, 1, renewed)

	released, err = svc.redisClient.Eval(ctx, wikiSlugLockReleaseScript, []string{key}, "owner-a").Int()
	require.NoError(t, err)
	assert.Equal(t, 1, released)

	_, err = svc.redisClient.Get(ctx, key).Result()
	assert.Equal(t, redis.Nil, err, "the owner's release must delete the key")
}

// TestWithSlugLockSerializesSameSlug checks the property the lock exists for:
// two reducers contending for one slug must never be inside fn at the same
// time. Concurrent writers on one page are what produced the duplicate-key
// failures and the silently lost contributions this path prevents.
func TestWithSlugLockSerializesSameSlug(t *testing.T) {
	svc := newWikiSlugLockTestService(t)
	ctx := context.Background()

	var mu sync.Mutex
	var entries []string
	var wg sync.WaitGroup
	for _, tag := range []string{"a", "b", "c"} {
		wg.Add(1)
		go func(tag string) {
			defer wg.Done()
			acquired, err := svc.withSlugLock(ctx, "kb-1", "entity/berkshire-hathaway", func() error {
				mu.Lock()
				entries = append(entries, "enter-"+tag)
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				entries = append(entries, "exit-"+tag)
				mu.Unlock()
				return nil
			})
			assert.NoError(t, err)
			assert.True(t, acquired, "a clean poll for the slug must eventually succeed")
		}(tag)
	}
	wg.Wait()

	require.Len(t, entries, 6)
	for i := 0; i < len(entries); i += 2 {
		enter, exit := entries[i], entries[i+1]
		require.Equal(t, "enter", enter[:5])
		require.Equal(t, "exit", exit[:4])
		assert.Equal(t, enter[6:], exit[5:],
			"two reducers were inside the same slug's critical section at once")
	}
}

// TestWithSlugLockReleasesOwnLock covers the release path end to end: after fn
// returns the key is gone, so the next reducer acquires immediately instead of
// waiting out (or worse, outliving) the TTL.
func TestWithSlugLockReleasesOwnLock(t *testing.T) {
	svc := newWikiSlugLockTestService(t)
	ctx := context.Background()
	const slug = "concept/intrinsic-value"
	key := wikiSlugLockPrefix + "kb-1:" + slug

	acquired, err := svc.withSlugLock(ctx, "kb-1", slug, func() error {
		_, getErr := svc.redisClient.Get(ctx, key).Result()
		assert.NoError(t, getErr, "the lock must be held while fn runs")
		return nil
	})
	require.NoError(t, err)
	require.True(t, acquired)

	_, err = svc.redisClient.Get(ctx, key).Result()
	assert.Equal(t, redis.Nil, err, "the lock must be released once fn returns")
}
