package collaboration

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func withAggregateLimit(t *testing.T, n int) {
	t.Helper()
	previous := maxAggregateBytes
	maxAggregateBytes = n
	t.Cleanup(func() { maxAggregateBytes = previous })
}

// An oversized active aggregate must close instead of rejecting every later
// mutation, including the cancellation that would end it.
func TestHistoryLimitClosesActiveGroup(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	r := New(s, nil)
	_, err := r.Spawn(ctx, scope, "spawn", "child", "agent", "{}", "task")
	require.NoError(t, err)
	before, err := json.Marshal(read(t, s, scope))
	require.NoError(t, err)
	withAggregateLimit(t, len(before)+256)

	_, err = r.Send(ctx, scope, "big", "child", strings.Repeat("x", 4000), "", false)
	require.ErrorIs(t, err, ErrHistoryLimit)

	g := read(t, s, scope)
	require.NotEqual(t, "active", g.Status)
	require.Empty(t, g.Members[0].Mail, "the rejected mutation is not persisted")
	require.Equal(t, "cancelled", current(g.Members[0]).State)
	require.NoError(t, r.Cancel(ctx, scope), "closing an oversized group still succeeds")
}

func TestCompactKeepsStatusAndDropsBulkyHistory(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	r := New(s, nil)
	_, err := r.Spawn(ctx, scope, "spawn", "child", "agent", "{}", "task")
	require.NoError(t, err)
	require.NoError(t, s.Change(ctx, scope, func(g *Group) error {
		m := g.Members[0]
		v := current(m)
		v.State = "succeeded"
		v.Result = Result{Answer: strings.Repeat("a", 10000), Evidence: "tool search: success=true"}
		publish(g, m, v)
		g.Status = "completed"
		return nil
	}))

	require.NoError(t, r.Compact(ctx, scope))
	g := read(t, s, scope)
	require.Equal(t, "completed", g.Status)
	require.Equal(t, "succeeded", g.StatusView().Tasks[0].Status)
	require.Equal(t, compactedResult, g.Events[0].Content)
	require.Empty(t, current(g.Members[0]).Result.Answer)
}

// MaxRunning bounds child executions across groups, not only within one group.
func TestMaxRunningBoundsExecutionsAcrossGroups(t *testing.T) {
	s, scope := testStore(t)
	ctx := context.Background()
	other := scope
	other.SessionID, other.ID = "other-session", "other-message"
	require.NoError(t, s.Ensure(ctx, other))

	var current, peak, total atomic.Int32
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	r := New(s, func(ctx context.Context, _ Work) (Result, error) {
		n := current.Add(1)
		defer current.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		total.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return Result{Answer: "done"}, nil
	})
	r.MaxRunning = 2
	for _, sc := range []Scope{scope, other} {
		for i, name := range []string{"a", "b", "c"} {
			_, err := r.Spawn(ctx, sc, sc.ID+name, name, "agent", "{}", "task")
			require.NoError(t, err, i)
		}
	}
	start(t, r)
	eventually(t, func() bool { return total.Load() == 2 })
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 2, peak.Load())
	once.Do(func() { close(release) })
	eventually(t, func() bool { return total.Load() == 6 })
	require.LessOrEqual(t, peak.Load(), int32(2))
}

func TestHeartbeatWritesOnlyWhenRenewalIsDue(t *testing.T) {
	r := New(nil, nil)
	now := time.Now()
	require.False(t, r.renewalDue(now.Add(r.Lease), now))
	require.True(t, r.renewalDue(now.Add(r.Lease/2), now))

	g := &Group{Deadline: now.Add(collaborationHorizon)}
	touchDeadline(g, now.Add(time.Minute))
	require.Equal(t, now.Add(collaborationHorizon), g.Deadline, "small extensions are skipped")
	later := now.Add(collaborationHorizon / 5)
	touchDeadline(g, later)
	require.Equal(t, later.Add(collaborationHorizon), g.Deadline)
}
