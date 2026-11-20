package service

import (
	"context"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 关闭自动生成后，恢复入口只清理待处理进度，不再发布已经落库的草稿。
// 重复触发清理也不能再次扣减文档的剩余子任务数。
func TestWikiProgressManualModeDiscardsRecoveryWithoutPublishing(t *testing.T) {
	forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		fixture := newProgressPublishFixture(t, db, "doc-manual-recovery", "entity/manual-recovery")
		claim := fixture.batch.claims[fixture.ids[0]]
		require.NoError(t, fixture.service.progressRepo.ReleaseWikiIngestClaims(ctx, []types.WikiProgressClaim{claim}))
		repo := apprepo.NewWikiPageRepository(db)
		before, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, before.Status)

		resumed := newWikiIngestProgressService(t, db)
		disabled := false
		resumed.kbService.(*wikiLookupKBService).kb.WikiConfig.AutoGenerate = &disabled
		for _, attempt := range []string{"首次清理", "重复清理"} {
			require.NoError(t, runProgressPublishTask(ctx, resumed), attempt)
			assertProgressPublishSkippedReduce(t, resumed)
			after, err := repo.GetBySlug(ctx, wikiIngestProgressTestKB, fixture.page.Slug)
			require.NoError(t, err)
			require.Equal(t, before, after, "手动模式不能修改草稿、引用或更新时间")
			for _, model := range []interface{}{
				&types.TaskPendingOp{}, &types.WikiIngestPlan{}, &types.WikiIngestPageProgress{},
			} {
				var remaining int64
				require.NoError(t, db.Model(model).Count(&remaining).Error)
				require.Zero(t, remaining, "清理父任务应级联删除其计划和回执")
			}
			var pending int
			require.NoError(t, db.Table("knowledges").Select("pending_subtasks_count").
				Where("id = ?", "doc-manual-recovery").Scan(&pending).Error)
			require.Equal(t, 1, pending, "Wiki 只能释放一次自己的子任务计数")
		}
	})
}
