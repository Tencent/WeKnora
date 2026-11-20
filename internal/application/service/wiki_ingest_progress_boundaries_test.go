package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 冻结 A 尚未写页时，新 B 也必须复用它的规范 slug，不能依赖 Redis 仍保留旧预约。
func TestWikiIngestProgressSeedsFrozenIdentityBeforeNewDocument(t *testing.T) {
	for _, mode := range []string{"lite", "redis-unavailable"} {
		t.Run(mode, func(t *testing.T) {
			frozen := SlugUpdate{
				KnowledgeID: "doc-a", Slug: "entity/beijing", Type: types.WikiPageTypeEntity,
				Item: extractedItem{Name: "北京", Slug: "entity/beijing"},
			}
			raw, err := json.Marshal(wikiDurablePlan{Version: wikiIngestPlanVersion, Updates: []SlugUpdate{frozen}})
			require.NoError(t, err)
			original := append([]byte(nil), raw...)
			batch := &wikiProgressBatch{states: map[int64]*types.WikiIngestPlanState{
				1: {Plan: &types.WikiIngestPlan{PendingOpID: 1, Payload: raw}},
			}}
			service := &wikiIngestService{}
			if mode == "redis-unavailable" {
				client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:0"})
				require.NoError(t, client.Close())
				require.Error(t, client.Ping(context.Background()).Err(), "使用已关闭客户端，测试不连接外部 Redis")
				service.redisClient = client
			}
			batchContext := &WikiBatchContext{}
			require.NoError(t, batch.seedIdentities(batchContext))
			chosen := service.claimWikiIdentitySlug(context.Background(), wikiIngestProgressTestKB,
				types.WikiPageTypeEntity, "北京", "entity/bei-jing", false, batchContext)
			require.Equal(t, frozen.Slug, chosen)
			fresh := SlugUpdate{
				KnowledgeID: "doc-b", Slug: "entity/bei-jing", Type: types.WikiPageTypeEntity,
				Item: extractedItem{Name: "北京", Slug: "entity/bei-jing"},
			}
			remapped := service.remapSlugUpdatesByIdentity(context.Background(), wikiIngestProgressTestKB,
				map[string][]SlugUpdate{fresh.Slug: {fresh}}, batchContext)
			require.Len(t, remapped, 1)
			require.Equal(t, frozen.Slug, remapped[frozen.Slug][0].Slug)
			require.Equal(t, "doc-b", remapped[frozen.Slug][0].KnowledgeID)
			require.Equal(t, original, []byte(batch.states[1].Plan.Payload), "只能约束新任务，不能改写冻结计划")
		})
	}
}

// progressPublishSwapPages 只在读取后注入同 slug 替换，写入仍走真实 service 和仓储。
type progressPublishSwapPages struct {
	interfaces.WikiPageService
	afterRead func()
}

func (p *progressPublishSwapPages) GetPageBySlug(ctx context.Context, kbID, slug string) (*types.WikiPage, error) {
	page, err := p.WikiPageService.GetPageBySlug(ctx, kbID, slug)
	if err == nil && p.afterRead != nil {
		hook := p.afterRead
		p.afterRead = nil
		hook()
	}
	return page, err
}

func TestWikiIngestProgressPublishDoesNotPublishReplacedDraft(t *testing.T) {
	for _, timing := range []string{"before-read", "after-read"} {
		t.Run(timing, func(t *testing.T) {
			forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
				service := newWikiIngestProgressService(t, db)
				pages := service.wikiService
				old, err := pages.CreatePage(context.Background(), wikiIngestProgressPage("entity/replaced"))
				require.NoError(t, err)
				replacement := wikiIngestProgressPage(old.Slug)
				replacement.Content = "新一代仍在处理的正文"
				replace := func() {
					require.NoError(t, db.Unscoped().Where("id = ?", old.ID).Delete(&types.WikiPage{}).Error)
					_, err := pages.CreatePage(context.Background(), replacement)
					require.NoError(t, err)
				}
				if timing == "before-read" {
					replace()
				} else {
					service.wikiService = &progressPublishSwapPages{WikiPageService: pages, afterRead: replace}
				}
				err = service.publishDraftPages(context.Background(), wikiIngestProgressTestKB,
					[]string{old.Slug}, map[string]string{old.Slug: old.ID})
				if timing == "after-read" {
					require.ErrorIs(t, err, apprepo.ErrWikiPageNotFound,
						"旧 ID 消失应保留可重试错误，不能按 slug 发布替代页")
				} else {
					require.NoError(t, err)
				}
				current, err := pages.GetPageBySlug(context.Background(), wikiIngestProgressTestKB, old.Slug)
				require.NoError(t, err)
				require.Equal(t, replacement.ID, current.ID)
				require.Equal(t, types.WikiPageStatusDraft, current.Status)
				require.Equal(t, replacement.Content, current.Content)
			})
		})
	}
}

func TestWikiIngestProgressPublishStillPublishesMatchingDraft(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		service := newWikiIngestProgressService(t, db)
		page, err := service.wikiService.CreatePage(context.Background(), wikiIngestProgressPage("entity/expected"))
		require.NoError(t, err)
		require.NoError(t, service.publishDraftPages(context.Background(), wikiIngestProgressTestKB,
			[]string{page.Slug}, map[string]string{page.Slug: page.ID}))
		current, err := service.wikiService.GetPageBySlug(
			context.Background(), wikiIngestProgressTestKB, page.Slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, current.Status)
		require.Equal(t, page.Content, current.Content)
		require.Equal(t, page.Version, current.Version)
	})
}

// progressBoundaryModel 替身只控制本次回答，不连接模型供应商。
type progressBoundaryModel struct {
	templateCaptureChatModel
	err   error
	calls atomic.Int32
}

func (m *progressBoundaryModel) Chat(
	ctx context.Context, messages []chat.Message, opts *chat.ChatOptions,
) (*types.ChatResponse, error) {
	m.calls.Add(1)
	if m.err != nil {
		return nil, m.err
	}
	return m.templateCaptureChatModel.Chat(ctx, messages, opts)
}

type progressBoundaryModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s progressBoundaryModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

func progressBoundaryEntityUpdate(knowledgeID, slug string) SlugUpdate {
	return SlugUpdate{
		KnowledgeID: knowledgeID, Slug: slug, Type: types.WikiPageTypeEntity,
		DocTitle: "合成文档", SourceRef: knowledgeID,
		Item: extractedItem{Name: "合成实体", Slug: slug, Description: "回归测试内容"},
	}
}

// 撤回在内存中会先改 SourceRefs；失败后必须连同正文一起保持原值，且不能完成回执。
func TestWikiIngestProgressModelFailureDoesNotCompleteOrPersistPartialState(t *testing.T) {
	for _, operation := range []string{"addition", "retraction"} {
		t.Run(operation, func(t *testing.T) {
			for _, answer := range []string{"error", "empty", "whitespace"} {
				t.Run(answer, func(t *testing.T) {
					forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
						service := newWikiIngestProgressService(t, db)
						enqueueWikiIngestProgressDoc(t, service, db, "doc")
						ops, _, batch := claimWikiIngestProgress(t, service, 1)
						page := wikiIngestProgressPage("entity/retry")
						page.Content = "失败之前已经保存的完整正文"
						page.SourceRefs = types.StringArray{"doc", "other"}
						stored, err := service.wikiService.CreatePage(context.Background(), page)
						require.NoError(t, err)
						update := progressBoundaryEntityUpdate("doc", page.Slug)
						if operation == "retraction" {
							update.Type, update.RetractDocContent = "retract", "待撤回的合成内容"
						}
						pending := prepareWikiIngestProgress(t, service, batch, ops, update)
						model := &progressBoundaryModel{}
						if answer == "error" {
							model.err = errors.New("synthetic invalid model response")
						}
						if answer == "whitespace" {
							model.response = " \n\t "
						}
						batchContext := &WikiBatchContext{
							SlugTitleMany: func(context.Context, []string) map[string]string {
								return nil
							},
							SummaryContentByKnowledgeID: func(context.Context, string) string { return "" },
						}
						changed, _, additionFailed, deferred, err := batch.reduce(
							context.Background(), service, page.Slug, pending[page.Slug],
							func(ctx context.Context, updates []SlugUpdate) (bool, string, bool, bool, error) {
								return service.reduceSlugUpdates(ctx, model, wikiIngestProgressTestKB,
									page.Slug, updates, 7, batchContext, nil)
							})
						require.Error(t, err)
						if model.err != nil {
							require.ErrorIs(t, err, model.err)
						}
						require.False(t, changed)
						require.False(t, deferred)
						require.Equal(t, operation == "addition", additionFailed)
						require.Equal(t, int32(1), model.calls.Load())
						current, err := service.wikiService.GetPageBySlug(
							context.Background(), wikiIngestProgressTestKB, page.Slug)
						require.NoError(t, err)
						require.Equal(t, stored.Content, current.Content)
						require.Equal(t, stored.SourceRefs, current.SourceRefs)
						require.Equal(t, stored.Version, current.Version)
						state := loadWikiIngestProgress(t, service, batch.claims[ops[0].dbID])
						require.Len(t, state.Pages, 1)
						require.False(t, state.Pages[0].Applied)
						require.False(t, state.Pages[0].EffectsDone)
					})
				})
			}
		})
	}
}

// 这里验证编排的限流分类，不测试供应商 SDK 重试；短错误避免引入 2/4/8 秒模型等待。
func TestProcessWikiIngestProgressRateLimitKeepsPendingAndSchedulesBackoff(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		seed := newWikiIngestProgressService(t, db)
		enqueueWikiIngestProgressDoc(t, seed, db, "doc")
		ops, ids, batch := claimWikiIngestProgress(t, seed, 1)
		update := progressBoundaryEntityUpdate("doc", "entity/rate-limited")
		prepareWikiIngestProgress(t, seed, batch, ops, update)
		require.NoError(t, batch.release(context.Background()))

		runner := newWikiIngestProgressService(t, db)
		model := &progressBoundaryModel{err: errors.New("HTTP 429 Too Many Requests")}
		runner.modelService = progressBoundaryModelService{model: model}
		queued, inspector := newQueuedFollowUpTestService(t)
		runner.task = queued.task
		raw, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: wikiIngestProgressTestKB})
		require.NoError(t, err)
		started := time.Now()
		require.NoError(t, runner.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, raw)))
		finished := time.Now()
		require.Equal(t, int32(1), model.calls.Load())

		var row types.TaskPendingOp
		require.NoError(t, db.First(&row, ids[0]).Error)
		require.Equal(t, 1, row.FailCount)
		require.Nil(t, row.ClaimedAt)
		var receipt types.WikiIngestPageProgress
		require.NoError(t, db.Where("pending_op_id = ? AND slug = ?", ids[0], update.Slug).First(&receipt).Error)
		require.False(t, receipt.Applied)
		require.False(t, receipt.EffectsDone)
		var knowledge types.Knowledge
		require.NoError(t, db.First(&knowledge, "id = ?", "doc").Error)
		require.Equal(t, 2, knowledge.PendingSubtasksCount)

		scheduled, err := inspector.ListScheduledTasks(types.QueueWiki)
		require.NoError(t, err)
		require.Len(t, scheduled, 1, "失败页只安排一次后续任务")
		require.Equal(t, types.TypeWikiIngest, scheduled[0].Type)
		require.False(t, scheduled[0].NextProcessAt.Before(started.Add(wikiRateLimitBackoff-time.Second)))
		require.False(t, scheduled[0].NextProcessAt.After(finished.Add(wikiRateLimitBackoff+time.Second)))
	})
}
