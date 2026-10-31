package repository

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// wikiProgressTestDB 为每个场景创建隔离数据库/模式，不读取应用或公司的数据库配置。
func wikiProgressTestDB(t *testing.T, dialect string) *gorm.DB {
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
		schema := "wiki_progress_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
		dsn := filepath.Join(t.TempDir(), "progress.db") + "?_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on"
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
		&types.WikiPage{}, &types.WikiPageRevision{},
	))
	require.NoError(t, db.Exec(
		"CREATE TABLE knowledge_processing_spans (knowledge_id VARCHAR(128), attempt INTEGER)",
	).Error)
	// 用最小归属表保留与真实查询一致的软删除字段；本夹具没有模型供应商或业务数据。
	timestamp := "TIMESTAMP"
	require.NoError(t, db.Exec("CREATE TABLE tenants (id BIGINT PRIMARY KEY, deleted_at "+timestamp+")").Error)
	require.NoError(t, db.Exec("CREATE TABLE knowledge_bases (id VARCHAR(64) PRIMARY KEY, tenant_id "+
		"BIGINT NOT NULL, deleted_at "+timestamp+")").Error)
	require.NoError(t, db.Exec("CREATE TABLE knowledges (id VARCHAR(128) PRIMARY KEY, tenant_id BIGINT "+
		"NOT NULL, knowledge_base_id VARCHAR(64) NOT NULL, parse_status "+
		"VARCHAR(32), pending_subtasks_count INTEGER NOT NULL DEFAULT 0, "+
		"error_message TEXT, processed_at "+timestamp+", updated_at "+timestamp+", deleted_at "+timestamp+")").Error)
	require.NoError(t, db.Exec("INSERT INTO tenants (id) VALUES (1), (2)").Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb', 1), "+
		"('other-kb', 2)").Error)
	return db
}

// forWikiProgressDialects 对相同事务语义运行 SQLite 与显式启用的真实 PostgreSQL。
func forWikiProgressDialects(t *testing.T, run func(*testing.T, *gorm.DB)) {
	t.Helper()
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) { run(t, wikiProgressTestDB(t, dialect)) })
	}
}

func seedWikiProgressClaim(t *testing.T, db *gorm.DB, knowledgeID string) types.WikiProgressClaim {
	t.Helper()
	require.NoError(t, db.Exec("INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status, "+
		"pending_subtasks_count) VALUES (?, 1, 'kb', 'finalizing', 2)", knowledgeID).Error)
	op := &types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "kb", DedupKey: knowledgeID, Op: "ingest", Payload: json.RawMessage(`{"attempt":3}`),
		EnqueuedAt: time.Now(),
	}
	queue := NewTaskPendingOpsRepository(db)
	require.NoError(t, queue.Enqueue(context.Background(), op))
	rows, err := queue.ClaimBatch(
		context.Background(),
		types.TypeWikiIngest,
		types.TaskScopeKnowledgeBase,
		"kb",
		1,
		time.Now().Add(-time.Hour),
	)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, op.ID, rows[0].ID)
	require.Greater(t, rows[0].ClaimEpoch, int64(0))
	return types.WikiProgressClaim{
		PendingOpID: op.ID, Epoch: rows[0].ClaimEpoch,
		TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: knowledgeID, Op: "ingest", Attempt: 3, SourceAttempt: 3,
	}
}

func wikiProgressRepo(t *testing.T, db *gorm.DB) interfaces.WikiIngestProgressRepository {
	t.Helper()
	repo, ok := NewTaskPendingOpsRepository(db).(interfaces.WikiIngestProgressRepository)
	require.True(t, ok)
	return repo
}

func saveWikiProgressPlan(t *testing.T, db *gorm.DB, claim types.WikiProgressClaim, slugs ...string) {
	t.Helper()
	state, err := wikiProgressRepo(t, db).SaveWikiIngestPlan(
		context.Background(),
		claim,
		json.RawMessage(`{"version":1,"summary":"synthetic"}`),
		slugs,
	)
	require.NoError(t, err)
	require.Len(t, state.Pages, len(slugs))
}

func wikiProgressCommit(claim types.WikiProgressClaim, slug string) types.WikiPageProgressCommit {
	return types.WikiPageProgressCommit{Claims: []types.WikiProgressClaim{claim}, Slug: slug}
}

func wikiProgressPage(slug string) *types.WikiPage {
	return &types.WikiPage{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", Slug: slug,
		Content: "synthetic body", Title: "synthetic", PageType: types.WikiPageTypeEntity,
		Version: 1, OutLinks: types.StringArray{"entity/next"},
	}
}

func TestWikiProgressPlanFreezesFirstPayloadAndSlugs(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		repo := wikiProgressRepo(t, db)
		state, err := repo.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.Nil(t, state.Plan)
		first := json.RawMessage(`{"version":1,"title":"first"}`)
		state, err = repo.SaveWikiIngestPlan(
			context.Background(),
			claim,
			first,
			[]string{"entity/a", "entity/a", "entity/b"},
		)
		require.NoError(t, err)
		require.JSONEq(t, string(first), string(state.Plan.Payload))
		require.Len(t, state.Pages, 2)
		state, err = repo.SaveWikiIngestPlan(
			context.Background(),
			claim,
			json.RawMessage(`{"version":1,"title":"later"}`),
			[]string{"entity/c"},
		)
		require.NoError(t, err)
		require.JSONEq(t, string(first), string(state.Plan.Payload))
		require.Equal(t, []string{"entity/a", "entity/b"}, []string{state.Pages[0].Slug, state.Pages[1].Slug})
		_, err = repo.SaveWikiIngestPlan(context.Background(), claim, json.RawMessage("invalid-json"), nil)
		require.Error(t, err)
	})
}

func TestWikiProgressCreateAndReceiptCommitTogether(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		commit := wikiProgressCommit(claim, "entity/a")
		page := wikiProgressPage(commit.Slug)
		ctx := types.WithWikiPageProgressCommit(context.Background(), &commit)
		require.NoError(t, NewWikiPageRepository(db).Create(ctx, page))
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.True(t, state.Pages[0].Applied)
		require.False(t, state.Pages[0].EffectsDone)
		require.True(t, state.Pages[0].Changed)
		require.Equal(t, page.ID, state.Pages[0].PageID)
		require.Equal(t, 1, state.Pages[0].PageVersion)
		require.Empty(t, state.Pages[0].OldOutLinks)
		require.Equal(t, page.OutLinks, state.Pages[0].NewOutLinks)
		require.ErrorIs(
			t,
			NewWikiPageRepository(db).Create(ctx, wikiProgressPage(commit.Slug)),
			types.ErrWikiProgressAlreadyApplied,
		)
		require.NoError(t, wikiProgressRepo(t, db).FinishWikiPageEffects(context.Background(), commit))
	})
}

func TestWikiProgressReceiptFailureRollsBackPageAndRevision(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, operation := range []string{"create", "revision", "metadata"} {
			t.Run(operation, func(t *testing.T) {
				claim := seedWikiProgressClaim(t, db, "doc-"+operation)
				slug := "entity/" + operation
				saveWikiProgressPlan(t, db, claim, slug)
				page := wikiProgressPage(slug)
				repo := NewWikiPageRepository(db)
				if operation != "create" {
					require.NoError(t, repo.Create(context.Background(), page))
					page.OutLinks = types.StringArray{"entity/replacement"}
					page.Content = "changed body"
				}
				callback := "wiki_progress_receipt_failure"
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "wiki_ingest_page_progress" {
						_ = tx.AddError(errors.New("injected progress persistence failure"))
					}
				}))
				defer func() { require.NoError(t, db.Callback().Update().Remove(callback)) }()
				commit := wikiProgressCommit(claim, slug)
				ctx := types.WithWikiPageProgressCommit(context.Background(), &commit)
				var err error
				switch operation {
				case "create":
					err = repo.Create(ctx, page)
				case "revision":
					rev := &types.WikiPageRevision{
						ID: uuid.NewString(), PageID: page.ID, Version: 1, Content: "old body",
					}
					err = repo.UpdateWithRevision(ctx, page, rev)
				case "metadata":
					err = repo.UpdateMeta(ctx, page)
				}
				require.ErrorContains(t, err, "injected progress")
				require.Equal(t, 1, page.Version, "事务失败不能把内存版本留在未提交值")
				var count int64
				require.NoError(
					t,
					db.Model(&types.WikiPageRevision{}).Where("page_id = ?", page.ID).Count(&count).Error,
				)
				require.Zero(t, count)
				stored, loadErr := repo.GetByID(context.Background(), page.ID)
				if operation == "create" {
					require.ErrorIs(t, loadErr, ErrWikiPageNotFound)
				} else {
					require.NoError(t, loadErr)
					require.Equal(t, "synthetic body", stored.Content)
					require.Equal(t, types.StringArray{"entity/next"}, stored.OutLinks)
					require.Equal(t, 1, stored.Version)
				}
				state, loadErr := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
				require.NoError(t, loadErr)
				require.False(t, state.Pages[0].Applied)
			})
		}
	})
}

func TestWikiProgressUpdateRecordsLockedOldLinks(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, operation := range []string{"revision", "metadata"} {
			t.Run(operation, func(t *testing.T) {
				claim := seedWikiProgressClaim(t, db, "doc-"+operation)
				slug := "entity/" + operation
				saveWikiProgressPlan(t, db, claim, slug)
				page := wikiProgressPage(slug)
				repo := NewWikiPageRepository(db)
				require.NoError(t, repo.Create(context.Background(), page))
				page.OutLinks = types.StringArray{"entity/new"}
				commit := wikiProgressCommit(claim, slug)
				ctx := types.WithWikiPageProgressCommit(context.Background(), &commit)
				if operation == "revision" {
					page.Content = "new content"
					require.NoError(
						t,
						repo.UpdateWithRevision(
							ctx,
							page,
							&types.WikiPageRevision{ID: uuid.NewString(), PageID: page.ID, Version: 1},
						),
					)
				} else {
					require.NoError(t, repo.UpdateMeta(ctx, page))
				}
				state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
				require.NoError(t, err)
				require.Equal(t, types.StringArray{"entity/next"}, state.Pages[0].OldOutLinks)
				require.Equal(t, types.StringArray{"entity/new"}, state.Pages[0].NewOutLinks)
				require.Equal(t, page.Version, state.Pages[0].PageVersion)
			})
		}
	})
}

func TestWikiProgressReclaimFencesEveryOldWorkerMutation(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		old := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, old, "entity/a")
		queue := NewTaskPendingOpsRepository(db)
		require.NoError(
			t,
			db.Model(&types.TaskPendingOp{}).Where("id = ?", old.PendingOpID).Update(
				"claimed_at",
				time.Now().Add(-2*time.Hour),
			).Error,
		)
		rows, err := queue.ClaimBatch(
			context.Background(),
			types.TypeWikiIngest,
			types.TaskScopeKnowledgeBase,
			"kb",
			1,
			time.Now().Add(-time.Hour),
		)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		fresh := old
		fresh.Epoch = rows[0].ClaimEpoch
		require.Equal(t, old.Epoch+1, fresh.Epoch)
		progress := wikiProgressRepo(t, db)
		_, err = progress.LoadWikiIngestPlan(context.Background(), old)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		_, err = progress.SaveWikiIngestPlan(context.Background(), old, json.RawMessage("{}"), nil)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		require.ErrorIs(
			t,
			progress.CompleteWikiPageNoop(context.Background(), wikiProgressCommit(old, "entity/a")),
			types.ErrWikiProgressClaimLost,
		)
		require.ErrorIs(
			t,
			progress.FinishWikiPageEffects(context.Background(), wikiProgressCommit(old, "entity/a")),
			types.ErrWikiProgressClaimLost,
		)
		_, err = progress.IncrWikiIngestFailCount(context.Background(), old)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		require.NoError(t, progress.ReleaseWikiIngestClaims(context.Background(), []types.WikiProgressClaim{old}))
		settled, err := progress.SettleWikiIngestClaim(context.Background(), old, true, true)
		require.NoError(t, err)
		require.False(t, settled)
		commit := wikiProgressCommit(old, "entity/a")
		require.ErrorIs(
			t,
			NewWikiPageRepository(db).Create(
				types.WithWikiPageProgressCommit(context.Background(), &commit),
				wikiProgressPage("entity/a"),
			),
			types.ErrWikiProgressClaimLost,
		)
		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, old.PendingOpID).Error)
		require.NotNil(t, row.ClaimedAt)
		require.Equal(t, fresh.Epoch, row.ClaimEpoch)
		require.Zero(t, row.FailCount)
		_, err = progress.LoadWikiIngestPlan(context.Background(), fresh)
		require.NoError(t, err)
	})
}

func TestWikiProgressRejectsWrongScopeAndMissingPlan(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		progress := wikiProgressRepo(t, db)
		for _, mutate := range []func(*types.WikiProgressClaim){
			func(c *types.WikiProgressClaim) { c.TenantID++ },
			func(c *types.WikiProgressClaim) { c.KnowledgeBaseID = "other-kb" },
			func(c *types.WikiProgressClaim) { c.KnowledgeID = "other-doc" },
			func(c *types.WikiProgressClaim) { c.Op = "retract" },
			func(c *types.WikiProgressClaim) { c.Attempt++ },
			func(c *types.WikiProgressClaim) { c.Epoch = 0 },
		} {
			wrong := claim
			mutate(&wrong)
			_, err := progress.LoadWikiIngestPlan(context.Background(), wrong)
			require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		}
		commit := wikiProgressCommit(claim, "entity/a")
		require.ErrorIs(
			t,
			progress.CompleteWikiPageNoop(context.Background(), commit),
			types.ErrWikiProgressPlanMissing,
		)
		require.ErrorIs(
			t,
			NewWikiPageRepository(db).Create(
				types.WithWikiPageProgressCommit(context.Background(), &commit),
				wikiProgressPage(commit.Slug),
			),
			types.ErrWikiProgressPlanMissing,
		)
		saveWikiProgressPlan(t, db, claim, commit.Slug)
		commit.Slug = "entity/unplanned"
		require.ErrorIs(
			t,
			progress.CompleteWikiPageNoop(context.Background(), commit),
			types.ErrWikiProgressPlanMissing,
		)
	})
}

func TestWikiProgressDeletionCascadesAndFencesRebuild(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		old := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, old, "entity/a")
		require.NoError(
			t,
			NewTaskPendingOpsRepository(db).DeleteByDedupKey(
				context.Background(),
				types.TypeWikiIngest,
				types.TaskScopeKnowledgeBase,
				"kb",
				"doc",
				"ingest",
			),
		)
		for _, model := range []interface{}{&types.WikiIngestPlan{}, &types.WikiIngestPageProgress{}} {
			var count int64
			require.NoError(t, db.Model(model).Where("pending_op_id = ?", old.PendingOpID).Count(&count).Error)
			require.Zero(t, count)
		}
		_, err := wikiProgressRepo(t, db).SaveWikiIngestPlan(
			context.Background(),
			old,
			json.RawMessage("{}"),
			[]string{"entity/a"},
		)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		commit := wikiProgressCommit(old, "entity/a")
		require.ErrorIs(
			t,
			NewWikiPageRepository(db).Create(
				types.WithWikiPageProgressCommit(context.Background(), &commit),
				wikiProgressPage(commit.Slug),
			),
			types.ErrWikiProgressClaimLost,
		)
		// 模拟重建再次排入同文档：新任务 ID 不得继承旧页面完成状态。
		op := &types.TaskPendingOp{
			TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb", DedupKey: "doc", Op: "ingest", Payload: json.RawMessage(`{"attempt":4}`),
		}
		queue := NewTaskPendingOpsRepository(db)
		require.NoError(t, queue.Enqueue(context.Background(), op))
		require.NotEqual(t, old.PendingOpID, op.ID)
		rows, err := queue.ClaimBatch(
			context.Background(),
			types.TypeWikiIngest,
			types.TaskScopeKnowledgeBase,
			"kb",
			1,
			time.Now().Add(-time.Hour),
		)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		fresh := old
		fresh.PendingOpID, fresh.Epoch, fresh.Attempt = op.ID, rows[0].ClaimEpoch, 4
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), fresh)
		require.NoError(t, err)
		require.Nil(t, state.Plan)
	})
}

func TestWikiProgressSoftDeletedOwnersBlockCommit(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, table := range []string{"knowledges", "knowledge_bases", "tenants"} {
			t.Run(table, func(t *testing.T) {
				claim := seedWikiProgressClaim(t, db, "doc-"+table)
				slug := "entity/" + table
				saveWikiProgressPlan(t, db, claim, slug)
				where, id := "id = ?", interface{}(claim.KnowledgeID)
				if table == "knowledge_bases" {
					id = "kb"
				}
				if table == "tenants" {
					id = uint64(1)
				}
				require.NoError(t, db.Table(table).Where(where, id).Update("deleted_at", time.Now()).Error)
				commit := wikiProgressCommit(claim, slug)
				require.ErrorIs(
					t,
					NewWikiPageRepository(db).Create(
						types.WithWikiPageProgressCommit(context.Background(), &commit),
						wikiProgressPage(slug),
					),
					types.ErrWikiProgressOwnerGone,
				)
				progress := wikiProgressRepo(t, db)
				_, err := progress.LoadWikiIngestPlan(context.Background(), claim)
				require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
				ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, false, true)
				require.NoError(t, err)
				require.True(t, ok, "当前领取可以清理失效归属留下的旧任务")
				var count int
				require.NoError(
					t,
					db.Table("knowledges").Select("pending_subtasks_count").Where(
						"id = ?",
						claim.KnowledgeID,
					).Scan(&count).Error,
				)
				require.Equal(t, 2, count, "清理不能释放新一轮或已删除文档的计数")
				for _, model := range []interface{}{&types.WikiIngestPlan{}, &types.WikiIngestPageProgress{}} {
					var retained int64
					require.NoError(
						t,
						db.Model(model).Where("pending_op_id = ?", claim.PendingOpID).Count(&retained).Error,
					)
					require.Zero(t, retained)
				}
				require.NoError(t, db.Table(table).Where(where, id).Update("deleted_at", nil).Error)
			})
		}
	})
}

func TestWikiProgressNoopAndSettlementAreIdempotent(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		progress := wikiProgressRepo(t, db)
		ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.ErrorIs(t, err, types.ErrWikiProgressIncomplete)
		require.False(t, ok)
		commit := wikiProgressCommit(claim, "entity/a")
		require.ErrorIs(
			t,
			progress.FinishWikiPageEffects(context.Background(), commit),
			types.ErrWikiProgressIncomplete,
		)
		require.NoError(t, progress.CompleteWikiPageNoop(context.Background(), commit))
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.True(t, state.Pages[0].Applied)
		require.True(t, state.Pages[0].EffectsDone)
		require.False(t, state.Pages[0].Changed)
		require.ErrorIs(
			t,
			progress.CompleteWikiPageNoop(context.Background(), commit),
			types.ErrWikiProgressAlreadyApplied,
		)
		ok, err = progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.NoError(t, err)
		require.True(t, ok)
		ok, err = progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.NoError(t, err)
		require.False(t, ok)
		var count int
		require.NoError(
			t,
			db.Table("knowledges").Select("pending_subtasks_count").Where("id = ?", "doc").Scan(&count).Error,
		)
		require.Equal(t, 1, count, "重复消费不能再次释放同一个子任务计数")
	})
}

func TestWikiProgressSettlementFailureRollsBackCounterAndQueue(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		progress := wikiProgressRepo(t, db)
		require.NoError(t, progress.CompleteWikiPageNoop(
			context.Background(), wikiProgressCommit(claim, "entity/a"),
		))
		const callback = "wiki_progress_settle_failure"
		require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "task_pending_ops" {
				_ = tx.AddError(errors.New("injected delete failure"))
			}
		}))
		defer func() { require.NoError(t, db.Callback().Delete().Remove(callback)) }()
		ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.ErrorContains(t, err, "injected delete")
		require.False(t, ok)
		var count int
		require.NoError(
			t,
			db.Table("knowledges").Select("pending_subtasks_count").Where("id = ?", "doc").Scan(&count).Error,
		)
		require.Equal(t, 2, count)
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.True(t, state.Pages[0].Applied)
	})
}

func TestWikiProgressSharedPageCommitsEveryContributionOnce(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		first := seedWikiProgressClaim(t, db, "first")
		second := seedWikiProgressClaim(t, db, "second")
		for _, claim := range []types.WikiProgressClaim{first, second} {
			saveWikiProgressPlan(t, db, claim, "entity/shared")
		}
		commit := types.WikiPageProgressCommit{Claims: []types.WikiProgressClaim{second, first}, Slug: "entity/shared"}
		ctx := types.WithWikiPageProgressCommit(context.Background(), &commit)
		require.NoError(t, NewWikiPageRepository(db).Create(ctx, wikiProgressPage(commit.Slug)))
		for _, claim := range commit.Claims {
			state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
			require.NoError(t, err)
			require.True(t, state.Pages[0].Applied)
		}
		third := seedWikiProgressClaim(t, db, "third")
		saveWikiProgressPlan(t, db, third, commit.Slug)
		commit.Claims = []types.WikiProgressClaim{first, third}
		require.ErrorIs(
			t,
			NewWikiPageRepository(db).Create(
				types.WithWikiPageProgressCommit(context.Background(), &commit),
				wikiProgressPage(commit.Slug),
			),
			types.ErrWikiProgressAlreadyApplied,
		)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), third)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied, "部分已应用时，不能误标其余贡献")
	})
}

func TestWikiProgressConcurrentDuplicateCommitHasOneWinner(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		commit := wikiProgressCommit(claim, "entity/a")
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				page := wikiProgressPage(commit.Slug)
				results <- NewWikiPageRepository(db).Create(
					types.WithWikiPageProgressCommit(context.Background(), &commit),
					page,
				)
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		success, already := 0, 0
		for err := range results {
			if err == nil {
				success++
			} else if errors.Is(err, types.ErrWikiProgressAlreadyApplied) {
				already++
			} else {
				require.NoError(t, err)
			}
		}
		require.Equal(t, 1, success)
		require.Equal(t, 1, already)
	})
}

func TestWikiProgressCanceledCommitLeavesPending(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		commit := wikiProgressCommit(claim, "entity/a")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		require.Error(
			t,
			NewWikiPageRepository(db).Create(
				types.WithWikiPageProgressCommit(ctx, &commit),
				wikiProgressPage(commit.Slug),
			),
		)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied)
	})
}

func TestWikiProgressConcurrentPlanKeepsOneCompleteWinner(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		progress := wikiProgressRepo(t, db)
		start := make(chan struct{})
		type result struct {
			state *types.WikiIngestPlanState
			err   error
		}
		results := make(chan result, 2)
		for _, label := range []string{"first", "second"} {
			go func(label string) {
				<-start
				payload, _ := json.Marshal(map[string]string{"winner": label})
				state, err := progress.SaveWikiIngestPlan(
					context.Background(),
					claim,
					payload,
					[]string{"entity/" + label},
				)
				results <- result{state: state, err: err}
			}(label)
		}
		close(start)
		a, b := <-results, <-results
		require.NoError(t, a.err)
		require.NoError(t, b.err)
		require.JSONEq(t, string(a.state.Plan.Payload), string(b.state.Plan.Payload))
		require.Len(t, a.state.Pages, 1)
		require.Len(t, b.state.Pages, 1)
		require.Equal(t, a.state.Pages[0].Slug, b.state.Pages[0].Slug)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(a.state.Plan.Payload, &payload))
		require.Equal(t, "entity/"+payload["winner"], a.state.Pages[0].Slug, "计划与页面集合必须来自同一个 winner")
	})
}

func TestWikiProgressConcurrentSettlementReleasesOneSlot(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		progress := wikiProgressRepo(t, db)
		require.NoError(t, progress.CompleteWikiPageNoop(
			context.Background(), wikiProgressCommit(claim, "entity/a"),
		))
		start := make(chan struct{})
		type result struct {
			settled bool
			err     error
		}
		results := make(chan result, 2)
		for i := 0; i < 2; i++ {
			go func() {
				<-start
				ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
				results <- result{settled: ok, err: err}
			}()
		}
		close(start)
		a, b := <-results, <-results
		require.NoError(t, a.err)
		require.NoError(t, b.err)
		require.NotEqual(t, a.settled, b.settled, "只有一个消费者能消费同一任务")
		var count int
		require.NoError(
			t,
			db.Table("knowledges").Select("pending_subtasks_count").Where("id = ?", "doc").Scan(&count).Error,
		)
		require.Equal(t, 1, count)
	})
}

func TestWikiProgressPlanFailureLeavesNoPartialSlugs(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		const callback = "wiki_progress_plan_failure"
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "wiki_ingest_page_progress" {
				if page, ok := tx.Statement.Dest.(*types.WikiIngestPageProgress); ok && page.Slug == "entity/b" {
					_ = tx.AddError(errors.New("injected second slug failure"))
				}
			}
		}))
		defer func() { require.NoError(t, db.Callback().Create().Remove(callback)) }()
		progress := wikiProgressRepo(t, db)
		_, err := progress.SaveWikiIngestPlan(
			context.Background(),
			claim,
			json.RawMessage("{}"),
			[]string{"entity/a", "entity/b"},
		)
		require.ErrorContains(t, err, "injected second")
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.Nil(t, state.Plan)
		require.Empty(t, state.Pages)
		var count int64
		require.NoError(t, db.Model(&types.WikiIngestPageProgress{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestWikiProgressReleasePreservesCompletedPages(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a", "entity/b")
		progress := wikiProgressRepo(t, db)
		require.NoError(t, progress.CompleteWikiPageNoop(
			context.Background(), wikiProgressCommit(claim, "entity/a"),
		))
		count, err := progress.IncrWikiIngestFailCount(context.Background(), claim)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		require.NoError(t, progress.ReleaseWikiIngestClaims(context.Background(), []types.WikiProgressClaim{claim}))
		rows, err := NewTaskPendingOpsRepository(db).ClaimBatch(
			context.Background(),
			types.TypeWikiIngest,
			types.TaskScopeKnowledgeBase,
			"kb",
			1,
			time.Now().Add(-time.Hour),
		)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.Equal(t, claim.PendingOpID, rows[0].ID)
		claim.Epoch = rows[0].ClaimEpoch
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.True(t, state.Pages[0].Applied)
		require.False(t, state.Pages[1].Applied)
	})
}

func TestWikiProgressMigrationUpgradeDowngradeAndCascade(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		// 从真实旧结构升级，而不是用 AutoMigrate 替代发布迁移。
		require.NoError(t, db.Exec("DROP TABLE wiki_ingest_page_progress").Error)
		require.NoError(t, db.Exec("DROP TABLE wiki_ingest_plans").Error)
		require.NoError(t, db.Exec("ALTER TABLE task_pending_ops DROP COLUMN claim_epoch").Error)
		dialect, name := "sqlite", "000052_wiki_ingest_progress"
		if db.Name() == "postgres" {
			dialect, name = "versioned", "000135_wiki_ingest_progress"
		}
		up, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", dialect, name+".up.sql"))
		require.NoError(t, err)
		down, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", dialect, name+".down.sql"))
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(up)).Error)
		require.True(t, db.Migrator().HasColumn(&types.TaskPendingOp{}, "claim_epoch"))
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		// 使用直接删除验证恢复/housekeeping 等绕过 repository 的清理同样级联。
		require.NoError(t, db.Where("id = ?", claim.PendingOpID).Delete(&types.TaskPendingOp{}).Error)
		for _, model := range []interface{}{&types.WikiIngestPlan{}, &types.WikiIngestPageProgress{}} {
			var count int64
			require.NoError(t, db.Model(model).Count(&count).Error)
			require.Zero(t, count)
		}
		require.NoError(t, db.Exec(string(down)).Error)
		require.False(t, db.Migrator().HasTable(&types.WikiIngestPlan{}))
		require.False(t, db.Migrator().HasColumn(&types.TaskPendingOp{}, "claim_epoch"))
		require.NoError(t, db.Exec(string(up)).Error)
	})
}

func TestWikiProgressEffectsMustFinishBeforeSettlement(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		progress := wikiProgressRepo(t, db)
		commit := wikiProgressCommit(claim, "entity/a")
		require.NoError(
			t,
			NewWikiPageRepository(db).Create(
				types.WithWikiPageProgressCommit(context.Background(), &commit),
				wikiProgressPage(commit.Slug),
			),
		)
		ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.ErrorIs(t, err, types.ErrWikiProgressIncomplete)
		require.False(t, ok)
		require.NoError(t, progress.FinishWikiPageEffects(context.Background(), commit))
		require.NoError(t, progress.FinishWikiPageEffects(context.Background(), commit), "完成回执可安全重放")
		ok, err = progress.SettleWikiIngestClaim(context.Background(), claim, true, false)
		require.NoError(t, err)
		require.True(t, ok)
	})
}

func TestWikiProgressDiscardCanRemoveUnplannedOpWithoutFinalizing(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		ok, err := wikiProgressRepo(t, db).SettleWikiIngestClaim(context.Background(), claim, false, true)
		require.NoError(t, err)
		require.True(t, ok)
		var count int
		require.NoError(
			t,
			db.Table("knowledges").Select("pending_subtasks_count").Where("id = ?", "doc").Scan(&count).Error,
		)
		require.Equal(t, 2, count)
	})
}

func TestWikiProgressVersionConflictLeavesReceiptPending(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		page := wikiProgressPage("entity/a")
		repo := NewWikiPageRepository(db)
		require.NoError(t, repo.Create(context.Background(), page))
		require.NoError(
			t,
			db.Model(&types.WikiPage{}).Where(
				"id = ?",
				page.ID,
			).Updates(map[string]interface{}{"version": 2, "content": "concurrent edit"}).Error,
		)
		commit := wikiProgressCommit(claim, page.Slug)
		err := repo.UpdateWithRevision(
			types.WithWikiPageProgressCommit(context.Background(), &commit),
			page,
			&types.WikiPageRevision{ID: uuid.NewString(), PageID: page.ID, Version: 1},
		)
		require.ErrorIs(t, err, ErrWikiPageConflict)
		require.Equal(t, 1, page.Version)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied)
		stored, err := repo.GetByID(context.Background(), page.ID)
		require.NoError(t, err)
		require.Equal(t, "concurrent edit", stored.Content)
	})
}

func TestWikiProgressPageOwnershipCannotOverrideClaim(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		commit := wikiProgressCommit(claim, "entity/a")
		page := wikiProgressPage(commit.Slug)
		page.TenantID, page.KnowledgeBaseID = 2, "other-kb"
		require.ErrorIs(
			t,
			NewWikiPageRepository(db).Create(types.WithWikiPageProgressCommit(context.Background(), &commit), page),
			types.ErrWikiProgressClaimLost,
		)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied)
	})
}

func TestWikiProgressMovedOwnerCanDiscardCurrentClaim(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		require.NoError(
			t,
			db.Table("knowledges").Where("id = ?", claim.KnowledgeID).Update("knowledge_base_id", "other-kb").Error,
		)
		progress := wikiProgressRepo(t, db)
		_, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, false, true)
		require.NoError(t, err)
		require.True(t, ok)
		var count int
		require.NoError(
			t,
			db.Table("knowledges").Select("pending_subtasks_count").Where(
				"id = ?",
				claim.KnowledgeID,
			).Scan(&count).Error,
		)
		require.Equal(t, 2, count, "原空间的旧任务不能扣迁移后文档的计数")
	})
}

func TestWikiProgressStoppedOwnerBlocksRestoreAndDiscardsWithoutFinalizing(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, status := range []string{types.ParseStatusCancelled, types.ParseStatusDeleting} {
			t.Run(status, func(t *testing.T) {
				claim := seedWikiProgressClaim(t, db, "doc-"+status)
				saveWikiProgressPlan(t, db, claim, "entity/"+status)
				// 模拟状态已变更但队列 scrub 失败；计数使用新值，旧任务不得再释放。
				require.NoError(t, db.Table("knowledges").Where("id = ?", claim.KnowledgeID).
					Updates(map[string]interface{}{"parse_status": status, "pending_subtasks_count": 7}).Error)
				progress := wikiProgressRepo(t, db)
				_, err := progress.LoadWikiIngestPlan(context.Background(), claim)
				require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
				commit := wikiProgressCommit(claim, "entity/"+status)
				page := wikiProgressPage(commit.Slug)
				require.ErrorIs(
					t,
					NewWikiPageRepository(db).Create(
						types.WithWikiPageProgressCommit(context.Background(), &commit),
						page,
					),
					types.ErrWikiProgressOwnerGone,
				)
				require.ErrorIs(
					t,
					progress.CompleteWikiPageNoop(context.Background(), commit),
					types.ErrWikiProgressOwnerGone,
				)
				ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, false, true)
				require.NoError(t, err)
				require.True(t, ok)
				var row struct {
					ParseStatus          string
					PendingSubtasksCount int
				}
				require.NoError(
					t,
					db.Table("knowledges").Select(
						"parse_status",
						"pending_subtasks_count",
					).Where("id = ?", claim.KnowledgeID).Take(&row).Error,
				)
				require.Equal(t, status, row.ParseStatus)
				require.Equal(t, 7, row.PendingSubtasksCount)
				var count int64
				require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Count(&count).Error)
				require.Zero(t, count)
			})
		}
	})
}

func TestWikiProgressOtherParseStatesRemainEligible(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, status := range []interface{}{
			nil, "", types.ParseStatusProcessing, types.ParseStatusCompleted, types.ParseStatusFailed,
		} {
			claim := seedWikiProgressClaim(t, db, uuid.NewString())
			require.NoError(
				t,
				db.Table("knowledges").Where("id = ?", claim.KnowledgeID).Update("parse_status", status).Error,
			)
			state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(context.Background(), claim)
			require.NoError(t, err, "仅 cancelled/deleting 拒绝继续，其他状态保持既有语义")
			require.Nil(t, state.Plan)
		}
	})
}

func insertWikiProgressSourceAttempt(t *testing.T, db *gorm.DB, claim types.WikiProgressClaim, attempt int) {
	t.Helper()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_processing_spans (knowledge_id, attempt) VALUES (?, ?)",
		claim.KnowledgeID, attempt,
	).Error)
}

func seedWikiProgressLegacyClaim(t *testing.T, db *gorm.DB, id string) types.WikiProgressClaim {
	t.Helper()
	claim := seedWikiProgressClaim(t, db, id)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("id = ?", claim.PendingOpID).
		Update("payload", json.RawMessage("{}")).Error)
	claim.Attempt = 0
	claim.SourceAttempt = types.WikiSourceAttemptUnbound
	return claim
}

func TestWikiProgressSupersededSourceFencesOldPlanWithoutSuccessfulScrub(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a", "entity/b")
		progress := wikiProgressRepo(t, db)
		applied := wikiProgressCommit(claim, "entity/a")
		require.NoError(t, NewWikiPageRepository(db).Create(
			types.WithWikiPageProgressCommit(context.Background(), &applied), wikiProgressPage(applied.Slug),
		))
		// 保留旧 op 模拟 scrub 失败，新解析的 span 和计数已经落库。
		insertWikiProgressSourceAttempt(t, db, claim, 4)
		require.NoError(t, db.Table("knowledges").Where("id = ?", claim.KnowledgeID).
			Update("pending_subtasks_count", 7).Error)
		_, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		_, err = progress.SaveWikiIngestPlan(context.Background(), claim, json.RawMessage("{}"), nil)
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		pending := wikiProgressCommit(claim, "entity/b")
		require.ErrorIs(t, NewWikiPageRepository(db).Create(
			types.WithWikiPageProgressCommit(context.Background(), &pending), wikiProgressPage(pending.Slug),
		), types.ErrWikiProgressOwnerGone)
		require.ErrorIs(t, progress.CompleteWikiPageNoop(context.Background(), pending), types.ErrWikiProgressOwnerGone)
		require.ErrorIs(t,
			progress.FinishWikiPageEffects(context.Background(), applied), types.ErrWikiProgressOwnerGone,
		)
		ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, false, true)
		require.NoError(t, err)
		require.True(t, ok)
		var count int
		require.NoError(t, db.Table("knowledges").Select("pending_subtasks_count").
			Where("id = ?", claim.KnowledgeID).Scan(&count).Error)
		require.Equal(t, 7, count)
	})
}

func TestWikiProgressLegacyMapCannotAdoptAttemptStartedAfterLoad(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressLegacyClaim(t, db, "legacy")
		insertWikiProgressSourceAttempt(t, db, claim, 5)
		progress := wikiProgressRepo(t, db)
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.Equal(t, 5, state.SourceAttempt)
		claim.SourceAttempt = state.SourceAttempt
		insertWikiProgressSourceAttempt(t, db, claim, 6)
		_, err = progress.SaveWikiIngestPlan(context.Background(), claim, json.RawMessage("{}"), []string{"entity/a"})
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		_, err = progress.LoadWikiIngestPlan(context.Background(), claim)
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		commit := wikiProgressCommit(claim, "entity/a")
		require.ErrorIs(t, NewWikiPageRepository(db).Create(
			types.WithWikiPageProgressCommit(context.Background(), &commit), wikiProgressPage(commit.Slug),
		), types.ErrWikiProgressOwnerGone)
	})
}

func TestWikiProgressLegacyHistoryAndRetainedPlanSurviveSpanCleanup(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressLegacyClaim(t, db, "legacy")
		insertWikiProgressSourceAttempt(t, db, claim, 5)
		progress := wikiProgressRepo(t, db)
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		claim.SourceAttempt = state.SourceAttempt
		saveWikiProgressPlan(t, db, claim, "entity/a")
		state, err = progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.Equal(t, 5, state.Plan.SourceAttempt)
		require.NoError(t, db.Exec(
			"DELETE FROM knowledge_processing_spans WHERE knowledge_id = ?", claim.KnowledgeID,
		).Error)
		initialClaim := claim
		initialClaim.SourceAttempt = types.WikiSourceAttemptUnbound
		state, err = progress.LoadWikiIngestPlan(context.Background(), initialClaim)
		require.NoError(t, err)
		require.Zero(t, state.SourceAttempt)
		require.Equal(t, 5, state.Plan.SourceAttempt, "恢复沿用计划代次，不能因 span 清理降低栅栏")
		claim.SourceAttempt = state.Plan.SourceAttempt
		commit := wikiProgressCommit(claim, "entity/a")
		require.NoError(t, NewWikiPageRepository(db).Create(
			types.WithWikiPageProgressCommit(context.Background(), &commit), wikiProgressPage(commit.Slug),
		))
		require.NoError(t, progress.FinishWikiPageEffects(context.Background(), commit))
	})
}

func TestWikiProgressPersistedPlanRejectsNewlyReboundAttempt(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressLegacyClaim(t, db, "legacy")
		insertWikiProgressSourceAttempt(t, db, claim, 5)
		progress := wikiProgressRepo(t, db)
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		claim.SourceAttempt = state.SourceAttempt
		saveWikiProgressPlan(t, db, claim, "entity/a")
		insertWikiProgressSourceAttempt(t, db, claim, 6)
		claim.SourceAttempt = types.WikiSourceAttemptUnbound
		_, err = progress.LoadWikiIngestPlan(context.Background(), claim)
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		// 即使调用方误把新代次绑定到旧 op，冻结计划自己的代次仍会拒绝覆盖。
		claim.SourceAttempt = 6
		_, err = progress.SaveWikiIngestPlan(context.Background(), claim, json.RawMessage("{}"), nil)
		require.ErrorIs(t, err, types.ErrWikiProgressOwnerGone)
		commit := wikiProgressCommit(claim, "entity/a")
		require.ErrorIs(t, NewWikiPageRepository(db).Create(
			types.WithWikiPageProgressCommit(context.Background(), &commit), wikiProgressPage(commit.Slug),
		), types.ErrWikiProgressOwnerGone)
	})
}

func TestWikiProgressUnboundSourceAttemptIsReadOnly(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		claim := seedWikiProgressClaim(t, db, "doc")
		saveWikiProgressPlan(t, db, claim, "entity/a")
		claim.SourceAttempt = types.WikiSourceAttemptUnbound
		progress := wikiProgressRepo(t, db)
		state, err := progress.LoadWikiIngestPlan(context.Background(), claim)
		require.NoError(t, err)
		require.Equal(t, claim.Attempt, state.SourceAttempt)
		_, err = progress.SaveWikiIngestPlan(context.Background(), claim, json.RawMessage("{}"), nil)
		require.ErrorIs(t, err, types.ErrWikiProgressClaimLost)
		commit := wikiProgressCommit(claim, "entity/a")
		require.ErrorIs(t, NewWikiPageRepository(db).Create(
			types.WithWikiPageProgressCommit(context.Background(), &commit), wikiProgressPage(commit.Slug),
		), types.ErrWikiProgressClaimLost)
		require.ErrorIs(t, progress.CompleteWikiPageNoop(context.Background(), commit), types.ErrWikiProgressClaimLost)
		require.ErrorIs(t, progress.FinishWikiPageEffects(context.Background(), commit), types.ErrWikiProgressClaimLost)
	})
}

func TestWikiProgressSettlementRechecksAttemptBeforeReleasingCounter(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, discard := range []bool{false, true} {
			claim := seedWikiProgressClaim(t, db, uuid.NewString())
			saveWikiProgressPlan(t, db, claim, "entity/a")
			progress := wikiProgressRepo(t, db)
			require.NoError(t, progress.CompleteWikiPageNoop(
				context.Background(), wikiProgressCommit(claim, "entity/a"),
			))
			insertWikiProgressSourceAttempt(t, db, claim, 4)
			require.NoError(t, db.Table("knowledges").Where("id = ?", claim.KnowledgeID).
				Update("pending_subtasks_count", 7).Error)
			// 调用方仍传 finalize=true，模拟事务外检查之后才开始新解析。
			ok, err := progress.SettleWikiIngestClaim(context.Background(), claim, true, discard)
			require.NoError(t, err)
			require.True(t, ok)
			var count int
			require.NoError(t, db.Table("knowledges").Select("pending_subtasks_count").
				Where("id = ?", claim.KnowledgeID).Scan(&count).Error)
			require.Equal(t, 7, count)
		}
	})
}
