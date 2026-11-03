package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// wikiPublicationForPage 仅用于夹具，把已提交页面写入的身份和版本固定为发布授权。
func wikiPublicationForPage(page *types.WikiPage) types.WikiPagePublication {
	return types.WikiPagePublication{
		TenantID: page.TenantID, KnowledgeBaseID: page.KnowledgeBaseID,
		PageID: page.ID, Slug: page.Slug, PageVersion: page.Version,
	}
}

// 带版本授权的发布也只能改状态和时间，不能覆盖来源、引用、正文或编辑者。
func TestWikiPagePublicationVersionOnlyChangesStatus(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/version-publish-fields")
		page.Status = types.WikiPageStatusDraft
		page.SourceRefs = types.StringArray{"first", "second"}
		page.ChunkRefs = types.StringArray{"first-chunk", "second-chunk"}
		page.InLinks = types.StringArray{"entity/inbound"}
		page.OutLinks = types.StringArray{"entity/outbound"}
		page.PageMetadata = types.JSON(`{"retained":true}`)
		page.Summary = "保留摘要"
		page.LastEditSource = "manual"
		page.LastEditorID = "retained-editor"
		page.UpdatedAt = time.Unix(1, 0).UTC()
		require.NoError(t, repo.Create(ctx, page))
		before, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		guarded := types.WithWikiPagePublication(ctx, wikiPublicationForPage(before))
		require.NoError(t, repo.PublishDraftPage(guarded, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
		after, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, after.Status)
		require.True(t, after.UpdatedAt.After(before.UpdatedAt))
		before.Status, before.UpdatedAt = after.Status, after.UpdatedAt
		before.ProgressSnapshot = after.ProgressSnapshot
		require.Equal(t, before, after)
	})
}

// 在发布 SQL 执行前写入新草稿，证明版本条件在数据库内生效，而非仅在读取时检查。
func TestWikiPagePublicationVersionPreservesInterleavedDraft(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/version-publish-interleaved")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		target := wikiPublicationForPage(page)
		var newer *types.WikiPage
		injected := false
		const callback = "test:wiki-version-publication-interleave"
		require.NoError(t, db.Callback().Update().Before("gorm:begin_transaction").
			Register(callback, func(tx *gorm.DB) {
				fields, ok := tx.Statement.Dest.(map[string]interface{})
				if !ok || tx.Statement.Table != "wiki_pages" ||
					fields["status"] != types.WikiPageStatusPublished || injected {
					return
				}
				injected = true
				// 在外层更新取得连接/开启事务前交错，SQLite 单连接夹具也不需要放宽锁或跳过事务。
				err := db.WithContext(ctx).Model(&types.WikiPage{}).Where("id = ?", page.ID).
					Updates(map[string]interface{}{
						"version": target.PageVersion + 1, "status": types.WikiPageStatusDraft,
						"content": "用户后来保存的新草稿", "updated_at": time.Now(),
					}).Error
				if err != nil {
					_ = tx.AddError(err)
					return
				}
				newer, err = repo.GetByID(ctx, page.ID)
				if err != nil {
					_ = tx.AddError(err)
				}
			}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })

		guarded := types.WithWikiPagePublication(ctx, target)
		require.NoError(t, repo.PublishDraftPage(guarded, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
		require.True(t, injected)
		require.NotNil(t, newer)
		stored, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, newer, stored, "旧回执不能发布 SQL 执行前刚保存的新草稿")

		// 合法的新版本有自己的授权，不能因为旧版本处理过就永久跳过同一 PageID。
		next := types.WithWikiPagePublication(ctx, wikiPublicationForPage(newer))
		require.NoError(t, repo.PublishDraftPage(next, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
		published, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusPublished, published.Status)
		require.Equal(t, newer.Version, published.Version)
		require.Equal(t, newer.Content, published.Content)
	})
}

// 上下文目标与调用身份不符或版本非法时，必须失败，不能退回旧的无版本发布。
func TestWikiPagePublicationVersionRejectsInvalidGuard(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/version-publish-invalid")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		before, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		cases := []struct {
			name string
			edit func(*types.WikiPagePublication)
		}{
			{"tenant", func(p *types.WikiPagePublication) { p.TenantID++ }},
			{"knowledge-base", func(p *types.WikiPagePublication) { p.KnowledgeBaseID = "other-kb" }},
			{"page", func(p *types.WikiPagePublication) { p.PageID = uuid.NewString() }},
			{"slug", func(p *types.WikiPagePublication) { p.Slug = "entity/another" }},
			{"zero-version", func(p *types.WikiPagePublication) { p.PageVersion = 0 }},
			{"negative-version", func(p *types.WikiPagePublication) { p.PageVersion = -1 }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				target := wikiPublicationForPage(before)
				tc.edit(&target)
				guarded := types.WithWikiPagePublication(ctx, target)
				require.ErrorContains(t,
					repo.PublishDraftPage(guarded, page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug),
					"invalid wiki page publication")
			})
		}
		after, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
}

// 即使上下文与参数一致，真实页面也必须属于指定租户、知识库、ID 和路径。
func TestWikiPagePublicationVersionKeepsScopeBoundaries(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/version-publish-scope")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		before, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		cases := []struct {
			name string
			edit func(*types.WikiPagePublication)
		}{
			{"tenant", func(p *types.WikiPagePublication) { p.TenantID++ }},
			{"knowledge-base", func(p *types.WikiPagePublication) { p.KnowledgeBaseID = "other-kb" }},
			{"page", func(p *types.WikiPagePublication) { p.PageID = uuid.NewString() }},
			{"slug", func(p *types.WikiPagePublication) { p.Slug = "entity/another" }},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				target := wikiPublicationForPage(before)
				tc.edit(&target)
				guarded := types.WithWikiPagePublication(ctx, target)
				require.ErrorIs(t, repo.PublishDraftPage(guarded,
					target.TenantID, target.KnowledgeBaseID, target.PageID, target.Slug), ErrWikiPageNotFound)
			})
		}
		after, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, before, after)
	})
}

// 已发布、归档以及重复消费同一授权都不应产生第二次状态写入。
func TestWikiPagePublicationVersionSkipsFinalStatuses(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		for _, status := range []string{types.WikiPageStatusPublished, types.WikiPageStatusArchived} {
			t.Run(status, func(t *testing.T) {
				page := wikiProgressPage("entity/version-publish-" + status)
				page.Status = status
				page.UpdatedAt = time.Unix(1, 0).UTC()
				require.NoError(t, repo.Create(ctx, page))
				before, err := repo.GetByID(ctx, page.ID)
				require.NoError(t, err)
				guarded := types.WithWikiPagePublication(ctx, wikiPublicationForPage(before))
				for range 2 {
					require.NoError(t, repo.PublishDraftPage(guarded,
						page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug))
				}
				after, err := repo.GetByID(ctx, page.ID)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	})
}

// 软删除和同路径重建不能借用旧页面回执；版本不匹配不能掩盖身份已不存在。
func TestWikiPagePublicationVersionRejectsDeletedAndReplacement(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/version-publish-replaced")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		guarded := types.WithWikiPagePublication(ctx, wikiPublicationForPage(page))
		require.NoError(t, repo.DeleteByID(ctx, page.ID))
		require.ErrorIs(t, repo.PublishDraftPage(guarded,
			page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug), ErrWikiPageNotFound)
		require.NoError(t, db.Unscoped().Where("id = ?", page.ID).Delete(&types.WikiPage{}).Error)
		replacement := wikiProgressPage(page.Slug)
		replacement.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, replacement))
		require.ErrorIs(t, repo.PublishDraftPage(guarded,
			page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug), ErrWikiPageNotFound)
		stored, err := repo.GetByID(ctx, replacement.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, stored.Status)
	})
}

// 版本授权不应把真正的取消和数据库错误当成“旧版本已跳过”吞掉。
func TestWikiPagePublicationVersionReturnsFailures(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/version-publish-failure")
		page.Status = types.WikiPageStatusDraft
		require.NoError(t, repo.Create(ctx, page))
		guarded := types.WithWikiPagePublication(ctx, wikiPublicationForPage(page))
		cancelled, cancel := context.WithCancel(guarded)
		cancel()
		require.ErrorIs(t, repo.PublishDraftPage(cancelled,
			page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug), context.Canceled)

		failure := errors.New("synthetic guarded publication failure")
		const callback = "test:wiki-version-publication-failure"
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			_ = tx.AddError(failure)
		}))
		t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
		require.ErrorIs(t, repo.PublishDraftPage(guarded,
			page.TenantID, page.KnowledgeBaseID, page.ID, page.Slug), failure)
		stored, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.WikiPageStatusDraft, stored.Status)
	})
}
