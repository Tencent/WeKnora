package service

import (
	"context"
	"fmt"
	"sort"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// wikiSourceFilterFixture 只替换模型和 Redis；计划、页面、回执和结算均使用真实仓储。
type wikiSourceFilterFixture struct {
	*wikiSettlementFixture
	model *progressBoundaryModel
}

// newWikiSourceFilterFixture 在设置删除标记之前冻结计划，模拟标记领先数据库删除的窗口。
func newWikiSourceFilterFixture(t *testing.T, db *gorm.DB, updates ...SlugUpdate) *wikiSourceFilterFixture {
	t.Helper()
	if db.Name() == "postgres" {
		// 原双库辅助函数按 Go 标记建成 JSON；公开迁移 000037 的此列为 JSONB。
		// 仅校准本测试新建的 schema，让真实的 @> 查询与部署结构一致。
		require.NoError(t, db.Exec(
			"ALTER TABLE wiki_pages ALTER COLUMN source_refs TYPE jsonb USING source_refs::jsonb",
		).Error)
	}
	svc := newWikiIngestProgressService(t, db)
	grouped := make(map[string][]SlugUpdate)
	byDocument := make(map[string]*docIngestResult)
	for _, update := range updates {
		grouped[update.Slug] = append(grouped[update.Slug], update)
		doc := byDocument[update.KnowledgeID]
		if doc == nil {
			enqueueWikiIngestProgressDoc(t, svc, db, update.KnowledgeID)
			require.NoError(t, db.Table("knowledges").Where("id = ?", update.KnowledgeID).
				Update("pending_subtasks_count", 1).Error)
			doc = &docIngestResult{KnowledgeID: update.KnowledgeID, DocTitle: update.KnowledgeID, Summary: "冻结的合成摘要"}
			byDocument[update.KnowledgeID] = doc
		}
		doc.Pages = append(doc.Pages, wikiIngestPageRef{Slug: update.Slug, Title: update.Slug})
	}
	ids := make([]string, 0, len(byDocument))
	for id := range byDocument {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	docs := make([]*docIngestResult, 0, len(ids))
	for _, id := range ids {
		docs = append(docs, byDocument[id])
	}
	ops, _, batch := claimWikiIngestProgress(t, svc, len(ids))
	pending, docs, _, err := batch.prepare(context.Background(), svc, ops, nil, docs, grouped, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	return wrapWikiSourceFilterFixture(svc, batch, pending, docs)
}

func wrapWikiSourceFilterFixture(svc *wikiIngestService, batch *wikiProgressBatch,
	pending map[string][]SlugUpdate, docs []*docIngestResult,
) *wikiSourceFilterFixture {
	base := &wikiSettlementFixture{svc: svc, batch: batch, pending: pending, docs: docs}
	base.c = newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), docs, pending)
	batch.settlement = base.c
	model := &progressBoundaryModel{}
	model.response = "SUMMARY: 合成摘要\n\n本次归并的合成正文。"
	return &wikiSourceFilterFixture{wikiSettlementFixture: base, model: model}
}

func (f *wikiSourceFilterFixture) reduce(slug string) error {
	_, _, _, _, err := f.batch.reduce(context.Background(), f.svc, slug, f.pending[slug],
		func(ctx context.Context, updates []SlugUpdate) (bool, string, bool, bool, error) {
			return f.svc.reduceSlugUpdates(ctx, f.model, wikiIngestProgressTestKB, slug, updates, 7,
				f.svc.newWikiBatchContext(wikiIngestProgressTestKB, nil), nil)
		})
	return err
}

// resume 只复用数据库，重新领取冻结计划；不继承旧 service 的缓存或模型计数。
func (f *wikiSourceFilterFixture) resume(t *testing.T, db *gorm.DB, documents int) *wikiSourceFilterFixture {
	t.Helper()
	require.NoError(t, f.batch.release(context.Background()))
	svc := newWikiIngestProgressService(t, db)
	ops, _, batch := claimWikiIngestProgress(t, svc, documents)
	pending, docs, _, err := batch.prepare(context.Background(), svc, ops, nil, nil, nil, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	return wrapWikiSourceFilterFixture(svc, batch, pending, docs)
}

// markWikiSourceFilterDeletion 不修改数据库，使原领取继续有效，避免仓储删除保护遮住本缺陷。
func markWikiSourceFilterDeletion(t *testing.T, svc *wikiIngestService, id string) func() {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	svc.redisClient = client
	key := WikiDeletedTombstoneKey(wikiIngestProgressTestKB, id)
	require.NoError(t, client.Set(context.Background(), key, "1", 0).Err())
	return func() { require.NoError(t, client.Del(context.Background(), key).Err()) }
}

func assertWikiSourceFilterReceipts(t *testing.T, db *gorm.DB, slug string, count int, applied bool) {
	t.Helper()
	var rows []types.WikiIngestPageProgress
	require.NoError(t, db.Where("slug = ?", slug).Find(&rows).Error)
	require.Len(t, rows, count)
	for _, row := range rows {
		assert.Equal(t, applied, row.Applied, "pending_op_id=%d", row.PendingOpID)
		assert.Equal(t, applied, row.EffectsDone, "pending_op_id=%d", row.PendingOpID)
		if !applied {
			assert.Empty(t, row.PageID)
		}
	}
}

// assertWikiSourceFilterRejected 同时核对页面与所有贡献者，防止只保护被过滤文档而提前结算健康文档。
func assertWikiSourceFilterRejected(
	t *testing.T, db *gorm.DB, updates ...SlugUpdate,
) (*wikiSourceFilterFixture, func()) {
	t.Helper()
	f := newWikiSourceFilterFixture(t, db, updates...)
	slug := updates[0].Slug
	page := wikiIngestProgressPage(slug)
	page.Content = "过滤前已保存的正文"
	page.SourceRefs = types.StringArray{"existing-source"}
	require.NoError(t, apprepo.NewWikiPageRepository(db).Create(context.Background(), page))
	clearMarker := markWikiSourceFilterDeletion(t, f.svc, updates[0].KnowledgeID)
	documents := make(map[string]bool)
	for _, update := range updates {
		documents[update.KnowledgeID] = true
	}
	err := f.reduce(slug)
	assert.ErrorContains(t, err, "wiki page progress sources changed before reduce")
	assert.Zero(t, f.model.calls.Load(), "来源缩水后不能继续模型归并")
	assertWikiSourceFilterReceipts(t, db, slug, len(documents), false)
	saved, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
	require.NoError(t, err)
	assert.Equal(t, page.Content, saved.Content)
	assert.Equal(t, page.SourceRefs, saved.SourceRefs)
	assert.Equal(t, page.Version, saved.Version)
	assert.ErrorIs(t, f.c.completeSlug(context.Background(), slug), types.ErrWikiProgressIncomplete)
	for id := range documents {
		assertEarlySettlementDocument(t, db, id, false)
	}
	return f, clearMarker
}

func TestWikiIngestProgressSourceFilterRejectsAllContributions(t *testing.T) {
	for _, kind := range []string{"entity", "summary"} {
		t.Run(kind, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				update := progressBoundaryEntityUpdate("deleted", "entity/filtered")
				if kind == "summary" {
					update = wikiIngestProgressUpdate("deleted", "summary/filtered")
				}
				assertWikiSourceFilterRejected(t, db, update)
			})
		})
	}
}

func TestWikiIngestProgressSourceFilterRejectsPartialContributions(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "entity/shared-filtered"
		f, clearMarker := assertWikiSourceFilterRejected(t, db,
			progressBoundaryEntityUpdate("deleted", slug), progressBoundaryEntityUpdate("healthy", slug))
		clearMarker()
		resumed := f.resume(t, db, 2)
		require.Len(t, resumed.pending, 1)
		require.ElementsMatch(t, f.pending[slug], resumed.pending[slug], "不能永久跳过被过滤或同页健康贡献")
		require.NoError(t, resumed.reduce(slug))
		assertWikiSourceFilterReceipts(t, db, slug, 2, true)
		require.NoError(t, resumed.c.completeSlug(context.Background(), slug))
		for _, id := range []string{"deleted", "healthy"} {
			assertEarlySettlementDocument(t, db, id, true)
		}
		page, err := resumed.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.Contains(t, page.SourceRefs, "deleted")
		require.Contains(t, page.SourceRefs, "healthy")
	})
}

// 同一文档对同页既有新增又有撤回时，只有一个 claim，但过滤仍可缩减它的贡献集合。
func TestWikiIngestProgressSourceFilterRejectsMixedUpdatesForOneClaim(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "entity/mixed-source"
		addition := progressBoundaryEntityUpdate("deleted", slug)
		retraction := addition
		retraction.Type, retraction.RetractDocContent = "retract", "应撤回的合成内容"
		assertWikiSourceFilterRejected(t, db, addition, retraction)
	})
}

// 重启只复用数据库；已成功兄弟页不能因失败贡献恢复而被重新写入或再次调用模型。
func TestWikiIngestProgressSourceFilterResumesWithoutReplayingSuccessfulPage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const stable, pendingSlug = "entity/stable", "entity/pending"
		f := newWikiSourceFilterFixture(t, db,
			progressBoundaryEntityUpdate("doc", stable), progressBoundaryEntityUpdate("doc", pendingSlug))
		require.NoError(t, f.reduce(stable))
		require.NoError(t, f.c.completeSlug(context.Background(), stable))
		stablePage, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, stable)
		require.NoError(t, err)
		clearMarker := markWikiSourceFilterDeletion(t, f.svc, "doc")
		require.ErrorContains(t, f.reduce(pendingSlug), "wiki page progress sources changed before reduce")
		require.Equal(t, int32(1), f.model.calls.Load())
		assertWikiSourceFilterReceipts(t, db, pendingSlug, 1, false)
		assertEarlySettlementDocument(t, db, "doc", false)
		clearMarker()
		resumed := f.resume(t, db, 1)
		svc := resumed.svc
		require.Equal(t, map[string][]SlugUpdate{
			pendingSlug: {progressBoundaryEntityUpdate("doc", pendingSlug)},
		}, resumed.pending)
		require.NoError(t, resumed.reduce(pendingSlug))
		require.Equal(t, int32(1), resumed.model.calls.Load())
		assertWikiSourceFilterReceipts(t, db, pendingSlug, 1, true)
		require.NoError(t, resumed.c.completeSlug(context.Background(), pendingSlug))
		// 文档结算会随队列行一起清理回执；先验证完成状态，再验证清理结果。
		assertWikiSourceFilterReceipts(t, db, pendingSlug, 0, false)
		assertEarlySettlementDocument(t, db, "doc", true)
		saved, err := svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, stable)
		require.NoError(t, err)
		require.Equal(t, stablePage.Version, saved.Version)
		require.Equal(t, stablePage.Content, saved.Content)
		require.Empty(t, svc.wikiService.(*wikiIngestProgressPages).updateCalls[stable])
	})
}

func TestWikiIngestProgressSourceFilterKeepsLiveContributions(t *testing.T) {
	for _, kind := range []string{"entity", "summary"} {
		t.Run(kind, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				update := progressBoundaryEntityUpdate("doc", "entity/live")
				if kind == "summary" {
					update = wikiIngestProgressUpdate("doc", "summary/live")
				}
				f := newWikiSourceFilterFixture(t, db, update)
				require.NoError(t, f.reduce(update.Slug))
				assertWikiSourceFilterReceipts(t, db, update.Slug, 1, true)
				require.NoError(t, f.c.completeSlug(context.Background(), update.Slug))
				assertEarlySettlementDocument(t, db, "doc", true)
				page, err := f.svc.wikiService.GetPageBySlug(
					context.Background(), wikiIngestProgressTestKB, update.Slug)
				require.NoError(t, err)
				require.Contains(t, page.SourceRefs, "doc")
			})
		})
	}
}

func TestWikiIngestProgressSourceFilterPreservesNonDurableFiltering(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const slug = "entity/non-durable"
				updates := []SlugUpdate{progressBoundaryEntityUpdate("deleted", slug)}
				if partial {
					updates = append(updates, progressBoundaryEntityUpdate("healthy", slug))
				}
				f := newWikiSourceFilterFixture(t, db, updates...)
				markWikiSourceFilterDeletion(t, f.svc, "deleted")
				changed, _, _, _, err := f.svc.reduceSlugUpdates(context.Background(), f.model,
					wikiIngestProgressTestKB, slug, updates, 7,
					f.svc.newWikiBatchContext(wikiIngestProgressTestKB, nil), nil)
				require.NoError(t, err)
				require.Equal(t, partial, changed)
				assertWikiSourceFilterReceipts(t, db, slug, len(updates), false)
				page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
				if partial {
					require.NoError(t, err)
					require.Equal(t, types.StringArray{"healthy"}, page.SourceRefs)
				} else {
					require.ErrorIs(t, err, apprepo.ErrWikiPageNotFound)
					require.Zero(t, f.model.calls.Load())
				}
			})
		})
	}
}

// 撤回不属于应过滤的新增贡献；缺页的合法 no-op 也必须仍可完成回执。
func TestWikiIngestProgressSourceFilterPreservesRetractions(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprintf("page-exists=%t", exists), func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const slug = "entity/retract"
				update := progressBoundaryEntityUpdate("deleted", slug)
				update.Type, update.RetractDocContent = "retract", "应撤回的合成内容"
				f := newWikiSourceFilterFixture(t, db, update)
				if exists {
					page := wikiIngestProgressPage(slug)
					page.PageType = types.WikiPageTypeEntity
					page.SourceRefs = types.StringArray{"deleted", "remaining"}
					require.NoError(t, apprepo.NewWikiPageRepository(db).Create(context.Background(), page))
				}
				markWikiSourceFilterDeletion(t, f.svc, "deleted")
				require.NoError(t, f.reduce(slug))
				assertWikiSourceFilterReceipts(t, db, slug, 1, true)
				page, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
				if exists {
					require.NoError(t, err)
					require.Equal(t, types.StringArray{"remaining"}, page.SourceRefs)
				} else {
					require.ErrorIs(t, err, apprepo.ErrWikiPageNotFound)
					require.Zero(t, f.model.calls.Load())
				}
			})
		})
	}
}
