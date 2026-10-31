package repository

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 发布只改变状态与更新时间，不能把早先读取的来源、链接或目录覆盖回来。
func TestWikiPublishDraftPagePreservesStoredFields(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/publish-fields")
		page.Status = types.WikiPageStatusDraft
		page.SourceRefs = types.StringArray{"original-document", "committed-document"}
		page.ChunkRefs = types.StringArray{"original-chunk", "committed-chunk"}
		page.InLinks = types.StringArray{"entity/concurrent-source"}
		page.OutLinks = types.StringArray{"entity/concurrent-target"}
		page.Aliases = types.StringArray{"保留别名"}
		page.PageMetadata = types.JSON(`{"contribution":"committed"}`)
		page.ParentSlug = "entity/parent"
		page.FolderID = uuid.NewString()
		page.CategoryPath = types.StringArray{"目录", "子目录"}
		page.WikiPath = "目录/子目录/页面"
		page.Depth = 2
		page.SortOrder = 3
		page.Summary = "保留摘要"
		page.LastEditSource = "manual"
		page.LastEditorID = "retained-editor"
		page.UpdatedAt = time.Unix(1, 0).UTC()
		require.NoError(t, repo.Create(ctx, page))
		before, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.NoError(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
		after, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, after.Status)
		require.True(t, after.UpdatedAt.After(before.UpdatedAt))
		before.Status = after.Status
		before.UpdatedAt = after.UpdatedAt
		before.ProgressSnapshot = after.ProgressSnapshot
		require.Equal(t, before, after, "发布必须保留其余全部存储字段")
	})
}

func TestWikiPublishDraftPageChecksIdentity(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/publish-identity")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		before, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		cases := []struct {
			name     string
			tenantID uint64
			kbID     string
			pageID   string
			slug     string
		}{
			{"tenant", 2, page.KnowledgeBaseID, page.ID, page.Slug},
			{"knowledge-base", page.TenantID, "other-kb", page.ID, page.Slug},
			{"page", page.TenantID, page.KnowledgeBaseID, uuid.NewString(), page.Slug},
			{"slug", page.TenantID, page.KnowledgeBaseID, page.ID, "entity/renamed"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				require.ErrorIs(t, repo.PublishDraftPage(ctx, tc.tenantID, tc.kbID, tc.pageID, tc.slug),
					ErrWikiPageNotFound)
			})
		}
		after, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
}

// 重复发布及归档后的迟到发布均为空操作，更新时间也不能反复变化。
func TestWikiPublishDraftPageSkipsFinalStatuses(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		for _, status := range []string{types.WikiPageStatusPublished, types.WikiPageStatusArchived} {
			t.Run(status, func(t *testing.T) {
				page := wikiProgressPage("entity/publish-" + status)
				page.Status = status
				page.UpdatedAt = time.Unix(1, 0).UTC()
				require.NoError(t, repo.Create(ctx, page))
				before, err := repo.GetByID(ctx, page.ID)
				require.NoError(t, err)
				require.NoError(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
				after, err := repo.GetByID(ctx, page.ID)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
		page := wikiProgressPage("entity/publish-repeat")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		require.NoError(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
		published, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.NoError(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
		repeated, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, published, repeated)
	})
}

func TestWikiPublishDraftPageRejectsDeletedAndReplacement(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/publish-replaced")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		require.NoError(t, repo.DeleteByID(ctx, page.ID))
		require.ErrorIs(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug),
			ErrWikiPageNotFound)
		var deleted types.WikiPage
		require.NoError(t, db.Unscoped().Where("id = ?", page.ID).Take(&deleted).Error)
		require.True(t, deleted.DeletedAt.Valid)
		require.Equal(t, types.WikiPageStatusDraft, deleted.Status)
		// 清理旧行后，同路径出现新身份也不属于旧发布回执。
		require.NoError(t, db.Unscoped().Where("id = ?", page.ID).Delete(&types.WikiPage{}).Error)
		replacement := wikiProgressPage(page.Slug)
		replacement.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, replacement))
		require.ErrorIs(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug),
			ErrWikiPageNotFound)
		stored, err := repo.GetByID(ctx, replacement.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, stored.Status)
	})
}

func TestWikiPublishDraftPageReturnsCancellationAndDatabaseErrors(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/publish-errors")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		require.ErrorIs(t, repo.PublishDraftPage(cancelled, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug),
			context.Canceled)
		tx := db.Begin()
		require.NoError(t, tx.Error)
		require.NoError(t, tx.Rollback().Error)
		closed := NewWikiPageRepository(tx)
		require.ErrorIs(t, closed.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug),
			sql.ErrTxDone)
		stored, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, stored.Status)
		require.NoError(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))

		// 零行更新后的存在性读取失败必须保留错误，不能误报已幂等完成。
		failure := errors.New("publish identity lookup failed")
		callback := "wiki_publish_lookup_failure"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callback, func(query *gorm.DB) {
			if query.Statement.Table == "wiki_pages" {
				_ = query.AddError(failure)
			}
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callback)) })
		require.ErrorIs(t, repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug), failure)
	})
}

// 两个发布者无需读写旧快照；先发布的一方完成状态转换，后一方应幂等成功。
func TestWikiPublishDraftPageConcurrentPublishIsIdempotent(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/publish-concurrent")
		page.Status = types.WikiPageStatusDraft
		page.SourceRefs = types.StringArray{"committed"}
		require.NoError(t, repo.Create(ctx, page))
		start := make(chan struct{})
		results := make(chan error, 2)
		for range 2 {
			go func() {
				<-start
				results <- repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug)
			}()
		}
		close(start)
		for range 2 {
			require.NoError(t, <-results)
		}
		stored, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, stored.Status)
		require.Equal(t, 1, stored.Version)
		require.Equal(t, page.SourceRefs, stored.SourceRefs)
	})
}

// 发布先到时，旧草稿贡献应冲突且不写回执；重读后可续写，不能回退发布状态。
func TestWikiPublishDraftPageConcurrentContributionPreservesReceipt(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		for _, contentChanged := range []bool{false, true} {
			name := "metadata"
			if contentChanged {
				name = "content"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				claim := seedWikiProgressClaim(t, db, "publish-"+name)
				slug := "entity/publish-" + name
				saveWikiProgressPlan(t, db, claim, slug)
				repo := NewWikiPageRepository(db)
				page := wikiProgressPage(slug)
				page.Status = types.WikiPageStatusDraft
				page.SourceRefs = types.StringArray{"original-document"}
				page.ChunkRefs = types.StringArray{"original-chunk"}
				require.NoError(t, repo.Create(ctx, page))
				candidate, err := repo.GetByID(ctx, page.ID)
				require.NoError(t, err)
				commit := wikiProgressCommit(claim, slug)
				write := func(current *types.WikiPage) error {
					current.SourceRefs = append(current.SourceRefs, claim.KnowledgeID)
					current.ChunkRefs = append(current.ChunkRefs, "committed-chunk")
					writeCtx := types.WithWikiPageProgressCommit(ctx, &commit)
					if contentChanged {
						current.Content = "正文贡献已提交"
						return repo.UpdateWithRevision(writeCtx, current, nil)
					}
					return repo.UpdateMeta(writeCtx, current)
				}
				start := make(chan struct{})
				published := make(chan error, 1)
				written := make(chan error, 1)
				go func() {
					<-start
					published <- repo.PublishDraftPage(ctx, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug)
				}()
				go func() {
					<-start
					written <- write(candidate)
				}()
				close(start)
				require.NoError(t, <-published)
				writeErr := <-written
				if writeErr != nil {
					require.ErrorIs(t, writeErr, ErrWikiPageConflict)
					state, loadErr := wikiProgressRepo(t, db).LoadWikiIngestPlan(ctx, claim)
					require.NoError(t, loadErr)
					require.False(t, state.Pages[0].Applied)
					latest, readErr := repo.GetByID(ctx, page.ID)
					require.NoError(t, readErr)
					require.NoError(t, write(latest))
				}
				stored, err := repo.GetByID(ctx, page.ID)
				require.NoError(t, err)
				require.Equal(t, types.WikiPageStatusPublished, stored.Status)
				require.ElementsMatch(t, []string{"original-document", claim.KnowledgeID}, stored.SourceRefs)
				require.ElementsMatch(t, []string{"original-chunk", "committed-chunk"}, stored.ChunkRefs)
				wantVersion := 1
				if contentChanged {
					wantVersion = 2
					require.Equal(t, "正文贡献已提交", stored.Content)
				}
				require.Equal(t, wantVersion, stored.Version)
				state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(ctx, claim)
				require.NoError(t, err)
				require.True(t, state.Pages[0].Applied)
				require.Equal(t, stored.ID, state.Pages[0].PageID)
			})
		}
	})
}

// 反链恢复与发布并发时，各自只改负责的列，不能丢掉任一来源。
func TestWikiPublishDraftPageConcurrentBacklinksPreserveContributions(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		target := wikiProgressPage("entity/publish-backlink-target")
		target.Status = types.WikiPageStatusDraft
		target.SourceRefs = types.StringArray{"committed-document"}
		target.ChunkRefs = types.StringArray{"committed-chunk"}
		target.InLinks = types.StringArray{"entity/existing-source"}
		require.NoError(t, repo.Create(ctx, target))
		start := make(chan struct{})
		results := make(chan error, 3)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- repo.PublishDraftPage(ctx, target.TenantID, target.KnowledgeBaseID, target.ID, target.Slug)
		}()
		for _, slug := range []string{"entity/source-a", "entity/source-b"} {
			source := wikiProgressPage(slug)
			source.OutLinks = types.StringArray{target.Slug}
			require.NoError(t, repo.Create(ctx, source))
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results <- repo.ReconcileWikiPageProgressInLink(ctx, target.TenantID, target.KnowledgeBaseID,
					source.ID, source.Slug, target.ID, target.Slug)
			}()
		}
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		stored, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, stored.Status)
		require.ElementsMatch(t,
			[]string{"entity/existing-source", "entity/source-a", "entity/source-b"}, stored.InLinks)
		require.Equal(t, target.SourceRefs, stored.SourceRefs)
		require.Equal(t, target.ChunkRefs, stored.ChunkRefs)
		require.Equal(t, target.OutLinks, stored.OutLinks)
		require.Equal(t, 1, stored.Version)
	})
}
