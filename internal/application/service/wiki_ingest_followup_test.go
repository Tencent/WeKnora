package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// --- test doubles -----------------------------------------------------------

// wikiPendingRepoForFollowUpTest reuses the cleanup-test repo and makes
// PendingCount configurable, which is the only input scheduleFollowUp reads.
type wikiPendingRepoForFollowUpTest struct {
	wikiPendingRepoForCleanupTest
	pending int64
}

func (r *wikiPendingRepoForFollowUpTest) PendingCount(
	context.Context, string, string, string,
) (int64, error) {
	return r.pending, nil
}

// followUpEnqueuerRecorder captures every trigger scheduleFollowUp enqueues.
// failFrom >= 0 makes enqueues from that index onward return failErr so the
// partial-failure path can be exercised.
type followUpEnqueuerRecorder struct {
	mu       sync.Mutex
	tasks    []*asynq.Task
	failFrom int
	failErr  error
}

func (e *followUpEnqueuerRecorder) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	idx := len(e.tasks)
	e.tasks = append(e.tasks, task)
	if e.failFrom >= 0 && idx >= e.failFrom {
		return nil, e.failErr
	}
	return &asynq.TaskInfo{}, nil
}

func (e *followUpEnqueuerRecorder) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.tasks)
}

func (e *followUpEnqueuerRecorder) payloads(t *testing.T, kbID string) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, task := range e.tasks {
		var payload WikiIngestPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &payload))
		require.Equal(t, kbID, payload.KnowledgeBaseID)
	}
}

// --- pure fan-out computation ----------------------------------------------

func TestFollowUpTriggerCount(t *testing.T) {
	tests := []struct {
		name                            string
		pending, batchSize, active, max int
		want                            int
	}{
		{"no pending enqueues nothing", 0, 5, 1, 4, 0},
		{"defensive zero batch size", 10, 0, 1, 4, 0},
		{"one batch or less stays at one", 5, 5, 1, 4, 1},
		{"single row stays at one", 1, 5, 1, 4, 1},
		{"just over one batch needs two", 6, 5, 1, 4, 2},
		{"backlog need is capped by free slots", 1000, 5, 2, 4, 3},
		{"all slots free counts own release", 1000, 5, 1, 4, 4},
		{"full board degrades to one", 1000, 5, 4, 4, 1},
		{"shrunk cap mid-flight clamps to one", 1000, 5, 7, 4, 1},
		{"unknown slot count is conservative", 1000, 5, -1, 4, 1},
		{"single-slot cap never fans out", 1000, 5, 0, 1, 1},
		{"exact multiple of batch size", 15, 5, 1, 4, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := followUpTriggerCount(tt.pending, tt.batchSize, tt.active, tt.max)
			require.Equal(t, tt.want, got)
		})
	}
}

// --- activeInflightSlots ----------------------------------------------------

func TestActiveInflightSlotsPurgesExpiredAndCountsLive(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc := &wikiIngestService{redisClient: rdb}
	ctx := context.Background()
	key := wikiInflightPrefix + "kb-1"

	// Two slots that expired long ago plus one live slot.
	require.NoError(t, rdb.ZAdd(ctx, key, redis.Z{
		Score:  float64(time.Now().Add(-time.Hour).UnixMilli()),
		Member: "expired-1",
	}).Err())
	require.NoError(t, rdb.ZAdd(ctx, key, redis.Z{
		Score:  float64(time.Now().Add(-time.Minute).UnixMilli()),
		Member: "expired-2",
	}).Err())
	require.NoError(t, rdb.ZAdd(ctx, key, redis.Z{
		Score:  float64(time.Now().Add(wikiInflightTTL).UnixMilli()),
		Member: "live-1",
	}).Err())

	require.Equal(t, 1, svc.activeInflightSlots(ctx, "kb-1"))
}

func TestActiveInflightSlotsUnknownOnRedisError(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	require.NoError(t, rdb.Close()) // every Eval now fails

	svc := &wikiIngestService{redisClient: rdb}
	require.Equal(t, -1, svc.activeInflightSlots(context.Background(), "kb-1"))
}

// --- scheduleFollowUp -------------------------------------------------------

func newFollowUpTestService(t *testing.T, pending int64) (*wikiIngestService, *followUpEnqueuerRecorder) {
	t.Helper()
	rec := &followUpEnqueuerRecorder{failFrom: -1}
	svc := &wikiIngestService{
		task:        rec,
		pendingRepo: &wikiPendingRepoForFollowUpTest{pending: pending},
	}
	return svc, rec
}

// reserveSlots claims n live slots the way n concurrent batches would, so the
// fan-out computation sees a realistic board.
func reserveSlots(t *testing.T, svc *wikiIngestService, kbID string, n, maxInflight int) {
	t.Helper()
	for i := 0; i < n; i++ {
		release, granted := svc.reserveInflightSlot(context.Background(), kbID, maxInflight)
		require.True(t, granted)
		t.Cleanup(release)
	}
}

func TestScheduleFollowUpFansOutToFreeSlots(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// 100 pending docs, batch size 10 → 10 batches needed. Two slots held
	// (one of them modelling the caller's own, which releases on return),
	// cap 4 → free = 4-2+1 = 3 follow-ups.
	svc, rec := newFollowUpTestService(t, 100)
	svc.redisClient = rdb
	reserveSlots(t, svc, "kb-1", 2, 4)

	got := svc.scheduleFollowUp(context.Background(),
		WikiIngestPayload{KnowledgeBaseID: "kb-1"}, wikiFollowUpDelay, 10, 4, false)
	require.True(t, got)
	require.Equal(t, 3, rec.count())
	rec.payloads(t, "kb-1")
}

func TestScheduleFollowUpBacklogSmallerThanBatchStaysSingle(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc, rec := newFollowUpTestService(t, 7)
	svc.redisClient = rdb
	reserveSlots(t, svc, "kb-1", 1, 4)

	got := svc.scheduleFollowUp(context.Background(),
		WikiIngestPayload{KnowledgeBaseID: "kb-1"}, wikiFollowUpDelay, 10, 4, false)
	require.True(t, got)
	require.Equal(t, 1, rec.count())
}

func TestScheduleFollowUpRateLimitedStaysSingle(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	// Even with the whole board free and a huge backlog, a rate-limited exit
	// must not widen concurrency: widening during a 429 storm amplifies it.
	svc, rec := newFollowUpTestService(t, 1000)
	svc.redisClient = rdb

	got := svc.scheduleFollowUp(context.Background(),
		WikiIngestPayload{KnowledgeBaseID: "kb-1"}, wikiRateLimitBackoff, 10, 32, true)
	require.True(t, got)
	require.Equal(t, 1, rec.count())
}

func TestScheduleFollowUpLiteModeStaysSingle(t *testing.T) {
	// No Redis client → liteLocks serializes batches per KB; extra triggers
	// would only bounce off ErrWikiIngestConcurrent.
	svc, rec := newFollowUpTestService(t, 1000)

	got := svc.scheduleFollowUp(context.Background(),
		WikiIngestPayload{KnowledgeBaseID: "kb-1"}, wikiFollowUpDelay, 10, 32, false)
	require.True(t, got)
	require.Equal(t, 1, rec.count())
}

func TestScheduleFollowUpNoPendingSchedulesNothing(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc, rec := newFollowUpTestService(t, 0)
	svc.redisClient = rdb

	got := svc.scheduleFollowUp(context.Background(),
		WikiIngestPayload{KnowledgeBaseID: "kb-1"}, wikiFollowUpDelay, 10, 4, false)
	require.False(t, got)
	require.Equal(t, 0, rec.count())
}

func TestScheduleFollowUpPartialEnqueueFailureStillReportsScheduled(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	svc, rec := newFollowUpTestService(t, 100)
	svc.redisClient = rdb
	rec.failFrom = 1
	rec.failErr = errors.New("enqueue failed")

	// The caller holds its own slot (releases on return): free = 4-1+1 = 4.
	// The first enqueue succeeds, the rest fail; "scheduled" must still be
	// true because at least one trigger got out.
	reserveSlots(t, svc, "kb-1", 1, 4)
	got := svc.scheduleFollowUp(context.Background(),
		WikiIngestPayload{KnowledgeBaseID: "kb-1"}, wikiFollowUpDelay, 10, 4, false)
	require.True(t, got)
	require.Equal(t, 4, rec.count())
}

// --- convergence ------------------------------------------------------------

// TestFollowUpFanOutConvergesUnderCap simulates successive batch completions
// fanning out on a board whose slots never free (worst case for over-issue)
// and asserts the per-completion trigger count never exceeds the free slots,
// i.e. the supply side alone cannot push concurrency past ingest_max_inflight.
func TestFollowUpFanOutConvergesUnderCap(t *testing.T) {
	const (
		maxInflight = 8
		batchSize   = 10
	)
	var (
		pending     = 1000
		activeSlots = 1 // the completing batch itself; releases on return
	)
	seen := map[int]bool{}
	for round := 0; round < 6; round++ {
		n := followUpTriggerCount(pending, batchSize, activeSlots, maxInflight)
		require.Greater(t, n, 0)
		require.LessOrEqual(t, n, maxInflight-activeSlots+1,
			"round %d: supply must not exceed free slots", round)
		if activeSlots == maxInflight {
			require.Equal(t, 1, n,
				"round %d: a full board must clamp supply to the follow-up chain", round)
		}
		seen[n] = true

		// The n triggers fire: n-1 of them claim slots (one bounces off the
		// cap into scheduleCappedRetry when the board is full), each claims a
		// batch of batchSize rows.
		newBatches := min(n, maxInflight-activeSlots)
		pending -= newBatches * batchSize
		activeSlots = maxInflight
		if pending <= 0 {
			pending = 1 // keep the loop meaningful; count stays capped below
		}
	}
	// The first round must have fanned out beyond the follow-up chain —
	// otherwise this would not be testing convergence at all.
	require.Greater(t, len(seen), 1)
}
