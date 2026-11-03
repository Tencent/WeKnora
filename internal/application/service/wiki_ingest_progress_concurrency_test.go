package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 两个文档先读同一页，再分别写入。成功回执不能掩盖另一份旧快照造成的贡献丢失。
// 同时覆盖顺序交错和真实并发；重启后只补未成功的文档，不重复执行已经完成的贡献。
func TestWikiIngestProgressSeparateClaimsPreserveContributionsAcrossResume(t *testing.T) {
	for _, mode := range []string{"metadata", "content"} {
		t.Run(mode, func(t *testing.T) {
			for _, schedule := range []string{"interleaved", "concurrent"} {
				t.Run(schedule, func(t *testing.T) {
					forWikiIngestProgressDBs(t, func(t *testing.T, db *gorm.DB) {
						ctx := context.Background()
						first := newWikiIngestProgressService(t, db)
						second := newWikiIngestProgressService(t, db)
						for _, id := range []string{"doc-first", "doc-second"} {
							enqueueWikiIngestProgressDoc(t, first, db, id)
						}
						ops, _, batch := claimWikiIngestProgress(t, first, 2)
						slug := "entity/shared-contributions"
						updates := []SlugUpdate{
							progressBoundaryEntityUpdate("doc-first", slug),
							progressBoundaryEntityUpdate("doc-second", slug),
						}
						prepareWikiIngestProgress(t, first, batch, ops, updates...)
						base := wikiIngestProgressPage(slug)
						base.PageType = types.WikiPageTypeEntity
						base.SourceRefs = types.StringArray{"original"}
						base.ChunkRefs = types.StringArray{"original-chunk"}
						_, err := first.wikiService.CreatePage(ctx, base)
						require.NoError(t, err)
						services := []*wikiIngestService{first, second}
						candidates := make([]*types.WikiPage, len(services))
						for i, svc := range services {
							candidates[i], err = svc.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
							require.NoError(t, err)
							candidates[i].SourceRefs = append(candidates[i].SourceRefs, updates[i].KnowledgeID)
							candidates[i].ChunkRefs = append(candidates[i].ChunkRefs, updates[i].KnowledgeID+"-chunk")
							if mode == "content" {
								candidates[i].Content += "\n" + updates[i].KnowledgeID
							}
						}
						results := make([]error, len(services))
						write := func(i int) {
							_, _, _, _, results[i] = batch.reduce(ctx, services[i], slug, updates[i:i+1],
								func(writeCtx context.Context, _ []SlugUpdate) (bool, string, bool, bool, error) {
									_, writeErr := services[i].wikiService.UpdatePage(writeCtx, candidates[i])
									return true, "ingest", false, false, writeErr
								})
						}
						if schedule == "concurrent" {
							var group sync.WaitGroup
							start := make(chan struct{})
							for i := range services {
								group.Add(1)
								go func(i int) {
									defer group.Done()
									<-start
									write(i)
								}(i)
							}
							close(start)
							group.Wait()
						} else {
							write(0)
							write(1)
						}
						winner, pendingID := "", ""
						for i, result := range results {
							if result == nil {
								require.Empty(t, winner, "两份旧快照不能同时完成")
								winner = updates[i].KnowledgeID
							} else {
								require.True(t, errors.Is(result, apprepo.ErrWikiPageConflict), "%v", result)
								require.Empty(t, pendingID)
								pendingID = updates[i].KnowledgeID
							}
						}
						require.NotEmpty(t, winner)
						require.NotEmpty(t, pendingID)
						for _, op := range ops {
							state := loadWikiIngestProgress(t, first, batch.claims[op.dbID])
							require.Equal(t, op.KnowledgeID == winner, state.Pages[0].Applied)
						}
						stored, err := first.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
						require.NoError(t, err)
						require.ElementsMatch(t, []string{"original", winner}, stored.SourceRefs)
						require.ElementsMatch(t, []string{"original-chunk", winner + "-chunk"}, stored.ChunkRefs)
						require.NoError(t, batch.release(ctx))

						resumed := newWikiIngestProgressService(t, db)
						again, ids, resumedBatch := claimWikiIngestProgress(t, resumed, 2)
						pending := prepareWikiIngestProgress(t, resumed, resumedBatch, again)
						require.Len(t, pending[slug], 1)
						require.Equal(t, pendingID, pending[slug][0].KnowledgeID)
						_, _, _, _, err = resumedBatch.reduce(ctx, resumed, slug, pending[slug],
							func(
								writeCtx context.Context, contributions []SlugUpdate,
							) (bool, string, bool, bool, error) {
								page, readErr := resumed.wikiService.GetPageBySlug(
									writeCtx, wikiIngestProgressTestKB, slug)
								if readErr != nil {
									return false, "", false, false, readErr
								}
								page.SourceRefs = append(page.SourceRefs, contributions[0].KnowledgeID)
								page.ChunkRefs = append(page.ChunkRefs, contributions[0].KnowledgeID+"-chunk")
								if mode == "content" {
									page.Content += "\n" + contributions[0].KnowledgeID
								}
								_, writeErr := resumed.wikiService.UpdatePage(writeCtx, page)
								return true, "ingest", false, false, writeErr
							})
						require.NoError(t, err)
						require.NoError(t, resumedBatch.settle(ctx, resumed, ids))
						stored, err = resumed.wikiService.GetPageBySlug(ctx, wikiIngestProgressTestKB, slug)
						require.NoError(t, err)
						require.ElementsMatch(t, []string{"original", "doc-first", "doc-second"}, stored.SourceRefs)
						require.ElementsMatch(t,
							[]string{"original-chunk", "doc-first-chunk", "doc-second-chunk"}, stored.ChunkRefs)
						if mode == "content" {
							require.Contains(t, stored.Content, "doc-first")
							require.Contains(t, stored.Content, "doc-second")
							require.Equal(t, 3, stored.Version)
						} else {
							require.Equal(t, base.Content, stored.Content)
							require.Equal(t, 1, stored.Version, "仅更新来源不递增可见内容版本")
						}
					})
				})
			}
		})
	}
}
