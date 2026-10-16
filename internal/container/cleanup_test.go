package container

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCleanupRunsRemainingHooksAfterDeadline(t *testing.T) {
	cleaner := NewResourceCleaner()
	var ran []string
	// Registered first, so it runs last unless promoted. This is BrowserSkill's
	// position: an earlier slow hook must not cause it to be skipped.
	cleaner.RegisterWithName("tail", func() error {
		ran = append(ran, "tail")
		return nil
	})
	cleaner.RegisterWithName("slow", func() error {
		time.Sleep(40 * time.Millisecond)
		ran = append(ran, "slow")
		return errors.New("slow failed")
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	errs := cleaner.Cleanup(ctx)
	if len(ran) != 2 || ran[0] != "slow" || ran[1] != "tail" {
		t.Fatalf("ran = %v, want slow then tail", ran)
	}
	if len(errs) != 1 || errs[0].Error() != "slow failed" {
		t.Fatalf("errs = %v, want only the hook error", errs)
	}
}

func TestPromoteRunsNamedHookFirst(t *testing.T) {
	cleaner := NewResourceCleaner()
	var ran []string
	cleaner.RegisterWithName("BrowserSkill", func() error {
		ran = append(ran, "browser")
		return nil
	})
	cleaner.RegisterWithName("later", func() error {
		ran = append(ran, "later")
		return nil
	})
	cleaner.Promote("BrowserSkill")

	if errs := cleaner.Cleanup(context.Background()); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if len(ran) != 2 || ran[0] != "browser" || ran[1] != "later" {
		t.Fatalf("ran = %v, want browser then later", ran)
	}
}

func TestCleanupAlreadyCanceledContextStillRunsHooks(t *testing.T) {
	cleaner := NewResourceCleaner()
	var n int
	cleaner.Register(func() error {
		n++
		return nil
	})
	cleaner.Register(func() error {
		n++
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if errs := cleaner.Cleanup(ctx); len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if n != 2 {
		t.Fatalf("ran %d hooks, want 2", n)
	}
}
