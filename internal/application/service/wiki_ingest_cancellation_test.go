package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiKnowledgeLookupStub struct {
	interfaces.KnowledgeService
	lookup func(context.Context, string) (*types.Knowledge, error)
}

func (s *wikiKnowledgeLookupStub) GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error) {
	return s.lookup(ctx, id)
}

// A canceled or failed lookup does not establish that the source was deleted.
// Map must return an error so the batch keeps the durable op for retry.
func TestWikiMapPreservesLookupFailures(t *testing.T) {
	for _, lookupErr := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database unavailable")} {
		t.Run(lookupErr.Error(), func(t *testing.T) {
			svc := &wikiIngestService{knowledgeSvc: &wikiKnowledgeLookupStub{
				lookup: func(context.Context, string) (*types.Knowledge, error) { return nil, lookupErr },
			}}
			result, updates, err := svc.mapOneDocument(context.Background(), nil,
				WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"},
				WikiPendingOp{KnowledgeID: "k-1", Op: WikiOpIngest}, nil)
			require.ErrorIs(t, err, lookupErr)
			assert.Nil(t, result)
			assert.Empty(t, updates)
		})
	}
}

// Reduce must not report a successful no-op when it cannot read a source:
// the batch otherwise deletes that document's durable op without its pages.
func TestWikiReducePreservesLookupFailures(t *testing.T) {
	for _, lookupErr := range []error{context.Canceled, context.DeadlineExceeded, errors.New("database unavailable")} {
		t.Run(lookupErr.Error(), func(t *testing.T) {
			svc := &wikiIngestService{knowledgeSvc: &wikiKnowledgeLookupStub{
				lookup: func(context.Context, string) (*types.Knowledge, error) { return nil, lookupErr },
			}}
			changed, _, _, err := svc.reduceSlugUpdates(context.Background(), nil, "kb-1", "entity/example",
				[]SlugUpdate{{Slug: "entity/example", Type: "entity", KnowledgeID: "k-1"}}, 7, nil, nil)
			require.ErrorIs(t, err, lookupErr)
			assert.False(t, changed)
		})
	}
}

func TestWikiLiveUpdatesKeepDeletionAndRetractionSemantics(t *testing.T) {
	lookupCalls := make(map[string]int)
	svc := &wikiIngestService{knowledgeSvc: &wikiKnowledgeLookupStub{
		lookup: func(_ context.Context, id string) (*types.Knowledge, error) {
			lookupCalls[id]++
			switch id {
			case "missing":
				return nil, fmt.Errorf("lookup: %w", apprepo.ErrKnowledgeNotFound)
			case "nil":
				return nil, nil
			case "deleting", "cancelled":
				return &types.Knowledge{ParseStatus: id}, nil
			default:
				return &types.Knowledge{ID: id, ParseStatus: types.ParseStatusFinalizing}, nil
			}
		},
	}}
	updates := []SlugUpdate{
		{Type: "entity", KnowledgeID: "live"},
		{Type: "summary", KnowledgeID: "live"},
		{Type: "entity", KnowledgeID: "missing"},
		{Type: "summary", KnowledgeID: "nil"},
		{Type: "entity", KnowledgeID: "deleting"},
		{Type: "entity", KnowledgeID: "cancelled"},
		{Type: "retract", KnowledgeID: "missing"},
		{Type: "retractStale", KnowledgeID: "missing"},
		{Type: "entity"},
	}
	got, err := svc.filterLiveUpdates(context.Background(), "kb-1", updates)
	require.NoError(t, err)
	assert.Equal(t, []SlugUpdate{updates[0], updates[1], updates[6], updates[7], updates[8]}, got)
	assert.Equal(t, 1, lookupCalls["live"], "shared source lookup is cached within the slug")
	assert.Equal(t, 1, lookupCalls["missing"])
}

func TestWikiLiveUpdatesRejectPartialLookupResults(t *testing.T) {
	lookupErr := errors.New("database unavailable")
	svc := &wikiIngestService{knowledgeSvc: &wikiKnowledgeLookupStub{
		lookup: func(_ context.Context, id string) (*types.Knowledge, error) {
			if id == "unreadable" {
				return nil, lookupErr
			}
			return &types.Knowledge{ID: id}, nil
		},
	}}
	got, err := svc.filterLiveUpdates(context.Background(), "kb-1", []SlugUpdate{
		{Type: "retract", KnowledgeID: "deleted"},
		{Type: "entity", KnowledgeID: "live"},
		{Type: "entity", KnowledgeID: "unreadable"},
	})
	require.ErrorIs(t, err, lookupErr)
	assert.Nil(t, got, "a partial slug must not be applied as a successful complete update")
}

func TestWikiKnowledgeTombstoneSkipsLookup(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	require.NoError(t, rdb.Set(ctx, WikiDeletedTombstoneKey("kb-1", "k-1"), "1", time.Minute).Err())
	svc := &wikiIngestService{redisClient: rdb, knowledgeSvc: &wikiKnowledgeLookupStub{
		lookup: func(context.Context, string) (*types.Knowledge, error) {
			t.Error("tombstoned knowledge should not require a database lookup")
			return nil, errors.New("database unavailable")
		},
	}}
	result, updates, err := svc.mapOneDocument(ctx, nil, WikiIngestPayload{KnowledgeBaseID: "kb-1"},
		WikiPendingOp{KnowledgeID: "k-1"}, nil)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Empty(t, updates)
}

type wikiLookupKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *wikiLookupKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *wikiLookupKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type wikiLookupModelService struct{ interfaces.ModelService }

func (wikiLookupModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	// The recovery fixture has no chunks, so the real pipeline skips the LLM.
	return nil, nil
}

type wikiLookupEmptyChunks struct{ interfaces.ChunkRepository }

func (wikiLookupEmptyChunks) ListChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	return nil, nil
}

func newWikiLookupRecoveryService(t *testing.T, kbID string, rdb *redis.Client) (*wikiIngestService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec(knowledgeTestDDL).Error)
	require.NoError(t, db.Exec("ALTER TABLE knowledges ADD COLUMN processed_at DATETIME").Error)
	require.NoError(t, db.AutoMigrate(&types.TaskPendingOp{}))
	require.NoError(t, db.Exec("CREATE TABLE tenants (id INTEGER PRIMARY KEY, deleted_at DATETIME)").Error)
	require.NoError(t, db.Exec("INSERT INTO tenants (id) VALUES (7)").Error)
	repo := apprepo.NewKnowledgeRepository(db)
	svc := &wikiIngestService{
		kbService: &wikiLookupKBService{kb: &types.KnowledgeBase{
			ID: kbID, SummaryModelID: "unused", IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
		}},
		modelService: wikiLookupModelService{}, chunkRepo: wikiLookupEmptyChunks{},
		knowledgeRepo: repo, knowledgeSvc: &wikiKnowledgeLookupStub{lookup: repo.GetKnowledgeByIDOnly},
		pendingRepo: apprepo.NewTaskPendingOpsRepository(db), task: &wikiGuardTaskQueue{}, redisClient: rdb,
	}
	return svc, db
}

func seedWikiLookupTask(t *testing.T, svc *wikiIngestService, db *gorm.DB, kbID, knowledgeID string) *asynq.Task {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO knowledges
		(id, tenant_id, knowledge_base_id, parse_status, pending_subtasks_count) VALUES (?, 7, ?, ?, 1)`,
		knowledgeID, kbID, types.ParseStatusFinalizing).Error)
	op, err := newWikiIngestPendingOp(context.Background(), 7, kbID, knowledgeID)
	require.NoError(t, err)
	require.NoError(t, svc.pendingRepo.Enqueue(context.Background(), op))
	payload, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: kbID})
	require.NoError(t, err)
	return asynq.NewTask(types.TypeWikiIngest, payload)
}

// Exercise the real batch handler, durable repository and finalizing counter.
// Cancellation happens after the op is claimed, at the source-document read.
func TestWikiIngestRetainsInterruptedLookupAndRecovers(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		t.Run(mode, func(t *testing.T) {
			var rdb *redis.Client
			if mode == "redis" {
				mr := miniredis.RunT(t)
				rdb = redis.NewClient(&redis.Options{Addr: mr.Addr()})
				t.Cleanup(func() { _ = rdb.Close() })
			}
			svc, db := newWikiLookupRecoveryService(t, "kb-1", rdb)
			task := seedWikiLookupTask(t, svc, db, "kb-1", "k-1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			lookup := svc.knowledgeSvc.(*wikiKnowledgeLookupStub)
			lookup.lookup = func(ctx context.Context, id string) (*types.Knowledge, error) {
				cancel()
				return svc.knowledgeRepo.GetKnowledgeByIDOnly(ctx, id)
			}
			require.NoError(t, svc.ProcessWikiIngest(ctx, task))
			var pending types.TaskPendingOp
			require.NoError(t, db.First(&pending).Error, "interrupted source must stay queued")
			assert.Equal(t, 1, pending.FailCount)
			assert.Nil(t, pending.ClaimedAt, "next batch must be able to reclaim immediately")
			kn, err := svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "k-1")
			require.NoError(t, err)
			assert.Equal(t, types.ParseStatusFinalizing, kn.ParseStatus)
			assert.Equal(t, 1, kn.PendingSubtasksCount)
			require.Len(t, svc.task.(*wikiGuardTaskQueue).tasks, 1, "retry must be scheduled")
			if rdb != nil {
				require.Zero(t, rdb.ZCard(context.Background(), wikiInflightPrefix+"kb-1").Val())
			}

			lookup.lookup = svc.knowledgeRepo.GetKnowledgeByIDOnly
			require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
			count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
			require.NoError(t, err)
			assert.Zero(t, count)
			kn, err = svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "k-1")
			require.NoError(t, err)
			assert.Equal(t, types.ParseStatusCompleted, kn.ParseStatus)
			assert.Zero(t, kn.PendingSubtasksCount)
		})
	}
}

func TestWikiMapClosesSpanAfterLookupCancellation(t *testing.T) {
	tracker, db := setupSpanTrackerTest(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, attempt, err := tracker.OpenAttempt(ctx, "k-1", "")
	require.NoError(t, err)
	require.NotNil(t, tracker.BeginStage(ctx, "k-1", attempt, types.StagePostProcess, nil))
	svc := &wikiIngestService{spanTracker: tracker, knowledgeSvc: &wikiKnowledgeLookupStub{
		lookup: func(ctx context.Context, _ string) (*types.Knowledge, error) {
			cancel()
			return nil, ctx.Err()
		},
	}}
	_, _, err = svc.mapOneDocument(ctx, nil, WikiIngestPayload{KnowledgeBaseID: "kb-1"},
		WikiPendingOp{KnowledgeID: "k-1"}, nil)
	require.ErrorIs(t, err, context.Canceled)
	var span types.KnowledgeProcessingSpan
	require.NoError(t, db.Where("name = ?", "postprocess.wiki").First(&span).Error)
	assert.Equal(t, types.SpanStatusFailed, span.Status, "interrupted map must not remain running in the trace")
	assert.NotNil(t, span.FinishedAt)
}

func TestWikiLookupFailureDoesNotBlockHealthySibling(t *testing.T) {
	svc, db := newWikiLookupRecoveryService(t, "kb-1", nil)
	seedWikiLookupTask(t, svc, db, "kb-1", "unreadable")
	task := seedWikiLookupTask(t, svc, db, "kb-1", "healthy")
	lookup := svc.knowledgeSvc.(*wikiKnowledgeLookupStub)
	lookup.lookup = func(ctx context.Context, id string) (*types.Knowledge, error) {
		if id == "unreadable" {
			return nil, errors.New("temporary read failure")
		}
		return svc.knowledgeRepo.GetKnowledgeByIDOnly(ctx, id)
	}
	require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	var ops []types.TaskPendingOp
	require.NoError(t, db.Find(&ops).Error)
	require.Len(t, ops, 1)
	assert.Equal(t, "unreadable", ops[0].DedupKey)
	healthy, err := svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "healthy")
	require.NoError(t, err)
	assert.Equal(t, types.ParseStatusCompleted, healthy.ParseStatus)
}

func TestWikiPersistentLookupFailureExhaustsRetryBudget(t *testing.T) {
	svc, db := newWikiLookupRecoveryService(t, "kb-1", nil)
	require.NoError(t, db.AutoMigrate(&types.TaskDeadLetter{}))
	svc.deadLetterRepo = apprepo.NewTaskDeadLetterRepository(db)
	task := seedWikiLookupTask(t, svc, db, "kb-1", "unreadable")
	svc.knowledgeSvc.(*wikiKnowledgeLookupStub).lookup = func(context.Context, string) (*types.Knowledge, error) {
		return nil, errors.New("persistent read failure")
	}
	for range wikiMaxFailRetries + 1 {
		require.NoError(t, svc.ProcessWikiIngest(context.Background(), task))
	}
	count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb-1")
	require.NoError(t, err)
	assert.Zero(t, count, "persistent failure must not retry forever")
	var letters []types.TaskDeadLetter
	require.NoError(t, db.Find(&letters).Error)
	require.Len(t, letters, 1)
	assert.Equal(t, "unreadable", letters[0].RelatedID)
	assert.Equal(t, wikiMaxFailRetries+1, letters[0].FailCount)
	kn, err := svc.knowledgeRepo.GetKnowledgeByIDOnly(context.Background(), "unreadable")
	require.NoError(t, err)
	assert.Zero(t, kn.PendingSubtasksCount, "exhausted work must release the finalizing slot")
	assert.NotEqual(t, types.ParseStatusFinalizing, kn.ParseStatus)
}
