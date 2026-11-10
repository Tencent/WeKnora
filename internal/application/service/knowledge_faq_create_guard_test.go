package service

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newGuardService(t *testing.T) *knowledgeService {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &knowledgeService{redisClient: rdb}
}

func TestAcquireFAQCreateGuardRejectsConcurrentSameQuestion(t *testing.T) {
	svc := newGuardService(t)
	ctx := context.Background()

	release, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-1", "哈哈哈")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	if _, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-1", "哈哈哈"); err == nil {
		t.Fatal("expected the retry to be rejected while the first create is in flight")
	}

	// Once the first create finishes the question is writable again, so a real
	// retry after a genuine failure is not permanently blocked.
	release()
	release2, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-1", "哈哈哈")
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	release2()
}

func TestAcquireFAQCreateGuardIsolatesUnrelatedCreates(t *testing.T) {
	svc := newGuardService(t)
	ctx := context.Background()

	release, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-1", "哈哈哈")
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	defer release()

	for _, tc := range []struct {
		name     string
		tenantID uint64
		kbID     string
		question string
	}{
		{"different question", 1, "kb-1", "呵呵呵"},
		{"different knowledge base", 1, "kb-2", "哈哈哈"},
		{"different tenant", 2, "kb-1", "哈哈哈"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release, err := svc.acquireFAQCreateGuard(ctx, tc.tenantID, tc.kbID, tc.question)
			if err != nil {
				t.Fatalf("acquire failed: %v", err)
			}
			release()
		})
	}
}

func TestAcquireFAQCreateGuardFallsBackWithoutRedis(t *testing.T) {
	svc := &knowledgeService{}
	ctx := context.Background()

	release, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-lite", "哈哈哈")
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	if _, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-lite", "哈哈哈"); err == nil {
		t.Fatal("expected in-process guard to reject the concurrent create")
	}

	release()
	release2, err := svc.acquireFAQCreateGuard(ctx, 1, "kb-lite", "哈哈哈")
	if err != nil {
		t.Fatalf("acquire after release failed: %v", err)
	}
	release2()
}
