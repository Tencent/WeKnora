package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestLocalLimiterAllowsWithinBudget(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	ctx := context.Background()
	const max = 3
	for i := 0; i < max; i++ {
		if !l.Allow(ctx, "k1", max) {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if l.Allow(ctx, "k1", max) {
		t.Fatal("request over budget should be denied")
	}
}

func TestLocalLimiterIndependentKeys(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	ctx := context.Background()
	if !l.Allow(ctx, "a", 1) {
		t.Fatal("first key should be allowed")
	}
	if !l.Allow(ctx, "b", 1) {
		t.Fatal("second key should be allowed")
	}
}

func TestAllowSkipsWhenMaxZero(t *testing.T) {
	l := New(nil, "test:", time.Minute, "inst")
	for i := 0; i < 100; i++ {
		if !l.Allow(context.Background(), "k", 0) {
			t.Fatal("max<=0 should always allow")
		}
	}
}
