package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// retryProgressTombstonePages 在真实快照冲突返回后写删除标记，固定下一次过滤的边界。
// 标记只在本用例的 miniredis 中，数据库中的文档和领取仍保持存活，暴露两者时序差。
type retryProgressTombstonePages struct {
	interfaces.WikiPageService
	afterConflict func() error
}

func (p *retryProgressTombstonePages) UpdatePage(
	ctx context.Context, page *types.WikiPage,
) (*types.WikiPage, error) {
	result, err := p.WikiPageService.UpdatePage(ctx, page)
	if errors.Is(err, apprepo.ErrWikiPageConflict) && p.afterConflict != nil {
		if hookErr := p.afterConflict(); hookErr != nil {
			return nil, hookErr
		}
	}
	return result, err
}

func (p *retryProgressTombstonePages) ReconcileAppliedPage(
	ctx context.Context, tenantID uint64, kbID string, receipt types.WikiIngestPageProgress,
) error {
	return p.WikiPageService.(interfaces.WikiPageProgressFinisher).
		ReconcileAppliedPage(ctx, tenantID, kbID, receipt)
}

// 删除标记可能早于数据库软删除，仓储的领取检查此时仍会通过。
// 无论首次过滤还是冲突后的过滤，都不能用缩小的贡献集合完成原来的全部回执。
func TestWikiRetryProgressCombinationTombstoneDoesNotCompleteFilteredClaims(t *testing.T) {
	for _, timing := range []string{"before-first-attempt", "after-real-conflict"} {
		t.Run(timing, func(t *testing.T) {
			for _, partial := range []bool{false, true} {
				t.Run(fmt.Sprintf("partial=%t", partial), func(t *testing.T) {
					forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
						const slug, deleted, healthy = "entity/filter-boundary", "deleted", "healthy"
						dependencies := map[string][]string{deleted: {slug}}
						if partial {
							dependencies[healthy] = []string{slug}
						}
						f := newRetryProgressFixture(t, db, dependencies)
						server := miniredis.RunT(t)
						client := redis.NewClient(&redis.Options{Addr: server.Addr()})
						t.Cleanup(func() { require.NoError(t, client.Close()) })
						f.svc.redisClient = client
						mark := func() error {
							return client.Set(context.Background(),
								WikiDeletedTombstoneKey(wikiIngestProgressTestKB, deleted), "1", 0).Err()
						}
						if timing == "before-first-attempt" {
							require.NoError(t, mark())
						} else {
							f.model.before = func(_ context.Context, page string, attempt int) error {
								if attempt == 1 {
									return f.compete(page, attempt)
								}
								return nil
							}
							f.svc.wikiService = &retryProgressTombstonePages{
								WikiPageService: f.svc.wikiService, afterConflict: mark,
							}
						}
						err := f.reduce(context.Background(), slug)
						assert.Error(t, err, "持久回执的贡献集变化必须保留失败，不能伪装成成功或 no-op")
						wantCalls := 0
						if timing == "after-real-conflict" {
							wantCalls = 1
						}
						assert.Equal(t, wantCalls, f.model.count(slug), "发现过滤变化后不能继续生成剩余贡献")

						var receipts []types.WikiIngestPageProgress
						require.NoError(t, db.Where("slug = ?", slug).Find(&receipts).Error)
						require.Len(t, receipts, len(dependencies))
						for _, receipt := range receipts {
							assert.False(t, receipt.Applied, "被过滤贡献和同页剩余贡献都不能误记完成")
							assert.False(t, receipt.EffectsDone)
							assert.Empty(t, receipt.PageID)
						}
						page, loadErr := f.svc.wikiService.GetPageBySlug(
							context.Background(), wikiIngestProgressTestKB, slug)
						require.NoError(t, loadErr)
						assert.NotContains(t, page.Content, "NEW_CONTRIBUTION")
						assert.NotContains(t, page.SourceRefs, deleted)
						assert.NotContains(t, page.SourceRefs, healthy)
						for id := range dependencies {
							assertEarlySettlementDocument(t, db, id, false)
						}

						// 撤销合成标记后，新的领取必须仍能拿到全部原贡献，不能永久跳过。
						require.NoError(t, client.Del(context.Background(),
							WikiDeletedTombstoneKey(wikiIngestProgressTestKB, deleted)).Err())
						resumed := f.resume(t, len(dependencies))
						require.Len(t, resumed.pending[slug], len(dependencies))
						require.NoError(t, resumed.reduce(context.Background(), slug))
						require.NoError(t, resumed.c.completeSlug(context.Background(), slug))
						require.Equal(t, 1, resumed.model.count(slug))
						for id := range dependencies {
							assertEarlySettlementDocument(t, db, id, true)
						}
					})
				})
			}
		})
	}
}
