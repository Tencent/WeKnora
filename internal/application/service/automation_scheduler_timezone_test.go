package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

// utc8 stands in for a scheduler process running with its local clock in
// UTC+08 (e.g. TZ=Asia/Shanghai): time.Now there carries a +08:00 offset.
var utc8 = time.FixedZone("UTC+08", 8*3600)

func onceTrigger(at time.Time) *types.AutomationTrigger {
	return &types.AutomationTrigger{Type: types.AutomationTriggerOnce, At: at.Format(time.RFC3339), Timezone: "UTC"}
}

// The scheduler clock shows a +08:00 wall time while next_run_at is stored
// as UTC. One second before the real due instant, the raw text comparison
// next_run_at <= now still matches, so the task must not be claimed.
func TestAutomationTickDoesNotClaimFutureTaskInNonUTCClock(t *testing.T) {
	f := newAutomationFixture(t)
	due := f.now.Add(time.Hour)
	a := f.create(t, types.AutomationPatch{Trigger: onceTrigger(due)})
	require.Equal(t, due, a.NextRunAt.UTC())

	f.now = due.Add(-time.Second).In(utc8)
	f.svc.tick(context.Background())

	require.Empty(t, f.runs(t, a.ID))
	reloaded := f.reload(t, a.ID)
	require.True(t, reloaded.Enabled)
	require.NotNil(t, reloaded.NextRunAt)
	require.Equal(t, due, reloaded.NextRunAt.UTC())
	require.Equal(t, int64(0), reloaded.Version)
	select {
	case <-f.svc.wake:
		t.Fatal("a future task woke the scheduler")
	default:
	}
}

// claim must refuse a due time in the future even when the query let it
// through, whatever offset the caller's clock carried.
func TestAutomationClaimRejectsFutureDueAt(t *testing.T) {
	f := newAutomationFixture(t)
	due := f.now.Add(time.Minute)
	a := f.create(t, types.AutomationPatch{Trigger: onceTrigger(due)})

	require.NoError(t, f.svc.claimDue(context.Background(), due.Add(-59*time.Second).In(utc8)))

	require.Empty(t, f.runs(t, a.ID))
	require.Equal(t, int64(0), f.reload(t, a.ID).Version)
}

// A unique slot (automation, scheduled_at, trigger_kind) that already holds
// a run must not be reported as a fresh queued run: no wake, no extra rows,
// however often the claim replays.
func TestAutomationDuplicateSlotDoesNotWake(t *testing.T) {
	f := newAutomationFixture(t)
	due := f.now.Add(time.Minute)
	a := f.create(t, types.AutomationPatch{Trigger: onceTrigger(due)})

	f.now = due.Add(time.Second)
	require.NoError(t, f.svc.claim(context.Background(), f.reload(t, a.ID), f.now))
	require.Len(t, f.runs(t, a.ID), 1)
	select {
	case <-f.svc.wake:
	default:
		t.Fatal("expected a wake for the first queued run")
	}

	// The executor finished; the slot stays taken for this scheduled_at.
	require.NoError(t, f.db.Model(&types.AutomationRun{}).
		Where("automation_id = ?", a.ID).
		Update("status", types.AutomationRunSucceeded).Error)

	// Pretend the same slot comes due again with no new schedule behind it.
	wakes := 0
	for i := 0; i < 32; i++ {
		require.NoError(t, f.db.Model(&types.Automation{}).Where("id = ?", a.ID).
			Updates(map[string]any{"next_run_at": due, "enabled": true, "version": 0}).Error)
		var stale types.Automation
		require.NoError(t, f.db.Where("id = ?", a.ID).First(&stale).Error)
		require.NoError(t, f.svc.claim(context.Background(), &stale, f.now))
		select {
		case <-f.svc.wake:
			wakes++
		default:
		}
	}
	require.Zero(t, wakes)
	require.Len(t, f.runs(t, a.ID), 1)
}

// A due task still claims exactly once, and a once trigger then stops.
func TestAutomationDueTaskStillClaimsUnderUTCClock(t *testing.T) {
	f := newAutomationFixture(t)
	due := f.now.Add(time.Minute)
	a := f.create(t, types.AutomationPatch{Trigger: onceTrigger(due)})

	f.now = due.Add(time.Second)
	f.svc.tick(context.Background())

	runs := f.runs(t, a.ID)
	require.Len(t, runs, 1)
	// tick starts an asynchronous executor. Wait for it to release its slot
	// before the fixture closes SQLite and removes its WAL directory.
	done := f.settleRun(t, runs[0].ID)
	require.Equal(t, types.AutomationRunSucceeded, done.Status)
	reloaded := f.reload(t, a.ID)
	require.False(t, reloaded.Enabled)
	require.Nil(t, reloaded.NextRunAt)
	// Claiming advances the schedule; starting the queued run acquires a second version.
	require.Equal(t, int64(2), reloaded.Version)
}
