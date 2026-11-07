// Package backgroundtest holds contract tests shared by every implementation of
// the background-work interfaces.
package backgroundtest

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// Advance moves the implementation's notion of "now" forward by d.
type Advance func(d time.Duration)

var (
	sessA = interfaces.SessionRef{TenantID: 1, SessionID: "sess-a"}
	sessB = interfaces.SessionRef{TenantID: 1, SessionID: "sess-b"}
)

func activity(id, kind string) interfaces.BackgroundActivity {
	return interfaces.BackgroundActivity{
		ID: id, Kind: kind, Title: "title " + id,
		StartedAt: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC),
	}
}

func listIDs(t *testing.T, r interfaces.ActivityRegistry, s interfaces.SessionRef) []string {
	t.Helper()
	items, err := r.List(context.Background(), s)
	require.NoError(t, err)
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	sort.Strings(ids)
	return ids
}

// RunActivityRegistryContract runs the shared ActivityRegistry cases. newRegistry
// must return an empty registry and a function that advances its clock.
func RunActivityRegistryContract(
	t *testing.T,
	newRegistry func(t *testing.T) (interfaces.ActivityRegistry, Advance),
) {
	ctx := context.Background()

	t.Run("AddThenList", func(t *testing.T) {
		r, _ := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("sj_1", "shell_job"), time.Minute, 0))
		items, err := r.List(ctx, sessA)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, "sj_1", items[0].ID)
		require.Equal(t, "shell_job", items[0].Kind)
		require.Equal(t, "title sj_1", items[0].Title)
		require.True(t, items[0].StartedAt.Equal(activity("sj_1", "").StartedAt))
	})

	t.Run("SessionsAreIsolated", func(t *testing.T) {
		r, _ := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("sj_1", "shell_job"), time.Minute, 0))
		require.Empty(t, listIDs(t, r, sessB))
	})

	t.Run("CapIsPerKind", func(t *testing.T) {
		r, _ := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k1"), time.Minute, 2))
		require.NoError(t, r.Add(ctx, sessA, activity("a2", "k1"), time.Minute, 2))
		err := r.Add(ctx, sessA, activity("a3", "k1"), time.Minute, 2)
		require.ErrorIs(t, err, interfaces.ErrActivityLimit)
		require.NoError(t, r.Add(ctx, sessA, activity("b1", "k2"), time.Minute, 2))
		require.Equal(t, []string{"a1", "a2", "b1"}, listIDs(t, r, sessA))
	})

	t.Run("NonPositiveCapMeansUnlimited", func(t *testing.T) {
		r, _ := newRegistry(t)
		for i := 0; i < 10; i++ {
			require.NoError(t, r.Add(ctx, sessA, activity(fmt.Sprintf("a%d", i), "k"), time.Minute, 0))
		}
		require.NoError(t, r.Add(ctx, sessA, activity("neg", "k"), time.Minute, -1))
		require.Len(t, listIDs(t, r, sessA), 11)
	})

	t.Run("ReAddExtendsWithoutCountingAgainstCap", func(t *testing.T) {
		r, advance := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k"), 10*time.Second, 1))
		advance(8 * time.Second)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k"), 10*time.Second, 1))
		advance(8 * time.Second) // 16s after the first Add, 8s after the second
		require.Equal(t, []string{"a1"}, listIDs(t, r, sessA))
	})

	t.Run("ConcurrentAddRespectsCap", func(t *testing.T) {
		r, _ := newRegistry(t)
		const workers = 20
		var wg sync.WaitGroup
		errs := make([]error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				errs[i] = r.Add(ctx, sessA, activity(fmt.Sprintf("c%d", i), "k"), time.Minute, 3)
			}(i)
		}
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
				continue
			}
			require.ErrorIs(t, err, interfaces.ErrActivityLimit)
		}
		require.Equal(t, 3, ok)
		require.Len(t, listIDs(t, r, sessA), 3)
	})

	t.Run("ExpiredItemsDisappearAndFreeTheCap", func(t *testing.T) {
		r, advance := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k"), 10*time.Second, 1))
		advance(11 * time.Second)
		require.Empty(t, listIDs(t, r, sessA))
		require.NoError(t, r.Add(ctx, sessA, activity("a2", "k"), 10*time.Second, 1))
		require.Equal(t, []string{"a2"}, listIDs(t, r, sessA))
	})

	t.Run("RenewExtends", func(t *testing.T) {
		r, advance := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k"), 10*time.Second, 0))
		advance(8 * time.Second)
		found, err := r.Renew(ctx, sessA, "a1", 10*time.Second)
		require.NoError(t, err)
		require.True(t, found)
		advance(8 * time.Second)
		require.Equal(t, []string{"a1"}, listIDs(t, r, sessA))
	})

	t.Run("RenewDoesNotRecreateRemoved", func(t *testing.T) {
		r, _ := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k"), time.Minute, 0))
		require.NoError(t, r.Remove(ctx, sessA, "a1"))
		found, err := r.Renew(ctx, sessA, "a1", time.Minute)
		require.NoError(t, err)
		require.False(t, found)
		require.Empty(t, listIDs(t, r, sessA))
	})

	t.Run("RenewDoesNotRecreateExpired", func(t *testing.T) {
		r, advance := newRegistry(t)
		require.NoError(t, r.Add(ctx, sessA, activity("a1", "k"), 10*time.Second, 0))
		advance(11 * time.Second)
		found, err := r.Renew(ctx, sessA, "a1", time.Minute)
		require.NoError(t, err)
		require.False(t, found)
		require.Empty(t, listIDs(t, r, sessA))
	})

	t.Run("RenewUnknownIsNotFound", func(t *testing.T) {
		r, _ := newRegistry(t)
		found, err := r.Renew(ctx, sessA, "missing", time.Minute)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("RemoveMissingIsNotAnError", func(t *testing.T) {
		r, _ := newRegistry(t)
		require.NoError(t, r.Remove(ctx, sessA, "missing"))
	})
}

func notice(id string, finishedAt time.Time) interfaces.BackgroundNotice {
	return interfaces.BackgroundNotice{ID: id, Kind: "shell_job", Text: "done " + id, FinishedAt: finishedAt}
}

func pendingIDs(t *testing.T, in interfaces.NoticeInbox, s interfaces.SessionRef) []string {
	t.Helper()
	items, err := in.Pending(context.Background(), s)
	require.NoError(t, err)
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids
}

// RunNoticeInboxContract runs the shared NoticeInbox cases. newInbox must return
// an empty inbox whose clock starts at 2026-10-14T10:00:00Z, and a function that
// advances that clock.
func RunNoticeInboxContract(
	t *testing.T,
	newInbox func(t *testing.T) (interfaces.NoticeInbox, Advance),
) {
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

	t.Run("FirstPushWins", func(t *testing.T) {
		in, _ := newInbox(t)
		first, err := in.Push(ctx, sessA, notice("n1", start))
		require.NoError(t, err)
		require.True(t, first)
		second := notice("n1", start)
		second.Text = "overwritten"
		first, err = in.Push(ctx, sessA, second)
		require.NoError(t, err)
		require.False(t, first)
		items, err := in.Pending(ctx, sessA)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, "done n1", items[0].Text)
	})

	t.Run("PendingIsOldestFirst", func(t *testing.T) {
		in, _ := newInbox(t)
		_, err := in.Push(ctx, sessA, notice("late", start.Add(2*time.Minute)))
		require.NoError(t, err)
		_, err = in.Push(ctx, sessA, notice("early", start))
		require.NoError(t, err)
		_, err = in.Push(ctx, sessA, notice("mid", start.Add(time.Minute)))
		require.NoError(t, err)
		require.Equal(t, []string{"early", "mid", "late"}, pendingIDs(t, in, sessA))
	})

	t.Run("AckRemoves", func(t *testing.T) {
		in, _ := newInbox(t)
		_, err := in.Push(ctx, sessA, notice("n1", start))
		require.NoError(t, err)
		_, err = in.Push(ctx, sessA, notice("n2", start))
		require.NoError(t, err)
		require.NoError(t, in.Ack(ctx, sessA, []string{"n1", "unknown"}))
		require.Equal(t, []string{"n2"}, pendingIDs(t, in, sessA))
	})

	t.Run("AckEmptyIsNoop", func(t *testing.T) {
		in, _ := newInbox(t)
		require.NoError(t, in.Ack(ctx, sessA, nil))
	})

	t.Run("AckedIDCanBePushedAgain", func(t *testing.T) {
		in, _ := newInbox(t)
		_, err := in.Push(ctx, sessA, notice("n1", start))
		require.NoError(t, err)
		require.NoError(t, in.Ack(ctx, sessA, []string{"n1"}))
		first, err := in.Push(ctx, sessA, notice("n1", start))
		require.NoError(t, err)
		require.True(t, first)
	})

	t.Run("RetentionHidesOldNotices", func(t *testing.T) {
		in, advance := newInbox(t)
		_, err := in.Push(ctx, sessA, notice("n1", start))
		require.NoError(t, err)
		advance(interfaces.NoticeRetention - time.Minute)
		require.Equal(t, []string{"n1"}, pendingIDs(t, in, sessA))
		advance(2 * time.Minute)
		require.Empty(t, pendingIDs(t, in, sessA))
	})

	t.Run("ClearRemovesSessionOnly", func(t *testing.T) {
		in, _ := newInbox(t)
		_, err := in.Push(ctx, sessA, notice("n1", start))
		require.NoError(t, err)
		_, err = in.Push(ctx, sessB, notice("n2", start))
		require.NoError(t, err)
		require.NoError(t, in.Clear(ctx, sessA))
		require.Empty(t, pendingIDs(t, in, sessA))
		require.Equal(t, []string{"n2"}, pendingIDs(t, in, sessB))
	})

	t.Run("PreservesFields", func(t *testing.T) {
		in, _ := newInbox(t)
		n := notice("n1", start)
		_, err := in.Push(ctx, sessA, n)
		require.NoError(t, err)
		items, err := in.Pending(ctx, sessA)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, n.ID, items[0].ID)
		require.Equal(t, n.Kind, items[0].Kind)
		require.Equal(t, n.Text, items[0].Text)
		require.True(t, n.FinishedAt.Equal(items[0].FinishedAt))
	})
}
