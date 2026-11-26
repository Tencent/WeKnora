package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// TestNextMemberUniqueWithinSameMillisecond guards the member-collision fix
// directly: two requests stamped with the same millisecond must still get
// distinct ZSET members, otherwise ZCARD undercounts the burst.
func TestNextMemberUniqueWithinSameMillisecond(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	const nowMs = int64(1795328000000)
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		m := l.nextMember(nowMs)
		if _, dup := seen[m]; dup {
			t.Fatalf("duplicate member %q for the same millisecond", m)
		}
		seen[m] = struct{}{}
	}
}

// TestRedisAllowCountsSameMillisecondRequestsSeparately reproduces the ZSET
// member collision: redisAllow used "instanceID:nowMs" as the member, so all
// requests from one instance within the same millisecond collapsed into a
// single ZSET member and were counted as one hit.
func TestRedisAllowCountsSameMillisecondRequestsSeparately(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = client.Close() }()

	l := New(client, "test:", time.Minute, "inst")
	ctx := context.Background()
	const maxReq = 10
	allowed := 0
	for i := 0; i < 1000; i++ {
		if l.Allow(ctx, "k", maxReq) {
			allowed++
		}
	}
	if allowed != maxReq {
		t.Fatalf("1000 rapid requests with max=%d: allowed %d, want exactly %d",
			maxReq, allowed, maxReq)
	}

	// The window must contain exactly maxReq distinct members.
	n, err := client.ZCard(ctx, "test:k").Result()
	if err != nil {
		t.Fatalf("ZCARD: %v", err)
	}
	if n != maxReq {
		t.Fatalf("ZCARD = %d, want %d", n, maxReq)
	}
}
