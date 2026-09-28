package hostpool

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// One node holds a plugin's lease at a time; it passes on when the holder
// gives it up or stops renewing it.
func TestLeases(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	ctx := context.Background()
	a, b := NewLeases(rdb, "a"), NewLeases(rdb, "b")
	for _, step := range []struct {
		l    *Leases
		want bool
	}{{a, true}, {b, false}, {a, true}} {
		if got, err := step.l.Acquire(ctx, "acme.bot"); err != nil || got != step.want {
			t.Fatalf("%s acquire = %v, %v", step.l.node, got, err)
		}
	}
	if a.Holder(ctx, "acme.bot") != "a" {
		t.Fatal("holder")
	}
	b.Release(ctx, "acme.bot") // not b's to give up
	if got, _ := b.Acquire(ctx, "acme.bot"); got {
		t.Fatal("b released a's lease")
	}
	a.Release(ctx, "acme.bot")
	if got, _ := b.Acquire(ctx, "acme.bot"); !got {
		t.Fatal("b could not take a released lease")
	}
	mr.FastForward(LeaseTTL + 1)
	if got, _ := a.Acquire(ctx, "acme.bot"); !got {
		t.Fatal("a could not take an expired lease")
	}
}
