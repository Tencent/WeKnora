package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 同页贡献必须基于读取时的完整快照；元数据更新不增加用户可见版本。
func TestWikiProgressMetadataConflictPreservesAppliedContribution(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		first := seedWikiProgressClaim(t, db, "snapshot-first")
		second := seedWikiProgressClaim(t, db, "snapshot-second")
		slug := "entity/snapshot-shared"
		saveWikiProgressPlan(t, db, first, slug)
		saveWikiProgressPlan(t, db, second, slug)
		pages := NewWikiPageRepository(db)
		base := wikiProgressPage(slug)
		base.SourceRefs = types.StringArray{"original"}
		require.NoError(t, pages.Create(ctx, base))
		a, err := pages.GetBySlug(ctx, "kb", slug)
		require.NoError(t, err)
		b, err := pages.GetByID(ctx, base.ID)
		require.NoError(t, err)
		require.NotEmpty(t, a.ProgressSnapshot)
		require.Equal(t, a.ProgressSnapshot, b.ProgressSnapshot)
		a.SourceRefs = append(a.SourceRefs, first.KnowledgeID)
		b.SourceRefs = append(b.SourceRefs, second.KnowledgeID)
		firstCommit := wikiProgressCommit(first, slug)
		secondCommit := wikiProgressCommit(second, slug)
		require.NoError(t, pages.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &firstCommit), a))
		require.ErrorIs(t, pages.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &secondCommit), b),
			ErrWikiPageConflict)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(ctx, second)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied)
		stored, err := pages.GetBySlug(ctx, "kb", slug)
		require.NoError(t, err)
		require.Equal(t, 1, stored.Version)
		require.Equal(t, types.StringArray{"original", first.KnowledgeID}, stored.SourceRefs)
		// 重新读取并合并后，只有第二份贡献会提交；先前成功的贡献继续保留。
		stored.SourceRefs = append(stored.SourceRefs, second.KnowledgeID)
		require.NoError(t, pages.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &secondCommit), stored))
		final, err := pages.GetByID(ctx, base.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"original", first.KnowledgeID, second.KnowledgeID}, final.SourceRefs)
		require.Equal(t, 1, final.Version)
		require.Equal(t, final.ProgressSnapshot, stored.ProgressSnapshot)
		require.ErrorIs(t, pages.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &firstCommit), a),
			types.ErrWikiProgressAlreadyApplied)
	})
}

// 正文写也不能绕过同版本元数据冲突，且冲突不得留下历史版本或完成回执。
func TestWikiProgressRevisionRejectsChangedMetadataWithoutVersionBump(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		claim := seedWikiProgressClaim(t, db, "revision-stale")
		slug := "entity/revision-stale"
		saveWikiProgressPlan(t, db, claim, slug)
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage(slug)
		require.NoError(t, repo.Create(ctx, page))
		originalSnapshot := page.ProgressSnapshot
		other, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		other.SourceRefs = types.StringArray{"concurrent"}
		require.NoError(t, repo.UpdateMeta(ctx, other))
		page.Content = "changed body"
		commit := wikiProgressCommit(claim, slug)
		revision := &types.WikiPageRevision{ID: uuid.NewString(), PageID: page.ID, Version: 1}
		require.ErrorIs(t, repo.UpdateWithRevision(types.WithWikiPageProgressCommit(ctx, &commit), page, revision),
			ErrWikiPageConflict)
		require.Equal(t, 1, page.Version)
		require.Equal(t, originalSnapshot, page.ProgressSnapshot)
		var count int64
		require.NoError(t, db.Model(&types.WikiPageRevision{}).Count(&count).Error)
		require.Zero(t, count)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(ctx, claim)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied)
	})
}

func TestWikiProgressUpdateRequiresOriginalSnapshot(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		claim := seedWikiProgressClaim(t, db, "missing-snapshot")
		slug := "entity/missing-snapshot"
		saveWikiProgressPlan(t, db, claim, slug)
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage(slug)
		require.NoError(t, repo.Create(ctx, page))
		page.ProgressSnapshot = ""
		commit := wikiProgressCommit(claim, slug)
		require.ErrorIs(t, repo.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &commit), page), ErrWikiPageConflict)
		state, err := wikiProgressRepo(t, db).LoadWikiIngestPlan(ctx, claim)
		require.NoError(t, err)
		require.False(t, state.Pages[0].Applied)
	})
}

func TestWikiProgressMetadataWriteKeepsLatestInboundLinks(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		claim := seedWikiProgressClaim(t, db, "keep-backlinks")
		slug := "entity/keep-backlinks"
		saveWikiProgressPlan(t, db, claim, slug)
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage(slug)
		require.NoError(t, repo.Create(ctx, page))
		require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).
			Update("in_links", types.StringArray{"entity/arrived"}).Error)
		page.SourceRefs = types.StringArray{claim.KnowledgeID}
		commit := wikiProgressCommit(claim, slug)
		require.NoError(t, repo.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &commit), page))
		stored, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"entity/arrived"}, stored.InLinks)
		require.Equal(t, stored.InLinks, page.InLinks)
	})
}

// 快照不对外序列化，不依赖时间精度或 JSON key 顺序，也不丢失大整数差异。
func TestWikiProgressSnapshotCanonicalFields(t *testing.T) {
	page := wikiProgressPage("entity/canonical")
	page.PageMetadata = types.JSON(`{"n":9007199254740992,"nested":{"b":2,"a":1}}`)
	first, err := types.WikiPageProgressSnapshot(page)
	require.NoError(t, err)
	other := *page
	other.InLinks = types.StringArray{"unrelated"}
	other.UpdatedAt = time.Now()
	other.CreatedAt = time.Now()
	other.ProgressSnapshot = "internal-token"
	other.SourceRefs = types.StringArray{}
	other.PageMetadata = types.JSON(` { "nested": {"a":1,"b":2}, "n":9007199254740992 } `)
	equivalent, err := types.WikiPageProgressSnapshot(&other)
	require.NoError(t, err)
	require.Equal(t, first, equivalent)
	other.PageMetadata = types.JSON(`{"n":9007199254740993,"nested":{"b":2,"a":1}}`)
	changed, err := types.WikiPageProgressSnapshot(&other)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
	body, err := json.Marshal(other)
	require.NoError(t, err)
	require.NotContains(t, string(body), "internal-token")
	other.PageMetadata = types.JSON(`{"broken"`)
	_, err = types.WikiPageProgressSnapshot(&other)
	require.Error(t, err)
}

// 收尾仅按当前源页链接更新目标反链，不能把旧目标快照的其他列写回。
func TestWikiProgressInLinkReconcilePreservesTargetMetadata(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		source := wikiProgressPage("entity/source")
		target := wikiProgressPage("entity/target")
		source.OutLinks = types.StringArray{target.Slug}
		target.InLinks = types.StringArray{"entity/other"}
		target.SourceRefs = types.StringArray{"old"}
		require.NoError(t, repo.Create(ctx, source))
		require.NoError(t, repo.Create(ctx, target))
		require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", target.ID).Updates(map[string]interface{}{
			"source_refs": types.StringArray{"old", "new"}, "out_links": types.StringArray{"entity/later"},
		}).Error)
		run := func() error {
			return repo.ReconcileWikiPageProgressInLink(ctx, 1, "kb", source.ID, source.Slug, target.ID, target.Slug)
		}
		require.NoError(t, run())
		stored, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"old", "new"}, stored.SourceRefs)
		require.Equal(t, types.StringArray{"entity/later"}, stored.OutLinks)
		require.Equal(t, types.StringArray{"entity/other", source.Slug}, stored.InLinks)
		require.NoError(t, run())
		repeated, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.Equal(t, stored.UpdatedAt, repeated.UpdatedAt)
		require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", source.ID).
			Update("out_links", types.StringArray{}).Error)
		require.NoError(t, run())
		removed, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.Equal(t, types.StringArray{"entity/other"}, removed.InLinks)
		require.Equal(t, stored.SourceRefs, removed.SourceRefs)
		require.Equal(t, stored.OutLinks, removed.OutLinks)
	})
}

func TestWikiProgressInLinkReconcileChecksIdentitiesAndCancellation(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		source := wikiProgressPage("entity/source")
		target := wikiProgressPage("entity/target")
		source.OutLinks = types.StringArray{target.Slug}
		require.NoError(t, repo.Create(ctx, source))
		require.NoError(t, repo.Create(ctx, target))
		require.ErrorIs(t, repo.ReconcileWikiPageProgressInLink(ctx, 2, "kb",
			source.ID, source.Slug, target.ID, target.Slug), ErrWikiPageNotFound)
		require.ErrorIs(t, repo.ReconcileWikiPageProgressInLink(ctx, 1, "other-kb",
			source.ID, source.Slug, target.ID, target.Slug), ErrWikiPageNotFound)
		require.ErrorIs(t, repo.ReconcileWikiPageProgressInLink(ctx, 1, "kb",
			source.ID, "entity/renamed", target.ID, target.Slug), ErrWikiPageNotFound)
		require.ErrorIs(t, repo.ReconcileWikiPageProgressInLink(ctx, 1, "kb",
			source.ID, source.Slug, uuid.NewString(), target.Slug), ErrWikiPageNotFound)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		require.ErrorIs(t, repo.ReconcileWikiPageProgressInLink(cancelled, 1, "kb",
			source.ID, source.Slug, target.ID, target.Slug), context.Canceled)
		require.NoError(t, repo.DeleteByID(ctx, source.ID))
		require.ErrorIs(t, repo.ReconcileWikiPageProgressInLink(ctx, 1, "kb",
			source.ID, source.Slug, target.ID, target.Slug), ErrWikiPageNotFound)
		stored, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.Empty(t, stored.InLinks)
	})
}

func TestWikiProgressConcurrentInboundReconcileKeepsBothSources(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		a := wikiProgressPage("entity/a")
		b := wikiProgressPage("entity/b")
		target := wikiProgressPage("entity/target")
		a.OutLinks = types.StringArray{target.Slug}
		b.OutLinks = types.StringArray{target.Slug}
		require.NoError(t, repo.Create(ctx, a))
		require.NoError(t, repo.Create(ctx, b))
		require.NoError(t, repo.Create(ctx, target))
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, source := range []*types.WikiPage{a, b} {
			wg.Add(1)
			go func(source *types.WikiPage) {
				defer wg.Done()
				results <- repo.ReconcileWikiPageProgressInLink(ctx, 1, "kb",
					source.ID, source.Slug, target.ID, target.Slug)
			}(source)
		}
		wg.Wait()
		close(results)
		for err := range results {
			require.NoError(t, err)
		}
		stored, err := repo.GetByID(ctx, target.ID)
		require.NoError(t, err)
		require.ElementsMatch(t, []string{a.Slug, b.Slug}, stored.InLinks)
	})
}

func TestWikiProgressReceiptFailureRestoresSnapshotAndInLinks(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		claim := seedWikiProgressClaim(t, db, "snapshot-rollback")
		slug := "entity/snapshot-rollback"
		saveWikiProgressPlan(t, db, claim, slug)
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage(slug)
		require.NoError(t, repo.Create(ctx, page))
		snapshot := page.ProgressSnapshot
		oldInLinks := page.InLinks
		require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).
			Update("in_links", types.StringArray{"entity/concurrent"}).Error)
		failure := errors.New("injected receipt failure")
		callback := "wiki_snapshot_receipt_failure"
		require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "wiki_ingest_page_progress" {
				_ = tx.AddError(failure)
			}
		}))
		defer func() { require.NoError(t, db.Callback().Update().Remove(callback)) }()
		page.SourceRefs = types.StringArray{claim.KnowledgeID}
		commit := wikiProgressCommit(claim, slug)
		require.ErrorIs(t, repo.UpdateMeta(types.WithWikiPageProgressCommit(ctx, &commit), page), failure)
		require.Equal(t, snapshot, page.ProgressSnapshot)
		require.Equal(t, oldInLinks, page.InLinks)
		require.Equal(t, 1, page.Version)
		stored, err := repo.GetByID(ctx, page.ID)
		require.NoError(t, err)
		require.Empty(t, stored.SourceRefs)
		require.Equal(t, types.StringArray{"entity/concurrent"}, stored.InLinks)
	})
}

// 每个可被主页面写覆盖的字段都要参与快照；同版本正文装饰也不能漏掉。
func TestWikiProgressSnapshotCoversPageWriteFields(t *testing.T) {
	base := wikiProgressPage("entity/field-coverage")
	before, err := types.WikiPageProgressSnapshot(base)
	require.NoError(t, err)
	changes := map[string]func(*types.WikiPage){
		"id":          func(p *types.WikiPage) { p.ID = "new-id" },
		"tenant":      func(p *types.WikiPage) { p.TenantID++ },
		"kb":          func(p *types.WikiPage) { p.KnowledgeBaseID = "other" },
		"slug":        func(p *types.WikiPage) { p.Slug = "other" },
		"version":     func(p *types.WikiPage) { p.Version++ },
		"title":       func(p *types.WikiPage) { p.Title = "new" },
		"content":     func(p *types.WikiPage) { p.Content = "new" },
		"summary":     func(p *types.WikiPage) { p.Summary = "new" },
		"type":        func(p *types.WikiPage) { p.PageType = types.WikiPageTypeSummary },
		"status":      func(p *types.WikiPage) { p.Status = types.WikiPageStatusArchived },
		"aliases":     func(p *types.WikiPage) { p.Aliases = types.StringArray{"new"} },
		"source_refs": func(p *types.WikiPage) { p.SourceRefs = types.StringArray{"new"} },
		"chunk_refs":  func(p *types.WikiPage) { p.ChunkRefs = types.StringArray{"new"} },
		"out_links":   func(p *types.WikiPage) { p.OutLinks = types.StringArray{"new"} },
		"metadata":    func(p *types.WikiPage) { p.PageMetadata = types.JSON(`{"new":true}`) },
		"parent":      func(p *types.WikiPage) { p.ParentSlug = "new" },
		"folder":      func(p *types.WikiPage) { p.FolderID = "new" },
		"category":    func(p *types.WikiPage) { p.CategoryPath = types.StringArray{"new"} },
		"path":        func(p *types.WikiPage) { p.WikiPath = "new" },
		"depth":       func(p *types.WikiPage) { p.Depth++ },
		"order":       func(p *types.WikiPage) { p.SortOrder++ },
		"edit_source": func(p *types.WikiPage) { p.LastEditSource = types.WikiEditSourceUser },
		"editor":      func(p *types.WikiPage) { p.LastEditorID = "new" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			candidate := *base
			change(&candidate)
			after, err := types.WikiPageProgressSnapshot(&candidate)
			require.NoError(t, err)
			require.NotEqual(t, before, after)
		})
	}
}

// 普通创建仍允许保留导入记录的软删除状态；内部快照读取不能把成功插入变成回滚。
func TestWikiProgressSnapshotPreservesOrdinaryCreateSoftDeleteSemantics(t *testing.T) {
	forWikiProgressDialects(t, func(t *testing.T, db *gorm.DB) {
		ctx := context.Background()
		repo := NewWikiPageRepository(db)
		page := wikiProgressPage("entity/created-deleted")
		page.DeletedAt = gorm.DeletedAt{Time: time.Now(), Valid: true}
		require.NoError(t, repo.Create(ctx, page))
		require.NotEmpty(t, page.ProgressSnapshot)
		require.True(t, page.DeletedAt.Valid)
		_, err := repo.GetByID(ctx, page.ID)
		require.ErrorIs(t, err, ErrWikiPageNotFound)
		var stored types.WikiPage
		require.NoError(t, db.Unscoped().Where("id = ?", page.ID).Take(&stored).Error)
		require.True(t, stored.DeletedAt.Valid)
		require.Equal(t, page.Content, stored.Content)
		snapshot, err := types.WikiPageProgressSnapshot(&stored)
		require.NoError(t, err)
		require.Equal(t, snapshot, page.ProgressSnapshot)
	})
}
