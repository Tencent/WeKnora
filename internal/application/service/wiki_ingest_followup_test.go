package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// PendingCount and the eligible-document count configurable.
type wikiPendingRepoForFollowUpTest struct {
	wikiPendingRepoForCleanupTest
	pending      int64
	claimable    *int64
	claimableErr error
}

func (r *wikiPendingRepoForFollowUpTest) PendingCount(
	context.Context, string, string, string,
) (int64, error) {
	return r.pending, nil
}

func (r *wikiPendingRepoForFollowUpTest) ClaimableCount(
	context.Context, string, string, string, time.Time,
) (int64, error) {
	if r.claimable != nil {
		return *r.claimable, r.claimableErr
	}
	return r.pending, r.claimableErr
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
	require.Equal(t, 5, rec.count()) // four follow-ups and one recovery probe
}

// Use the actual asynq client so these tests exercise atomic reservations and
// queue state, rather than merely checking the single-completer arithmetic.
func newQueuedFollowUpTestService(t *testing.T) (*wikiIngestService, *asynq.Inspector) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	client := asynq.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return &wikiIngestService{
		redisClient: rdb,
		task:        client,
		pendingRepo: &wikiPendingRepoForFollowUpTest{pending: 24624},
	}, asynq.NewInspectorFromRedisClient(rdb)
}

func expireFollowUpChecks(t *testing.T, svc *wikiIngestService) {
	t.Helper()
	ctx := context.Background()
	ids, err := svc.redisClient.ZRange(ctx, wikiFollowUpPrefix+"kb-1", 0, -1).Result()
	require.NoError(t, err)
	for _, id := range ids {
		require.NoError(t, svc.redisClient.ZAdd(ctx, wikiFollowUpPrefix+"kb-1", redis.Z{
			Score: float64(time.Now().Add(-time.Minute).UnixMilli()), Member: id,
		}).Err())
	}
}

func TestScheduleFollowUpWorkerPoolBelowCap(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	ctx := context.Background()
	payload := WikiIngestPayload{KnowledgeBaseID: "kb-1"}
	// Startup recovery has one running batch, whose completion fans out.
	release, ok := svc.reserveInflightSlot(ctx, "kb-1", 32)
	require.True(t, ok)
	require.True(t, svc.scheduleFollowUp(ctx, payload, wikiFollowUpDelay, 10, 32, false))
	release()
	queued, err := inspector.ListScheduledTasks("wiki", asynq.PageSize(100))
	require.NoError(t, err)
	require.Len(t, queued, 32)

	// Model eight workers dequeuing tasks. Keep their follow-up reservations
	// until completion, just as the handler does, and consume one queued task
	// for each finished batch. No model, database or external Redis is used.
	runningIDs := make([]string, 8)
	for i := range runningIDs {
		runningIDs[i] = queued[i].ID
		require.NoError(t, inspector.DeleteTask("wiki", queued[i].ID))
	}
	reserveSlots(t, svc, "kb-1", 8, 32)
	for completed := 0; completed < 100; completed++ {
		svc.pendingRepo.(*wikiPendingRepoForFollowUpTest).pending -= 10
		require.True(t, svc.scheduleFollowUp(ctx, payload, wikiFollowUpDelay, 10, 32, false))
		worker := completed % len(runningIDs)
		require.NoError(t, svc.redisClient.ZRem(ctx, wikiFollowUpPrefix+"kb-1", runningIDs[worker]).Err())
		queued, err = inspector.ListScheduledTasks("wiki", asynq.PageSize(100))
		require.NoError(t, err)
		require.NotEmpty(t, queued)
		runningIDs[worker] = queued[0].ID
		require.NoError(t, inspector.DeleteTask("wiki", queued[0].ID))
		info, err := inspector.GetQueueInfo("wiki")
		require.NoError(t, err)
		require.LessOrEqual(t, info.Scheduled, 25, "completion %d must not multiply the queue", completed)
	}
}

func TestScheduleFollowUpConcurrentCompletionsShareBudget(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	reserveSlots(t, svc, "kb-1", 16, 32)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			svc.scheduleFollowUp(context.Background(), WikiIngestPayload{KnowledgeBaseID: "kb-1"},
				wikiFollowUpDelay, 10, 32, false)
		}()
	}
	close(start)
	wg.Wait()
	info, err := inspector.GetQueueInfo("wiki")
	require.NoError(t, err)
	// Cap + one successor spare + at most one coalesced recovery probe.
	require.LessOrEqual(t, info.Scheduled, 34)
	require.Equal(t, int64(33), svc.redisClient.ZCard(context.Background(), wikiFollowUpPrefix+"kb-1").Val())
}

func TestScheduleFollowUpClaimedRowsOnlyKeepsCrashRecovery(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	zero := int64(0)
	svc.pendingRepo.(*wikiPendingRepoForFollowUpTest).claimable = &zero
	require.True(t, svc.scheduleFollowUp(context.Background(), WikiIngestPayload{KnowledgeBaseID: "kb-1"},
		wikiFollowUpDelay, 10, 32, false))
	tasks, err := inspector.ListScheduledTasks("wiki")
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	require.Equal(t, "wiki-ingest-recheck-kb-1", tasks[0].ID)
	require.Greater(t, time.Until(tasks[0].NextProcessAt), wikiClaimStaleAfter-time.Second)
}

func TestFollowUpReservationsKeepLongQueuedTasks(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	ctx := context.Background()
	payload := WikiIngestPayload{KnowledgeBaseID: "kb-1"}
	require.True(t, svc.scheduleFollowUp(ctx, payload, wikiFollowUpDelay, 10, 4, false))
	for i := 0; i < 5; i++ {
		expireFollowUpChecks(t, svc)
		require.True(t, svc.scheduleFollowUp(ctx, payload, wikiFollowUpDelay, 10, 4, false))
	}
	info, err := inspector.GetQueueInfo("wiki")
	require.NoError(t, err)
	require.LessOrEqual(t, info.Scheduled, 6) // five reserved tasks plus one probe
	require.Equal(t, int64(5), svc.redisClient.ZCard(ctx, wikiFollowUpPrefix+"kb-1").Val())
}

func TestFollowUpReservationsRecoverMissingAndArchivedTasks(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	ctx := context.Background()
	require.True(t, svc.scheduleFollowUp(ctx, WikiIngestPayload{KnowledgeBaseID: "kb-1"},
		wikiFollowUpDelay, 10, 4, false))
	tasks, err := inspector.ListScheduledTasks("wiki")
	require.NoError(t, err)
	require.NoError(t, inspector.DeleteTask("wiki", tasks[0].ID))
	require.NoError(t, inspector.ArchiveTask("wiki", tasks[1].ID))
	expireFollowUpChecks(t, svc)
	require.NoError(t, svc.reconcileFollowUpTasks(ctx, "kb-1"))
	ids, err := svc.redisClient.ZRange(ctx, wikiFollowUpPrefix+"kb-1", 0, -1).Result()
	require.NoError(t, err)
	require.NotContains(t, ids, tasks[0].ID)
	require.NotContains(t, ids, tasks[1].ID)
	require.Len(t, ids, len(tasks)-2)
}

func TestFollowUpReservationsProtectEnqueueInProgress(t *testing.T) {
	svc, _ := newQueuedFollowUpTestService(t)
	ctx := context.Background()
	ids, err := svc.reserveFollowUpTasks(ctx, "kb-1", 5, 4)
	require.NoError(t, err)
	require.Len(t, ids, 5)
	// The task does not exist yet. Another completer must not recycle its
	// capacity while the first enqueue is still in flight.
	more, err := svc.reserveFollowUpTasks(ctx, "kb-1", 5, 4)
	require.NoError(t, err)
	require.Empty(t, more)
	// A producer that died before enqueueing must not consume capacity forever.
	expireFollowUpChecks(t, svc)
	more, err = svc.reserveFollowUpTasks(ctx, "kb-1", 5, 4)
	require.NoError(t, err)
	require.Len(t, more, 5)
}

// An enqueue can be committed by Redis even when its response is lost.
type lostFollowUpEnqueueResponse struct{ client *asynq.Client }

func (q lostFollowUpEnqueueResponse) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if _, err := q.client.Enqueue(task, opts...); err != nil {
		return nil, err
	}
	return nil, errors.New("enqueue response lost")
}

func TestFollowUpReservationsSurviveAmbiguousEnqueueFailure(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	client := svc.task.(*asynq.Client)
	svc.task = lostFollowUpEnqueueResponse{client: client}
	svc.scheduleFollowUp(context.Background(), WikiIngestPayload{KnowledgeBaseID: "kb-1"},
		wikiFollowUpDelay, 10, 4, false)
	svc.task = client
	expireFollowUpChecks(t, svc)
	require.NoError(t, svc.reconcileFollowUpTasks(context.Background(), "kb-1"))
	require.Equal(t, int64(5), svc.redisClient.ZCard(context.Background(), wikiFollowUpPrefix+"kb-1").Val())
	info, err := inspector.GetQueueInfo("wiki")
	require.NoError(t, err)
	require.Equal(t, 6, info.Scheduled) // five real tasks and the recovery probe
}

func TestFollowUpReservationHandlerLifecycle(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry=%t", fail), func(t *testing.T) {
			svc, inspector := newQueuedFollowUpTestService(t)
			kbService := &wikiGuardKBService{}
			if fail {
				kbService.err = errors.New("temporary KB lookup failure")
			}
			svc.kbService = kbService
			ctx := context.Background()
			ids, err := svc.reserveFollowUpTasks(ctx, "kb-1", 1, 1)
			require.NoError(t, err)
			require.Len(t, ids, 1)
			server := asynq.NewServer(asynq.RedisClientOpt{Addr: svc.redisClient.Options().Addr}, asynq.Config{
				Concurrency: 1, Queues: map[string]int{"wiki": 1},
				TaskCheckInterval: 10 * time.Millisecond, ShutdownTimeout: time.Second,
				RetryDelayFunc: func(int, error, *asynq.Task) time.Duration { return time.Hour },
				LogLevel:       asynq.ErrorLevel,
			})
			require.NoError(t, server.Start(asynq.HandlerFunc(svc.ProcessWikiIngest)))
			t.Cleanup(server.Shutdown)
			payload, err := json.Marshal(WikiIngestPayload{KnowledgeBaseID: "kb-1"})
			require.NoError(t, err)
			_, err = svc.task.Enqueue(asynq.NewTask("wiki:ingest", payload,
				asynq.Queue("wiki"), asynq.TaskID(ids[0])))
			require.NoError(t, err)
			if fail {
				require.Eventually(t, func() bool {
					info, err := inspector.GetTaskInfo("wiki", ids[0])
					return err == nil && info.State == asynq.TaskStateRetry
				}, 5*time.Second, 10*time.Millisecond)
				expireFollowUpChecks(t, svc)
				require.NoError(t, svc.reconcileFollowUpTasks(ctx, "kb-1"))
				require.Equal(t, int64(1), svc.redisClient.ZCard(ctx, wikiFollowUpPrefix+"kb-1").Val())
			} else {
				require.Eventually(t, func() bool {
					return svc.redisClient.ZCard(ctx, wikiFollowUpPrefix+"kb-1").Val() == 0
				}, 5*time.Second, 10*time.Millisecond, "successful handler must release its reservation")
			}
		})
	}
}

func TestFollowUpRecheckCanRearmItself(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	server := asynq.NewServer(asynq.RedisClientOpt{Addr: svc.redisClient.Options().Addr}, asynq.Config{
		Concurrency: 1, Queues: map[string]int{"wiki": 1},
		TaskCheckInterval: 10 * time.Millisecond, ShutdownTimeout: time.Second,
		LogLevel: asynq.ErrorLevel,
	})
	require.NoError(t, server.Start(asynq.HandlerFunc(func(ctx context.Context, _ *asynq.Task) error {
		if !svc.scheduleFollowUpRecheck(ctx, WikiIngestPayload{KnowledgeBaseID: "kb-1"}) {
			return errors.New("could not re-arm probe")
		}
		return nil
	})))
	t.Cleanup(server.Shutdown)
	_, err := svc.task.Enqueue(asynq.NewTask("wiki:ingest", nil,
		asynq.Queue("wiki"), asynq.TaskID("wiki-followup-recheck-kb-1")))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		info, err := inspector.GetTaskInfo("wiki", "wiki-followup-recheck-kb-1-next")
		return err == nil && info.State == asynq.TaskStateScheduled
	}, 5*time.Second, 10*time.Millisecond)
}

func TestFollowUpReservationsKeepCapacitySeparateForEachKB(t *testing.T) {
	svc, _ := newQueuedFollowUpTestService(t)
	ctx := context.Background()
	first, err := svc.reserveFollowUpTasks(ctx, "kb-1", 5, 4)
	require.NoError(t, err)
	require.Len(t, first, 5)
	second, err := svc.reserveFollowUpTasks(ctx, "kb-2", 5, 4)
	require.NoError(t, err)
	require.Len(t, second, 5)
	more, err := svc.reserveFollowUpTasks(ctx, "kb-1", 5, 4)
	require.NoError(t, err)
	require.Empty(t, more)
}

func TestScheduleFollowUpUsesRepositoryBatchLimit(t *testing.T) {
	svc, inspector := newQueuedFollowUpTestService(t)
	svc.pendingRepo.(*wikiPendingRepoForFollowUpTest).pending = 1500
	reserveSlots(t, svc, "kb-1", 1, 4)
	require.True(t, svc.scheduleFollowUp(context.Background(), WikiIngestPayload{KnowledgeBaseID: "kb-1"},
		wikiFollowUpDelay, 2000, 4, false))
	info, err := inspector.GetQueueInfo("wiki")
	require.NoError(t, err)
	require.Equal(t, 2, info.Scheduled, "ClaimBatch can claim at most 1000 documents per batch")
}
