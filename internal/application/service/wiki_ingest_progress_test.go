package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const wikiIngestProgressTestKB = "kb-progress"

// wikiIngestProgressDB 只使用临时 SQLite 或显式传入的测试 PG；每个用例独占 schema。
func wikiIngestProgressDB(t *testing.T, dialect string) *gorm.DB {
	t.Helper()
	config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	var db *gorm.DB
	var err error
	if dialect == "postgres" {
		dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
		if dsn == "" {
			t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for isolated PostgreSQL tests")
		}
		admin, openErr := gorm.Open(postgres.Open(dsn), config)
		require.NoError(t, openErr)
		adminSQL, openErr := admin.DB()
		require.NoError(t, openErr)
		schema := "wiki_ingest_progress_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
		t.Cleanup(func() {
			require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			require.NoError(t, adminSQL.Close())
		})
		separator := " "
		if strings.Contains(dsn, "://") {
			separator = "?"
			if strings.Contains(dsn, "?") {
				separator = "&"
			}
		}
		db, err = gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), config)
	} else {
		dsn := filepath.Join(t.TempDir(), "progress.db") +
			"?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
		db, err = gorm.Open(sqlite.Open(dsn), config)
	}
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	if dialect == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&types.TaskPendingOp{}, &types.WikiIngestPlan{}, &types.WikiIngestPageProgress{},
		&types.WikiPage{}, &types.WikiPageRevision{}, &types.WikiFolder{},
	))
	// 保留真实仓储查询的归属、软删除及结算列，夹具没有公司数据和模型供应商配置。
	for _, ddl := range []string{
		"CREATE TABLE tenants (id BIGINT PRIMARY KEY, deleted_at TIMESTAMP)",
		"CREATE TABLE knowledge_processing_spans (id VARCHAR(64) PRIMARY KEY, " +
			"knowledge_id VARCHAR(128), attempt INTEGER)",
		"CREATE TABLE knowledge_bases (id VARCHAR(64) PRIMARY KEY, tenant_id BIGINT NOT NULL, deleted_at TIMESTAMP)",
		"CREATE TABLE knowledges (id VARCHAR(128) PRIMARY KEY, tenant_id BIGINT NOT NULL, " +
			"knowledge_base_id VARCHAR(64) NOT NULL, title TEXT, parse_status VARCHAR(32), " +
			"pending_subtasks_count INTEGER NOT NULL DEFAULT 0, error_message TEXT, " +
			"processed_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)",
		"INSERT INTO tenants (id) VALUES (7)",
		"INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-progress', 7)",
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return db
}

func forWikiIngestProgressDBs(t *testing.T, run func(*testing.T, *gorm.DB)) {
	t.Helper()
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { run(t, wikiIngestProgressDB(t, dialect)) })
	}
}

// wikiIngestProgressPages 只注入指定写入/收尾故障；正文和回执仍由真实仓储原子提交。
type wikiIngestProgressPages struct {
	interfaces.WikiPageService
	mu               sync.Mutex
	createCalls      map[string]int
	updateCalls      map[string]int
	finishCalls      map[string]int
	failWriteSlug    string
	finishErr        error
	commitWasCleared bool
}

func (p *wikiIngestProgressPages) CreatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	p.mu.Lock()
	p.createCalls[page.Slug]++
	fail := p.failWriteSlug == page.Slug
	p.mu.Unlock()
	if fail {
		return nil, errors.New("synthetic page write failure")
	}
	return p.WikiPageService.CreatePage(ctx, page)
}

func (p *wikiIngestProgressPages) UpdatePage(ctx context.Context, page *types.WikiPage) (*types.WikiPage, error) {
	p.mu.Lock()
	p.updateCalls[page.Slug]++
	fail := p.failWriteSlug == page.Slug
	p.mu.Unlock()
	if fail {
		return nil, errors.New("synthetic page write failure")
	}
	return p.WikiPageService.UpdatePage(ctx, page)
}

func (p *wikiIngestProgressPages) ReconcileAppliedPage(
	ctx context.Context, tenantID uint64, kbID string, receipt types.WikiIngestPageProgress,
) error {
	p.mu.Lock()
	p.finishCalls[receipt.Slug]++
	p.commitWasCleared = p.commitWasCleared && types.WikiPageProgressCommitFromContext(ctx) == nil
	err := p.finishErr
	p.mu.Unlock()
	if err != nil {
		return err
	}
	return p.WikiPageService.(interfaces.WikiPageProgressFinisher).
		ReconcileAppliedPage(ctx, tenantID, kbID, receipt)
}

type wikiIngestProgressChunks struct {
	wikiLookupEmptyChunks
	calls atomic.Int32
}

func (c *wikiIngestProgressChunks) ListChunksByKnowledgeID(
	ctx context.Context, tenantID uint64, knowledgeID string,
) ([]*types.Chunk, error) {
	c.calls.Add(1)
	return c.wikiLookupEmptyChunks.ListChunksByKnowledgeID(ctx, tenantID, knowledgeID)
}

func (c *wikiIngestProgressChunks) ListChunksByKnowledgeIDAndTypes(
	ctx context.Context, tenantID uint64, knowledgeID string, kinds []types.ChunkType,
) ([]*types.Chunk, error) {
	c.calls.Add(1)
	return c.wikiLookupEmptyChunks.ListChunksByKnowledgeIDAndTypes(ctx, tenantID, knowledgeID, kinds)
}

// newWikiIngestProgressService 每次都创建新的 service、锁和缓存，重启测试只复用数据库。
func newWikiIngestProgressService(t *testing.T, db *gorm.DB) *wikiIngestService {
	t.Helper()
	queue := apprepo.NewTaskPendingOpsRepository(db)
	progress, ok := queue.(interfaces.WikiIngestProgressRepository)
	require.True(t, ok)
	knowledge := apprepo.NewKnowledgeRepository(db)
	pages := &wikiIngestProgressPages{
		WikiPageService: NewWikiPageService(apprepo.NewWikiPageRepository(db), nil, nil, nil, nil, nil),
		createCalls:     make(map[string]int), updateCalls: make(map[string]int),
		finishCalls: make(map[string]int), commitWasCleared: true,
	}
	return &wikiIngestService{
		wikiService: pages,
		kbService: &wikiLookupKBService{kb: &types.KnowledgeBase{
			ID: wikiIngestProgressTestKB, TenantID: 7, SummaryModelID: "synthetic-model",
			IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
			WikiConfig:       &types.WikiConfig{IngestMapParallel: 1, IngestReduceParallel: 1},
		}},
		modelService: wikiLookupModelService{}, chunkRepo: &wikiIngestProgressChunks{},
		knowledgeRepo: knowledge,
		knowledgeSvc:  &wikiKnowledgeLookupStub{lookup: knowledge.GetKnowledgeByIDOnly},
		pendingRepo:   queue, progressRepo: progress, task: &wikiGuardTaskQueue{},
	}
}

func enqueueWikiIngestProgressDoc(t *testing.T, svc *wikiIngestService, db *gorm.DB, id string) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, parse_status, pending_subtasks_count) "+
			"VALUES (?, 7, ?, 'synthetic document', 'finalizing', 2) ON CONFLICT (id) DO NOTHING",
		id, wikiIngestProgressTestKB,
	).Error)
	op := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: id, DocTitle: "synthetic document"}
	payload, err := json.Marshal(op)
	require.NoError(t, err)
	require.NoError(t, svc.pendingRepo.Enqueue(context.Background(), &types.TaskPendingOp{
		TenantID: 7, TaskType: wikiTaskType, Scope: wikiTaskScope, ScopeID: wikiIngestProgressTestKB,
		Op: op.Op, DedupKey: id, Payload: payload, EnqueuedAt: time.Now(),
	}))
}

func claimWikiIngestProgress(t *testing.T, svc *wikiIngestService, count int) (
	[]WikiPendingOp, []int64, *wikiProgressBatch,
) {
	t.Helper()
	ops, ids, batch, err := svc.claimWikiProgressBatch(
		context.Background(), WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB}, count,
	)
	require.NoError(t, err)
	require.Len(t, ops, count)
	require.Len(t, ids, count)
	return ops, ids, batch
}

func wikiIngestProgressUpdate(id, slug string) SlugUpdate {
	return SlugUpdate{
		KnowledgeID: id, Slug: slug, Type: "summary", DocTitle: "synthetic document",
		SourceRef: id, SummaryBody: "frozen synthetic body for " + slug, SummaryLine: "frozen summary",
	}
}

func prepareWikiIngestProgress(t *testing.T, svc *wikiIngestService, batch *wikiProgressBatch,
	ops []WikiPendingOp, updates ...SlugUpdate,
) map[string][]SlugUpdate {
	t.Helper()
	grouped := make(map[string][]SlugUpdate)
	for _, update := range updates {
		grouped[update.Slug] = append(grouped[update.Slug], update)
	}
	pending, _, _, err := batch.prepare(context.Background(), svc, ops, nil, nil, grouped, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	return pending
}

func wikiIngestProgressPage(slug string) *types.WikiPage {
	return &types.WikiPage{
		ID: uuid.NewString(), TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB,
		Slug: slug, Title: "synthetic", Content: "synthetic body", PageType: types.WikiPageTypeSummary,
		Status: types.WikiPageStatusDraft, Version: 1,
	}
}

func applyWikiIngestProgress(t *testing.T, svc *wikiIngestService, batch *wikiProgressBatch,
	slug string, updates []SlugUpdate,
) {
	t.Helper()
	changed, _, failed, deferred, err := batch.reduce(context.Background(), svc, slug, updates,
		func(ctx context.Context, got []SlugUpdate) (bool, string, bool, bool, error) {
			require.Equal(t, updates, got)
			_, err := svc.wikiService.CreatePage(ctx, wikiIngestProgressPage(slug))
			return true, "ingest", false, false, err
		})
	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, failed)
	require.False(t, deferred)
}

func loadWikiIngestProgress(
	t *testing.T, svc *wikiIngestService, claim types.WikiProgressClaim,
) *types.WikiIngestPlanState {
	t.Helper()
	state, err := svc.progressRepo.LoadWikiIngestPlan(context.Background(), claim)
	require.NoError(t, err)
	return state
}

func TestWikiIngestProgressRestartRestoresFrozenPlan(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, _, batch := claimWikiIngestProgress(t, svc, 1)
		first, second := wikiIngestProgressUpdate(
			"doc", "summary/first",
		), wikiIngestProgressUpdate("doc", "summary/second")
		grouped := map[string][]SlugUpdate{first.Slug: {first}, second.Slug: {second}}
		folders := &WikiBatchContext{PlannedFolderID: map[string]string{second.Slug: "original-folder"}}
		docs := []*docIngestResult{{
			KnowledgeID: "doc", DocTitle: "original title", Summary: "original summary",
			Pages: []wikiIngestPageRef{{Slug: first.Slug, Title: "first"}, {Slug: second.Slug, Title: "second"}},
		}}
		pending, _, _, err := batch.prepare(context.Background(), svc, ops, nil, docs, grouped, nil, folders)
		require.NoError(t, err)
		before := loadWikiIngestProgress(t, svc, batch.claims[ops[0].dbID])
		applyWikiIngestProgress(t, svc, batch, first.Slug, pending[first.Slug])
		require.NoError(t, batch.release(context.Background()))

		// 新 service 不继承旧缓存；传入不同的新 Map 结果也必须以首次持久计划为准。
		restarted := newWikiIngestProgressService(t, db)
		again, _, resumed := claimWikiIngestProgress(t, restarted, 1)
		require.True(t, resumed.restored(again[0]))
		replaced := wikiIngestProgressUpdate("doc", "summary/unexpected")
		restoredFolders := &WikiBatchContext{}
		left, recovered, affected, err := resumed.prepare(context.Background(), restarted, again, nil,
			[]*docIngestResult{{KnowledgeID: "doc", Summary: "incorrect replacement"}},
			map[string][]SlugUpdate{replaced.Slug: {replaced}}, nil, restoredFolders)
		require.NoError(t, err)
		require.Equal(t, map[string][]SlugUpdate{second.Slug: {second}}, left)
		require.Equal(t, []string{first.Slug}, affected)
		require.Len(t, recovered, 1)
		require.Equal(t, docs[0].Summary, recovered[0].Summary)
		require.Equal(t, docs[0].Pages, recovered[0].Pages)
		require.Equal(t, "original-folder", restoredFolders.PlannedFolderID[second.Slug])
		after := loadWikiIngestProgress(t, restarted, resumed.claims[again[0].dbID])
		require.Equal(t, []byte(before.Plan.Payload), []byte(after.Plan.Payload))
		require.Equal(t, before.Plan.CreatedAt, after.Plan.CreatedAt)
		require.Empty(t, restarted.wikiService.(*wikiIngestProgressPages).createCalls)
	})
}

func TestWikiIngestProgressSharedSlugFiltersOnlyAppliedContributions(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		for _, id := range []string{"doc-a", "doc-b"} {
			enqueueWikiIngestProgressDoc(t, svc, db, id)
		}
		ops, _, batch := claimWikiIngestProgress(t, svc, 2)
		a, b := wikiIngestProgressUpdate("doc-a", "summary/shared"), wikiIngestProgressUpdate("doc-b", "summary/shared")
		prepareWikiIngestProgress(t, svc, batch, ops, a, b)
		applyWikiIngestProgress(t, svc, batch, a.Slug, []SlugUpdate{a})
		require.NoError(t, batch.release(context.Background()))

		restarted := newWikiIngestProgressService(t, db)
		again, ids, resumed := claimWikiIngestProgress(t, restarted, 2)
		pending := prepareWikiIngestProgress(t, restarted, resumed, again)
		require.Equal(t, []SlugUpdate{b}, pending[b.Slug])
		_, _, _, _, err := resumed.reduce(context.Background(), restarted, b.Slug, pending[b.Slug],
			func(ctx context.Context, updates []SlugUpdate) (bool, string, bool, bool, error) {
				require.Equal(t, []SlugUpdate{b}, updates)
				commit := types.WikiPageProgressCommitFromContext(ctx)
				require.Len(t, commit.Claims, 1)
				require.Equal(t, "doc-b", commit.Claims[0].KnowledgeID)
				page, err := restarted.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, b.Slug)
				if err != nil {
					return false, "", false, false, err
				}
				page.Content += "\nsecond contribution"
				_, err = restarted.wikiService.UpdatePage(ctx, page)
				return true, "ingest", false, false, err
			})
		require.NoError(t, err)
		page, err := restarted.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, b.Slug)
		require.NoError(t, err)
		require.Equal(t, 2, page.Version)
		require.Equal(t, 1, strings.Count(page.Content, "second contribution"))
		require.NoError(t, resumed.settle(context.Background(), restarted, ids))
		var docs []types.Knowledge
		require.NoError(t, db.Find(&docs).Error)
		require.Len(t, docs, 2)
		for _, doc := range docs {
			require.Equal(t, 1, doc.PendingSubtasksCount)
		}
	})
}

func TestWikiIngestProgressAppliedRecoveryOnlyFinishesEffects(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, _, batch := claimWikiIngestProgress(t, svc, 1)
		update := wikiIngestProgressUpdate("doc", "summary/source")
		prepareWikiIngestProgress(t, svc, batch, ops, update)
		target := wikiIngestProgressPage("entity/target")
		require.NoError(t, apprepo.NewWikiPageRepository(db).Create(context.Background(), target))
		source := wikiIngestProgressPage(update.Slug)
		source.Content = "synthetic [[entity/target]]"
		commit := types.WikiPageProgressCommit{
			Slug: update.Slug, Claims: []types.WikiProgressClaim{batch.claims[ops[0].dbID]},
		}
		_, err := svc.wikiService.CreatePage(types.WithWikiPageProgressCommit(context.Background(), &commit), source)
		require.NoError(t, err)
		state := loadWikiIngestProgress(t, svc, commit.Claims[0])
		require.True(t, state.Pages[0].Applied)
		require.False(t, state.Pages[0].EffectsDone)
		require.NoError(t, batch.release(context.Background()))

		restarted := newWikiIngestProgressService(t, db)
		again, _, resumed := claimWikiIngestProgress(t, restarted, 1)
		pending := prepareWikiIngestProgress(t, restarted, resumed, again)
		require.Empty(t, pending, "已提交正文只补反链收尾，不能再次交给 reducer")
		pages := restarted.wikiService.(*wikiIngestProgressPages)
		require.Empty(t, pages.createCalls)
		require.Empty(t, pages.updateCalls)
		require.Equal(t, 1, pages.finishCalls[update.Slug])
		require.True(t, pages.commitWasCleared)
		state = loadWikiIngestProgress(t, restarted, resumed.claims[again[0].dbID])
		require.True(t, state.Pages[0].EffectsDone)
		targetAfter, err := restarted.wikiService.GetPageBySlug(
			context.Background(), wikiIngestProgressTestKB, target.Slug,
		)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{source.Slug}, targetAfter.InLinks)
		sourceAfter, err := restarted.wikiService.GetPageBySlug(
			context.Background(), wikiIngestProgressTestKB, source.Slug,
		)
		require.NoError(t, err)
		require.Equal(t, source.Version, sourceAfter.Version)
		require.Equal(t, source.Content, sourceAfter.Content)
	})
}

func TestWikiIngestProgressFinisherFailurePreservesAppliedReceipt(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, _, batch := claimWikiIngestProgress(t, svc, 1)
		update := wikiIngestProgressUpdate("doc", "summary/source")
		pending := prepareWikiIngestProgress(t, svc, batch, ops, update)
		want := errors.New("synthetic backlink failure")
		svc.wikiService.(*wikiIngestProgressPages).finishErr = want
		_, _, _, _, err := batch.reduce(context.Background(), svc, update.Slug, pending[update.Slug],
			func(ctx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
				_, err := svc.wikiService.CreatePage(ctx, wikiIngestProgressPage(update.Slug))
				return true, "ingest", false, false, err
			})
		require.ErrorIs(t, err, want)
		state := loadWikiIngestProgress(t, svc, batch.claims[ops[0].dbID])
		require.True(t, state.Pages[0].Applied)
		require.False(t, state.Pages[0].EffectsDone)
		require.ErrorIs(
			t, batch.settle(context.Background(), svc, []int64{ops[0].dbID}), types.ErrWikiProgressIncomplete,
		)
		require.NoError(t, batch.release(context.Background()))
		restarted := newWikiIngestProgressService(t, db)
		again, ids, resumed := claimWikiIngestProgress(t, restarted, 1)
		require.Empty(t, prepareWikiIngestProgress(t, restarted, resumed, again))
		require.NoError(t, resumed.settle(context.Background(), restarted, ids))
		require.Empty(t, restarted.wikiService.(*wikiIngestProgressPages).createCalls)
	})
}

func TestWikiIngestProgressFailureIsNotNoop(t *testing.T) {
	cases := []struct {
		name           string
		err            error
		additionFailed bool
		deferred       bool
		complete       bool
	}{
		{name: "model_error", err: errors.New("synthetic model failure")},
		{name: "empty_model_result", additionFailed: true},
		{name: "deferred_write", deferred: true},
		{name: "canceled", err: context.Canceled},
		{name: "confirmed_noop", complete: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				svc := newWikiIngestProgressService(t, db)
				enqueueWikiIngestProgressDoc(t, svc, db, "doc")
				ops, _, batch := claimWikiIngestProgress(t, svc, 1)
				update := wikiIngestProgressUpdate("doc", "summary/result")
				pending := prepareWikiIngestProgress(t, svc, batch, ops, update)
				_, _, failed, deferred, err := batch.reduce(
					context.Background(), svc, update.Slug, pending[update.Slug],
					func(context.Context, []SlugUpdate) (bool, string, bool, bool, error) {
						return false, "ingest", tc.additionFailed, tc.deferred, tc.err
					})
				require.ErrorIs(t, err, tc.err)
				require.Equal(t, tc.additionFailed, failed)
				require.Equal(t, tc.deferred, deferred)
				state := loadWikiIngestProgress(t, svc, batch.claims[ops[0].dbID])
				require.Equal(t, tc.complete, state.Pages[0].Applied)
				require.Equal(t, tc.complete, state.Pages[0].EffectsDone)
				require.False(t, state.Pages[0].Changed)
				require.Empty(t, state.Pages[0].PageID)
			})
		})
	}
}

func TestWikiIngestProgressOldClaimCannotCommitOrSettle(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, ids, old := claimWikiIngestProgress(t, svc, 1)
		update := wikiIngestProgressUpdate("doc", "summary/result")
		pending := prepareWikiIngestProgress(t, svc, old, ops, update)
		require.NoError(t, old.release(context.Background()))
		restarted := newWikiIngestProgressService(t, db)
		current, _, fresh := claimWikiIngestProgress(t, restarted, 1)
		require.Greater(t, fresh.claims[current[0].dbID].Epoch, old.claims[ops[0].dbID].Epoch)
		_, _, _, _, err := old.reduce(context.Background(), svc, update.Slug, pending[update.Slug],
			func(ctx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
				_, err := svc.wikiService.CreatePage(ctx, wikiIngestProgressPage(update.Slug))
				return true, "ingest", false, false, err
			})
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		// 失主结算允许幂等返回，但必须保留新领取及原文档计数。
		require.NoError(t, old.settle(context.Background(), svc, ids))
		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, ids[0]).Error)
		require.Equal(t, fresh.claims[current[0].dbID].Epoch, row.ClaimEpoch)
		require.NotNil(t, row.ClaimedAt)
		var doc types.Knowledge
		require.NoError(t, db.First(&doc, "id = ?", "doc").Error)
		require.Equal(t, 2, doc.PendingSubtasksCount)
		var pageCount int64
		require.NoError(t, db.Model(&types.WikiPage{}).Count(&pageCount).Error)
		require.Zero(t, pageCount)
	})
}

func TestWikiIngestProgressCanceledWriteLeavesPagePending(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, _, batch := claimWikiIngestProgress(t, svc, 1)
		update := wikiIngestProgressUpdate("doc", "summary/canceled")
		pending := prepareWikiIngestProgress(t, svc, batch, ops, update)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		_, _, _, _, err := batch.reduce(ctx, svc, update.Slug, pending[update.Slug],
			func(ctx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
				cancel()
				_, err := svc.wikiService.CreatePage(ctx, wikiIngestProgressPage(update.Slug))
				return true, "ingest", false, false, err
			})
		require.ErrorIs(t, err, context.Canceled)
		state := loadWikiIngestProgress(t, svc, batch.claims[ops[0].dbID])
		require.False(t, state.Pages[0].Applied)
		var count int64
		require.NoError(t, db.Model(&types.WikiPage{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestWikiIngestProgressRebuildDoesNotReuseDeletedGeneration(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, ids, old := claimWikiIngestProgress(t, svc, 1)
		update := wikiIngestProgressUpdate("doc", "summary/old")
		pending := prepareWikiIngestProgress(t, svc, old, ops, update)
		require.NoError(t, svc.pendingRepo.DeleteByIDs(context.Background(), ids))
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		restarted := newWikiIngestProgressService(t, db)
		current, _, fresh := claimWikiIngestProgress(t, restarted, 1)
		require.NotEqual(t, ids[0], current[0].dbID)
		require.False(t, fresh.restored(current[0]))
		next := wikiIngestProgressUpdate("doc", "summary/new")
		require.Equal(t, map[string][]SlugUpdate{next.Slug: {next}},
			prepareWikiIngestProgress(t, restarted, fresh, current, next))
		_, _, _, _, err := old.reduce(context.Background(), svc, update.Slug, pending[update.Slug],
			func(ctx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
				_, err := svc.wikiService.CreatePage(ctx, wikiIngestProgressPage(update.Slug))
				return true, "ingest", false, false, err
			})
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		var oldPlans int64
		require.NoError(t, db.Model(&types.WikiIngestPlan{}).Where("pending_op_id = ?", ids[0]).Count(&oldPlans).Error)
		require.Zero(t, oldPlans)
		state := loadWikiIngestProgress(t, restarted, fresh.claims[current[0].dbID])
		require.Len(t, state.Pages, 1)
		require.Equal(t, next.Slug, state.Pages[0].Slug)
		require.False(t, state.Pages[0].Applied)
	})
}

func TestWikiIngestProgressSettlementDoesNotDrainTwice(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "doc")
		ops, ids, batch := claimWikiIngestProgress(t, svc, 1)
		update := wikiIngestProgressUpdate("doc", "summary/noop")
		pending := prepareWikiIngestProgress(t, svc, batch, ops, update)
		require.ErrorIs(t, batch.settle(context.Background(), svc, ids), types.ErrWikiProgressIncomplete)
		_, _, _, _, err := batch.reduce(context.Background(), svc, update.Slug, pending[update.Slug],
			func(context.Context, []SlugUpdate) (bool, string, bool, bool, error) {
				return false, "ingest", false, false, nil
			})
		require.NoError(t, err)
		for range 2 {
			require.NoError(t, batch.settle(context.Background(), svc, ids))
		}
		var doc types.Knowledge
		require.NoError(t, db.First(&doc, "id = ?", "doc").Error)
		require.Equal(t, 1, doc.PendingSubtasksCount, "同一个队列槽只能释放一次")
		require.Equal(t, types.ParseStatusFinalizing, doc.ParseStatus)
		var count int64
		require.NoError(t, db.Model(&types.WikiIngestPlan{}).Count(&count).Error)
		require.Zero(t, count)
		require.NoError(t, db.Model(&types.WikiIngestPageProgress{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestWikiIngestProgressConcurrentServicesClaimDisjointRows(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, svc, db, "first")
		enqueueWikiIngestProgressDoc(t, svc, db, "second")
		type result struct {
			ids []int64
			err error
		}
		results := make(chan result, 2)
		start := make(chan struct{})
		for range 2 {
			worker := newWikiIngestProgressService(t, db)
			go func() {
				<-start
				_, ids, _, err := worker.claimWikiProgressBatch(context.Background(),
					WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB}, 1)
				results <- result{ids: ids, err: err}
			}()
		}
		close(start)
		first, second := <-results, <-results
		require.NoError(t, first.err)
		require.NoError(t, second.err)
		require.Len(t, first.ids, 1)
		require.Len(t, second.ids, 1)
		require.NotEqual(t, first.ids[0], second.ids[0])
	})
}

// TestProcessWikiIngestRestartsFromPersistedPageProgress 使用真实编排、队列和页面仓储。
// Map 输出为合成摘要计划，仅验证服务与数据库连接重建后的恢复，不冒充进程强杀或模型端到端。
func TestProcessWikiIngestRestartsFromPersistedPageProgress(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		seed := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, seed, db, "doc")
		ops, ids, batch := claimWikiIngestProgress(t, seed, 1)
		first := wikiIngestProgressUpdate("doc", "summary/success")
		second := wikiIngestProgressUpdate("doc", "summary/retry")
		prepareWikiIngestProgress(t, seed, batch, ops, first, second)
		frozen := append([]byte(nil), batch.states[ops[0].dbID].Plan.Payload...)
		require.NoError(t, batch.release(context.Background()))
		raw, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB})
		require.NoError(t, err)
		task := asynq.NewTask(types.TypeWikiIngest, raw)

		runner := newWikiIngestProgressService(t, db)
		runner.wikiService.(*wikiIngestProgressPages).failWriteSlug = second.Slug
		require.NoError(t, runner.ProcessWikiIngest(context.Background(), task))
		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, ids[0]).Error)
		require.Nil(t, row.ClaimedAt)
		require.Equal(t, 1, row.FailCount)
		var persisted types.WikiIngestPlan
		require.NoError(t, db.First(&persisted, "pending_op_id = ?", ids[0]).Error)
		require.Equal(t, frozen, []byte(persisted.Payload))
		var receipts []types.WikiIngestPageProgress
		require.NoError(t, db.Where("pending_op_id = ?", ids[0]).Order("slug").Find(&receipts).Error)
		require.Len(t, receipts, 2)
		for _, receipt := range receipts {
			require.Equal(t, receipt.Slug == first.Slug, receipt.Applied)
			require.Equal(t, receipt.Slug == first.Slug, receipt.EffectsDone)
		}
		require.Zero(t, runner.chunkRepo.(*wikiIngestProgressChunks).calls.Load())
		saved, err := runner.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, first.Slug)
		require.NoError(t, err)

		// 关闭旧连接池后，用同一临时文件或随机 schema 新建连接；不复用上一轮内存状态。
		var reopen gorm.Dialector
		switch original := db.Dialector.(type) {
		case *sqlite.Dialector:
			reopen = sqlite.Open(original.DSN)
		case *postgres.Dialector:
			reopen = postgres.Open(original.DSN)
		default:
			t.Fatalf("unexpected test dialect %T", db.Dialector)
		}
		oldSQL, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, oldSQL.Close())
		db, err = gorm.Open(reopen, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		newSQL, err := db.DB()
		require.NoError(t, err)
		require.NotSame(t, oldSQL, newSQL)
		if db.Name() == "sqlite" {
			newSQL.SetMaxOpenConns(1)
		}
		// 后注册的清理先关闭新连接，原夹具随后关闭旧连接并删除 PG schema。
		t.Cleanup(func() { require.NoError(t, newSQL.Close()) })
		restarted := newWikiIngestProgressService(t, db)
		require.NoError(t, restarted.ProcessWikiIngest(context.Background(), task))
		count, err := restarted.pendingRepo.PendingCount(
			context.Background(), wikiTaskType, wikiTaskScope, wikiIngestProgressTestKB,
		)
		require.NoError(t, err)
		require.Zero(t, count)
		writes := restarted.wikiService.(*wikiIngestProgressPages)
		require.Zero(t, writes.createCalls[first.Slug])
		require.Zero(t, writes.updateCalls[first.Slug])
		require.Equal(t, 1, writes.createCalls[second.Slug])
		require.Zero(t, restarted.chunkRepo.(*wikiIngestProgressChunks).calls.Load())
		unchanged, err := restarted.wikiService.GetPageBySlug(
			context.Background(), wikiIngestProgressTestKB, first.Slug,
		)
		require.NoError(t, err)
		require.Equal(t, saved.Version, unchanged.Version)
		require.Equal(t, saved.Content, unchanged.Content)
		retried, err := restarted.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, second.Slug)
		require.NoError(t, err)
		require.Equal(t, second.SummaryBody, retried.Content)
		require.Equal(t, types.WikiPageStatusPublished, retried.Status)
		var doc types.Knowledge
		require.NoError(t, db.First(&doc, "id = ?", "doc").Error)
		require.Equal(t, 1, doc.PendingSubtasksCount)

		require.NoError(t, restarted.ProcessWikiIngest(context.Background(), task))
		require.NoError(t, db.First(&doc, "id = ?", "doc").Error)
		require.Equal(t, 1, doc.PendingSubtasksCount, "重复任务投递不能再次结算")
		require.Equal(t, 1, writes.createCalls[second.Slug])
	})
}

// TestWikiIngestProgressGoneSourceDoesNotBlockHealthySibling 保证孤儿队列行不会反复占据同一批次。
// 清理遗留任务只消费其队列，不扣已删除或已移库文档的其他子任务计数。
func TestWikiIngestProgressGoneSourceDoesNotBlockHealthySibling(t *testing.T) {
	for _, gone := range []string{"deleted", "moved"} {
		t.Run(gone, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				svc := newWikiIngestProgressService(t, db)
				enqueueWikiIngestProgressDoc(t, svc, db, "gone")
				enqueueWikiIngestProgressDoc(t, svc, db, "healthy")
				var abandoned types.TaskPendingOp
				require.NoError(t, db.Where("dedup_key = ?", "gone").First(&abandoned).Error)
				if gone == "deleted" {
					require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "gone").
						UpdateColumn("deleted_at", time.Now()).Error)
				} else {
					require.NoError(t, db.Exec(
						"INSERT INTO knowledge_bases (id, tenant_id) VALUES ('other-kb', 7)",
					).Error)
					require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "gone").
						UpdateColumn("knowledge_base_id", "other-kb").Error)
				}

				ops, ids, batch, err := svc.claimWikiProgressBatch(context.Background(),
					WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB}, 2)
				require.NoError(t, err)
				require.Len(t, ops, 1)
				require.Equal(t, "healthy", ops[0].KnowledgeID)
				require.Len(t, ids, 1)
				require.Equal(t, ops[0].dbID, ids[0])
				var abandonedCount int64
				require.NoError(t, db.Model(&types.TaskPendingOp{}).
					Where("id = ?", abandoned.ID).Count(&abandonedCount).Error)
				require.Zero(t, abandonedCount)
				var previous types.Knowledge
				require.NoError(t, db.Unscoped().First(&previous, "id = ?", "gone").Error)
				require.Equal(t, 2, previous.PendingSubtasksCount)

				update := wikiIngestProgressUpdate("healthy", "summary/healthy")
				pending := prepareWikiIngestProgress(t, svc, batch, ops, update)
				applyWikiIngestProgress(t, svc, batch, update.Slug, pending[update.Slug])
				require.NoError(t, batch.settle(context.Background(), svc, ids))
				var healthy types.Knowledge
				require.NoError(t, db.First(&healthy, "id = ?", "healthy").Error)
				require.Equal(t, 1, healthy.PendingSubtasksCount)
				count, err := svc.pendingRepo.PendingCount(
					context.Background(), wikiTaskType, wikiTaskScope, wikiIngestProgressTestKB,
				)
				require.NoError(t, err)
				require.Zero(t, count)
			})
		})
	}
}

// TestProcessWikiIngestDoesNotPublishReplacementPage 防止旧页面回执按 slug 发布了重建后的新页面。
func TestProcessWikiIngestDoesNotPublishReplacementPage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		seed := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, seed, db, "doc")
		ops, ids, batch := claimWikiIngestProgress(t, seed, 1)
		update := wikiIngestProgressUpdate("doc", "summary/replaced")
		prepareWikiIngestProgress(t, seed, batch, ops, update)
		original := wikiIngestProgressPage(update.Slug)
		commit := types.WikiPageProgressCommit{
			Slug: update.Slug, Claims: []types.WikiProgressClaim{batch.claims[ops[0].dbID]},
		}
		_, err := seed.wikiService.CreatePage(
			types.WithWikiPageProgressCommit(context.Background(), &commit), original,
		)
		require.NoError(t, err)
		require.NoError(t, batch.release(context.Background()))

		// 物理清理仅发生在夹具临时库，用来构造同 slug 被另一次操作重新创建的边界。
		require.NoError(t, db.Unscoped().Delete(&types.WikiPage{}, "id = ?", original.ID).Error)
		replacement := wikiIngestProgressPage(update.Slug)
		replacement.Title = "new independent draft"
		replacement.Content = "new body must stay private until its own operation publishes it"
		replacement.Summary = "new independent summary"
		require.NotEqual(t, original.ID, replacement.ID)
		require.NoError(t, apprepo.NewWikiPageRepository(db).Create(context.Background(), replacement))

		restarted := newWikiIngestProgressService(t, db)
		raw, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB})
		require.NoError(t, err)
		require.NoError(t, restarted.ProcessWikiIngest(
			context.Background(), asynq.NewTask(types.TypeWikiIngest, raw),
		))
		current, err := restarted.wikiService.GetPageBySlug(
			context.Background(), wikiIngestProgressTestKB, replacement.Slug,
		)
		require.NoError(t, err)
		require.Equal(t, replacement.ID, current.ID)
		require.Equal(t, replacement.Content, current.Content)
		require.Equal(t, replacement.Title, current.Title)
		require.Equal(t, replacement.Summary, current.Summary)
		require.Equal(t, replacement.Version, current.Version)
		require.Equal(t, types.WikiPageStatusDraft, current.Status)
		writes := restarted.wikiService.(*wikiIngestProgressPages)
		require.Empty(t, writes.createCalls)
		require.Empty(t, writes.updateCalls)
		require.Zero(t, restarted.chunkRepo.(*wikiIngestProgressChunks).calls.Load())

		var oldQueue int64
		require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", ids[0]).Count(&oldQueue).Error)
		require.Zero(t, oldQueue)
		var newPageFinalize int64
		require.NoError(t, db.Model(&types.TaskPendingOp{}).
			Where("task_type = ? AND op = ? AND dedup_key = ?",
				wikiFinalizeTaskType, wikiFinalizeOpSlug, replacement.Slug).
			Count(&newPageFinalize).Error)
		require.Zero(t, newPageFinalize, "旧回执不能把新页加入本批 finalize")
	})
}

// TestWikiIngestProgressGoneDuplicateRowsAreDrained 覆盖去重只保留最后一条 op 的清理边界。
// 源删除后的原清理未执行时，本批同文档的全部旧任务及计划仍必须一起清除。
func TestWikiIngestProgressGoneDuplicateRowsAreDrained(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		svc := newWikiIngestProgressService(t, db)
		const duplicateCount = 3
		for range duplicateCount {
			enqueueWikiIngestProgressDoc(t, svc, db, "deleted-doc")
		}

		// 先为每条旧任务写入真实计划，再释放领取，模拟多次排队留下的持久状态。
		ctx := context.Background()
		rows, err := svc.pendingRepo.ClaimBatch(ctx, wikiTaskType, wikiTaskScope,
			wikiIngestProgressTestKB, duplicateCount, time.Now().Add(-time.Hour))
		require.NoError(t, err)
		require.Len(t, rows, duplicateCount)
		claims := make([]types.WikiProgressClaim, 0, duplicateCount)
		oldIDs := make([]int64, 0, duplicateCount)
		for _, row := range rows {
			claim := types.WikiProgressClaim{
				PendingOpID: row.ID, Epoch: row.ClaimEpoch, TenantID: 7,
				KnowledgeBaseID: wikiIngestProgressTestKB, KnowledgeID: "deleted-doc", Op: WikiOpIngest,
			}
			update := wikiIngestProgressUpdate("deleted-doc", "summary/deleted")
			payload, marshalErr := json.Marshal(wikiDurablePlan{
				Version: wikiIngestPlanVersion, Updates: []SlugUpdate{update},
			})
			require.NoError(t, marshalErr)
			state, saveErr := svc.progressRepo.SaveWikiIngestPlan(ctx, claim, payload, []string{update.Slug})
			require.NoError(t, saveErr)
			require.NotNil(t, state.Plan)
			require.Len(t, state.Pages, 1)
			claims = append(claims, claim)
			oldIDs = append(oldIDs, row.ID)
		}
		require.NoError(t, svc.progressRepo.ReleaseWikiIngestClaims(ctx, claims))
		require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", "deleted-doc").
			UpdateColumn("deleted_at", time.Now()).Error)

		ops, ids, _, err := svc.claimWikiProgressBatch(ctx,
			WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB}, duplicateCount)
		require.NoError(t, err)
		require.Empty(t, ops)
		require.Empty(t, ids)

		var remaining int64
		require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id IN ?", oldIDs).Count(&remaining).Error)
		require.Zero(t, remaining, "不能只清理去重后的最后一条旧任务")
		require.NoError(t, db.Model(&types.WikiIngestPlan{}).
			Where("pending_op_id IN ?", oldIDs).Count(&remaining).Error)
		require.Zero(t, remaining)
		require.NoError(t, db.Model(&types.WikiIngestPageProgress{}).
			Where("pending_op_id IN ?", oldIDs).Count(&remaining).Error)
		require.Zero(t, remaining)
		require.NoError(t, db.Model(&types.TaskPendingOp{}).
			Where("scope_id = ? AND claimed_at IS NOT NULL", wikiIngestProgressTestKB).Count(&remaining).Error)
		require.Zero(t, remaining, "不能遗留新领取等待失效窗口")
		count, err := svc.pendingRepo.PendingCount(ctx, wikiTaskType, wikiTaskScope, wikiIngestProgressTestKB)
		require.NoError(t, err)
		require.Zero(t, count)
		var doc types.Knowledge
		require.NoError(t, db.Unscoped().First(&doc, "id = ?", "deleted-doc").Error)
		require.Equal(t, 2, doc.PendingSubtasksCount, "清理遗留队列不释放已删除源的其他子任务")
	})
}
