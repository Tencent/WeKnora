package limiter

import (
	"context"
	"testing"
	"time"
)

func TestLocalLimiterRaisesLimitWithActiveHolder(t *testing.T) {
	l := NewLocalLimiter()
	held, _ := l.Acquire(context.Background(), "model", 1)
	defer held()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	second, _ := l.Acquire(ctx, "model", 2)
	defer second()
	if ctx.Err() != nil {
		t.Fatal("raising the limit did not admit a second holder")
	}
	stats, err := l.(*localLimiter).RuntimeStats(context.Background())
	if err != nil || len(stats) != 1 || stats[0].Active != 2 || stats[0].Limit != 2 {
		t.Fatalf("raised limit stats = %+v, err = %v", stats, err)
	}
}

func TestLocalLimiterLowersLimitWithoutLosingHolders(t *testing.T) {
	l := NewLocalLimiter()
	r1, _ := l.Acquire(context.Background(), "model", 2)
	defer r1()
	r2, _ := l.Acquire(context.Background(), "model", 2)
	defer r2()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	acquired := make(chan func(), 1)
	go func() {
		r, _ := l.Acquire(ctx, "model", 1)
		acquired <- r
	}()
	// Wait until the lower limit is registered, without relying on scheduling.
	for {
		stats, _ := l.(*localLimiter).RuntimeStats(context.Background())
		if len(stats) == 1 && stats[0].Waiting == 1 && stats[0].Limit == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("lower-limit waiter did not start")
		}
		time.Sleep(time.Millisecond)
	}
	r1()
	select {
	case r := <-acquired:
		r()
		t.Fatal("lowered limit admitted a request while one old holder was still active")
	case <-time.After(30 * time.Millisecond):
	}
	r2()
	select {
	case r := <-acquired:
		defer r()
		if ctx.Err() != nil {
			t.Fatal("waiter only returned because its context expired")
		}
	case <-ctx.Done():
		t.Fatal("waiter did not proceed after old holders released")
	}
}

func TestLocalLimiterLimitChangeKeepsExistingWaiter(t *testing.T) {
	l := NewLocalLimiter()
	r1, _ := l.Acquire(context.Background(), "model", 1)
	defer r1()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	acquired := make(chan func(), 1)
	go func() {
		r, _ := l.Acquire(ctx, "model", 1)
		acquired <- r
	}()
	for {
		stats, _ := l.(*localLimiter).RuntimeStats(context.Background())
		if len(stats) == 1 && stats[0].Waiting == 1 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("original waiter did not start")
		}
		time.Sleep(time.Millisecond)
	}
	r2, _ := l.Acquire(ctx, "model", 2)
	r2()
	select {
	case r := <-acquired:
		defer r()
		if ctx.Err() != nil {
			t.Fatal("original waiter only returned because its context expired")
		}
		stats, _ := l.(*localLimiter).RuntimeStats(context.Background())
		if len(stats) != 1 || stats[0].Active != 2 || stats[0].Limit != 2 {
			t.Fatalf("original waiter lost the updated limit: %+v", stats)
		}
	case <-ctx.Done():
		t.Fatal("original waiter was stranded when the limit changed")
	}
}
