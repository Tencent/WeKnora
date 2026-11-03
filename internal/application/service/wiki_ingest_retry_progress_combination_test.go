package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// retryProgressModel 只替代模型输出；before 在读取完成后插入真实数据库写入，
// 不直接伪造冲突错误。计数和提示词用于区分失败页重算与已成功页被重复执行。
type retryProgressModel struct {
	mu      sync.Mutex
	calls   map[string]int
	prompts map[string][]string
	before  func(context.Context, string, int) error
	suffix  string
}

type retryProgressSlugKey struct{}

func (m *retryProgressModel) Chat(
	ctx context.Context, messages []chat.Message, _ *chat.ChatOptions,
) (*types.ChatResponse, error) {
	slug, _ := ctx.Value(retryProgressSlugKey{}).(string)
	if slug == "" {
		return nil, errors.New("组合夹具缺少当前页面标识")
	}
	var prompt strings.Builder
	for _, message := range messages {
		prompt.WriteString(message.Content)
	}
	m.mu.Lock()
	m.calls[slug]++
	attempt := m.calls[slug]
	m.prompts[slug] = append(m.prompts[slug], prompt.String())
	m.mu.Unlock()
	if m.before != nil {
		if err := m.before(ctx, slug, attempt); err != nil {
			return nil, err
		}
	}
	body := "SUMMARY: combination\n\nBASE " + slug + "\nNEW_CONTRIBUTION"
	// 输出只能保留本次提示词中的竞争内容，不能凭测试的数据库状态补齐。
	for index := 1; index <= 3; index++ {
		marker := fmt.Sprintf("CONCURRENT_EDIT_%d", index)
		if strings.Contains(prompt.String(), marker) {
			body += "\n" + marker
		}
	}
	return &types.ChatResponse{Content: body + m.suffix}, nil
}

func (*retryProgressModel) ChatStream(
	context.Context, []chat.Message, *chat.ChatOptions,
) (<-chan types.StreamResponse, error) {
	return nil, errors.New("组合夹具不使用流式模型")
}

func (*retryProgressModel) GetModelID() string   { return "retry-progress-synthetic" }
func (*retryProgressModel) GetModelName() string { return "retry-progress-synthetic" }

func (m *retryProgressModel) count(slug string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[slug]
}

// retryProgressFixture 冻结提取计划，但真实执行单页归并、双库事务、反链和结算。
// 不依赖单独重试 PR 的测试辅助函数，同批用例也能在原进度 PR 上作负向对照。
type retryProgressFixture struct {
	*wikiSettlementFixture
	db    *gorm.DB
	ops   []WikiPendingOp
	model *retryProgressModel
}

func newRetryProgressModel() *retryProgressModel {
	return &retryProgressModel{calls: make(map[string]int), prompts: make(map[string][]string)}
}

func newRetryProgressFixture(t *testing.T, db *gorm.DB, dependencies map[string][]string) *retryProgressFixture {
	t.Helper()
	ctx := context.Background()
	svc := newWikiIngestProgressService(t, db)
	grouped := make(map[string][]SlugUpdate)
	var docs []*docIngestResult
	ids := make([]string, 0, len(dependencies))
	for id := range dependencies {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		enqueueWikiIngestProgressDoc(t, svc, db, id)
		require.NoError(t, db.Table("knowledges").Where("id = ?", id).Update("pending_subtasks_count", 1).Error)
		doc := &docIngestResult{KnowledgeID: id, DocTitle: id, Summary: "冻结的合成摘要"}
		for _, slug := range dependencies[id] {
			grouped[slug] = append(grouped[slug], progressBoundaryEntityUpdate(id, slug))
			doc.Pages = append(doc.Pages, wikiIngestPageRef{Slug: slug, Title: slug})
		}
		docs = append(docs, doc)
	}
	for slug := range grouped {
		page := wikiIngestProgressPage(slug)
		page.PageType = types.WikiPageTypeEntity
		page.Content = "BASE " + slug
		require.NoError(t, apprepo.NewWikiPageRepository(db).Create(ctx, page))
	}
	ops, _, batch := claimWikiIngestProgress(t, svc, len(ids))
	pending, docs, _, err := batch.prepare(ctx, svc, ops, nil, docs, grouped, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	base := &wikiSettlementFixture{svc: svc, batch: batch, pending: pending, docs: docs}
	base.c = newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), docs, pending)
	batch.settlement = base.c
	return &retryProgressFixture{wikiSettlementFixture: base, db: db, ops: ops, model: newRetryProgressModel()}
}

func (f *retryProgressFixture) reduce(ctx context.Context, slug string) error {
	ctx = context.WithValue(ctx, retryProgressSlugKey{}, slug)
	_, _, _, _, err := f.batch.reduce(ctx, f.svc, slug, f.pending[slug],
		func(callCtx context.Context, updates []SlugUpdate) (bool, string, bool, bool, error) {
			return f.svc.reduceSlugUpdates(callCtx, f.model, wikiIngestProgressTestKB, slug, updates, 7,
				f.svc.newWikiBatchContext(wikiIngestProgressTestKB, nil), nil)
		})
	return err
}

// compete 用独立的普通页面服务提交另一作者的内容和来源；旧模型输出必须由产品拒绝。
func (f *retryProgressFixture) compete(slug string, attempt int) error {
	ctx := context.Background()
	pages := NewWikiPageService(apprepo.NewWikiPageRepository(f.db), nil, nil, nil, nil, nil)
	page, err := pages.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
	if err != nil {
		return err
	}
	page.Content += fmt.Sprintf("\nCONCURRENT_EDIT_%d", attempt)
	page.SourceRefs = append(page.SourceRefs, fmt.Sprintf("other-source-%d", attempt))
	page.ChunkRefs = append(page.ChunkRefs, fmt.Sprintf("other-chunk-%d", attempt))
	_, err = pages.UpdatePage(ctx, page)
	return err
}

func (f *retryProgressFixture) receipt(t *testing.T, docID, slug string) types.WikiIngestPageProgress {
	t.Helper()
	state := loadWikiIngestProgress(t, f.svc, f.batch.claims[f.id(docID)])
	for _, page := range state.Pages {
		if page.Slug == slug {
			return page
		}
	}
	t.Fatalf("缺少 %s / %s 的持久回执", docID, slug)
	return types.WikiIngestPageProgress{}
}

// resume 新建 service 和批次，只复用数据库，恢复时使用全新的模型计数器。
func (f *retryProgressFixture) resume(t *testing.T, count int) *retryProgressFixture {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, f.batch.release(ctx))
	svc := newWikiIngestProgressService(t, f.db)
	ops, _, batch := claimWikiIngestProgress(t, svc, count)
	pending, docs, _, err := batch.prepare(ctx, svc, ops, nil, nil, nil, nil,
		&WikiBatchContext{PlannedFolderID: make(map[string]string)})
	require.NoError(t, err)
	base := &wikiSettlementFixture{svc: svc, batch: batch, pending: pending, docs: docs}
	base.c = newWikiDocumentSettlement(batch, svc, wikiSettlementPayload(), docs, pending)
	batch.settlement = base.c
	return &retryProgressFixture{wikiSettlementFixture: base, db: f.db, ops: ops, model: newRetryProgressModel()}
}

func TestWikiRetryProgressCombinationConflictReloadsAndCommitsReceipt(t *testing.T) {
	for _, conflicts := range []int{1, 2} {
		t.Run(fmt.Sprintf("conflicts=%d", conflicts), func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				const stable, contended = "entity/stable", "entity/contended"
				f := newRetryProgressFixture(t, db, map[string][]string{"doc": {stable, contended}})
				f.model.before = func(_ context.Context, slug string, attempt int) error {
					if slug == contended && attempt <= conflicts {
						return f.compete(slug, attempt)
					}
					return nil
				}
				require.NoError(t, f.reduce(context.Background(), stable))
				require.NoError(t, f.c.completeSlug(context.Background(), stable))
				require.False(t, f.c.isSettled(f.id("doc")), "冲突页未完成时不能提前结算")
				require.NoError(t, f.reduce(context.Background(), contended))
				require.Equal(t, 1, f.model.count(stable))
				require.Equal(t, conflicts+1, f.model.count(contended))
				saved, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, contended)
				require.NoError(t, err)
				receipt := f.receipt(t, "doc", contended)
				require.True(t, receipt.Applied && receipt.EffectsDone)
				require.Equal(t, saved.ID, receipt.PageID)
				require.Equal(t, saved.Version, receipt.PageVersion)
				require.Contains(t, saved.SourceRefs, "doc")
				for attempt := 1; attempt <= conflicts; attempt++ {
					require.Contains(t, saved.Content, fmt.Sprintf("CONCURRENT_EDIT_%d", attempt))
					require.Contains(t, saved.SourceRefs, fmt.Sprintf("other-source-%d", attempt))
					require.Contains(t, saved.ChunkRefs, fmt.Sprintf("other-chunk-%d", attempt))
					require.Contains(t, f.model.prompts[contended][attempt], fmt.Sprintf("CONCURRENT_EDIT_%d", attempt))
				}
				require.NoError(t, f.c.completeSlug(context.Background(), contended))
				assertEarlySettlementDocument(t, db, "doc", true)
			})
		})
	}
}

func TestWikiRetryProgressCombinationExhaustionResumesOnlyPendingPage(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const stable, contended = "entity/stable", "entity/exhausted"
		f := newRetryProgressFixture(t, db, map[string][]string{"doc": {stable, contended}})
		f.model.before = func(_ context.Context, slug string, attempt int) error {
			if slug == contended {
				return f.compete(slug, attempt)
			}
			return nil
		}
		require.NoError(t, f.reduce(context.Background(), stable))
		require.NoError(t, f.c.completeSlug(context.Background(), stable))
		require.ErrorIs(t, f.reduce(context.Background(), contended), apprepo.ErrWikiPageConflict)
		require.Equal(t, 3, f.model.count(contended), "包含首次调用，总共只允许三次归并")
		require.False(t, f.receipt(t, "doc", contended).Applied)
		require.True(t, f.receipt(t, "doc", stable).Applied)
		require.ErrorIs(t, f.c.completeSlug(context.Background(), contended), types.ErrWikiProgressIncomplete)
		assertEarlySettlementDocument(t, db, "doc", false)
		require.NoError(t, f.batch.retry(context.Background(), f.svc, wikiSettlementPayload(), f.ops))
		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, f.id("doc")).Error)
		require.Equal(t, 1, row.FailCount)
		resumed := f.resume(t, 1)
		require.Len(t, resumed.pending, 1)
		require.Contains(t, resumed.pending, contended)
		require.NoError(t, resumed.reduce(context.Background(), contended))
		require.Equal(t, 1, resumed.model.count(contended))
		require.Zero(t, resumed.model.count(stable), "重启恢复不能重新执行成功兄弟页")
		require.NoError(t, resumed.c.completeSlug(context.Background(), contended))
		assertEarlySettlementDocument(t, db, "doc", true)
	})
}

func TestWikiRetryProgressCombinationCancellationAfterConflictCanResume(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "entity/canceled"
		f := newRetryProgressFixture(t, db, map[string][]string{"doc": {slug}})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		f.model.before = func(_ context.Context, page string, attempt int) error {
			if attempt == 1 {
				return f.compete(page, attempt)
			}
			cancel()
			return nil // 即使模型在取消后返回正文，也不允许落库。
		}
		require.ErrorIs(t, f.reduce(ctx, slug), context.Canceled)
		require.Equal(t, 2, f.model.count(slug))
		require.False(t, f.receipt(t, "doc", slug).Applied)
		saved, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.NotContains(t, saved.Content, "NEW_CONTRIBUTION")
		require.NotContains(t, saved.SourceRefs, "doc")
		resumed := f.resume(t, 1)
		require.NoError(t, resumed.reduce(context.Background(), slug))
		require.NoError(t, resumed.c.completeSlug(context.Background(), slug))
		assertEarlySettlementDocument(t, db, "doc", true)
	})
}

func TestWikiRetryProgressCombinationClaimReplacementRejectsOldAttempt(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "entity/reclaimed"
		f := newRetryProgressFixture(t, db, map[string][]string{"doc": {slug}})
		var resumed *retryProgressFixture
		f.model.before = func(_ context.Context, page string, attempt int) error {
			if attempt == 1 {
				return f.compete(page, attempt)
			}
			resumed = f.resume(t, 1)
			return nil
		}
		require.ErrorIs(t, f.reduce(context.Background(), slug), types.ErrWikiProgressClaimLost)
		require.Equal(t, 2, f.model.count(slug), "领取失效不属于可再次重试的页面冲突")
		require.NotNil(t, resumed)
		require.Greater(t, resumed.batch.claims[resumed.id("doc")].Epoch, f.batch.claims[f.id("doc")].Epoch)
		require.False(t, resumed.receipt(t, "doc", slug).Applied)
		assertEarlySettlementDocument(t, db, "doc", false)
		require.NoError(t, resumed.reduce(context.Background(), slug))
		require.NoError(t, resumed.c.completeSlug(context.Background(), slug))
		assertEarlySettlementDocument(t, db, "doc", true)
	})
}

func TestWikiRetryProgressCombinationDeletedSourceCannotCompleteReceipt(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "entity/deleted-source"
		f := newRetryProgressFixture(t, db, map[string][]string{"doc": {slug}})
		f.model.before = func(_ context.Context, page string, attempt int) error {
			if attempt == 1 {
				return f.compete(page, attempt)
			}
			return db.Table("knowledges").Where("id = ? AND tenant_id = ?", "doc", 7).
				Update("deleted_at", time.Now()).Error
		}
		require.Error(t, f.reduce(context.Background(), slug))
		require.Equal(t, 2, f.model.count(slug))
		// 直接查持久回执，避免来源软删除后 LoadWikiIngestPlan 的存活校验掩盖错误写入。
		var receipt types.WikiIngestPageProgress
		require.NoError(t, db.Where("pending_op_id = ? AND slug = ?", f.id("doc"), slug).Take(&receipt).Error)
		require.False(t, receipt.Applied)
		require.False(t, receipt.EffectsDone)
		saved, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.NotContains(t, saved.SourceRefs, "doc")
		require.NotContains(t, saved.Content, "NEW_CONTRIBUTION")
	})
}

func TestWikiRetryProgressCombinationBacklinkFailureResumesWithoutModel(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug, target = "entity/effects", "entity/target"
		f := newRetryProgressFixture(t, db, map[string][]string{"doc": {slug}})
		targetPage := wikiIngestProgressPage(target)
		require.NoError(t, apprepo.NewWikiPageRepository(db).Create(context.Background(), targetPage))
		f.model.suffix = "\n[[" + target + "]]"
		f.model.before = func(_ context.Context, page string, attempt int) error {
			if attempt == 1 {
				return f.compete(page, attempt)
			}
			return nil
		}
		failure := errors.New("合成反链维护失败")
		f.svc.wikiService.(*wikiIngestProgressPages).finishErr = failure
		require.ErrorIs(t, f.reduce(context.Background(), slug), failure)
		require.Equal(t, 2, f.model.count(slug))
		receipt := f.receipt(t, "doc", slug)
		require.True(t, receipt.Applied)
		require.False(t, receipt.EffectsDone)
		before, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		resumed := f.resume(t, 1)
		require.Empty(t, resumed.pending, "正文已提交，恢复仅补反链")
		require.Empty(t, resumed.model.calls)
		require.True(t, resumed.receipt(t, "doc", slug).EffectsDone)
		require.NoError(t, resumed.c.settleReady(context.Background()))
		after, err := resumed.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, slug)
		require.NoError(t, err)
		require.Equal(t, before.Content, after.Content)
		require.Equal(t, before.Version, after.Version)
		linked, err := resumed.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, target)
		require.NoError(t, err)
		require.Contains(t, linked.InLinks, slug)
		assertEarlySettlementDocument(t, db, "doc", true)
	})
}

func TestWikiRetryProgressCombinationPublicationFailureResumesWithoutModel(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const slug = "entity/publication"
		f := newRetryProgressFixture(t, db, map[string][]string{"doc": {slug}})
		f.model.before = func(_ context.Context, page string, attempt int) error {
			if attempt == 1 {
				return f.compete(page, attempt)
			}
			return nil
		}
		require.NoError(t, f.reduce(context.Background(), slug))
		require.Equal(t, 2, f.model.count(slug))
		failure := errors.New("合成发布失败")
		f.svc.wikiService = &sharedPublicationHook{
			WikiPageService: f.svc.wikiService,
			before:          func(context.Context, string) error { return failure },
		}
		require.ErrorIs(t, f.c.completeSlug(context.Background(), slug), failure)
		receipt := f.receipt(t, "doc", slug)
		require.True(t, receipt.Applied && receipt.EffectsDone)
		assertEarlySettlementDocument(t, db, "doc", false)
		resumed := f.resume(t, 1)
		require.Empty(t, resumed.pending)
		require.NoError(t, resumed.c.settleReady(context.Background()))
		require.Empty(t, resumed.model.calls)
		assertEarlySettlementDocument(t, db, "doc", true)
	})
}

func TestWikiRetryProgressCombinationSharedDraftSurvivesSiblingRetry(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		const shared, contended = "entity/shared", "entity/slow"
		f := newRetryProgressFixture(t, db, map[string][]string{"first": {shared}, "second": {shared, contended}})
		f.model.before = func(_ context.Context, slug string, attempt int) error {
			if slug == contended && attempt == 1 {
				return f.compete(slug, attempt)
			}
			return nil
		}
		require.NoError(t, f.reduce(context.Background(), shared))
		require.NoError(t, f.c.completeSlug(context.Background(), shared))
		require.True(t, f.c.isSettled(f.id("first")))
		require.False(t, f.c.isSettled(f.id("second")))
		draft, err := sharedPublicationEditDraft(context.Background(), db, shared)
		require.NoError(t, err)
		require.NoError(t, f.reduce(context.Background(), contended))
		require.Equal(t, 2, f.model.count(contended))
		// 慢文档仍未结算时先经过批尾发布，再完成其结算，两条路径均不能重发新草稿。
		require.NoError(t, f.batch.publishUnsettledPages(context.Background(), f.svc, []string{shared, contended}))
		require.NoError(t, f.c.completeSlug(context.Background(), contended))
		current, err := f.svc.wikiService.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, shared)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, current.Status)
		require.Equal(t, draft.Version, current.Version)
		require.Equal(t, draft.Content, current.Content)
		require.Contains(t, current.SourceRefs, "first")
		require.Contains(t, current.SourceRefs, "second")
		require.Equal(t, 1, f.model.count(shared))
		assertEarlySettlementDocument(t, db, "first", true)
		assertEarlySettlementDocument(t, db, "second", true)
	})
}
