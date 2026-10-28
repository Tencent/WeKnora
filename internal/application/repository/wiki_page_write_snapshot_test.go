package repository

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// setupWikiSnapshotPostgresDB 只在显式指定的测试库里建独立 schema，不复用业务表。
func setupWikiSnapshotPostgresDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL wiki snapshot tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	schema := "wiki_snapshot_" + strings.ReplaceAll(uuid.NewString(), "-", "")
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
	db, err := gorm.Open(postgres.Open(dsn+separator+"search_path="+schema), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}, &types.WikiPageRevision{}))
	// 与正式 PostgreSQL 表一样使用 JSONB，覆盖其序列化格式差异。
	for _, column := range []string{
		"aliases", "category_path", "source_refs", "chunk_refs", "in_links", "out_links", "page_metadata",
	} {
		require.NoError(t, db.Exec(
			"ALTER TABLE wiki_pages ALTER COLUMN "+column+" TYPE JSONB USING "+column+"::jsonb",
		).Error)
	}
	return db
}

// createWikiSnapshotPage 从数据库回读，保证测试快照与真实读取的默认值、JSON格式一致。
func createWikiSnapshotPage(t *testing.T, db *gorm.DB) *types.WikiPage {
	t.Helper()
	repo := NewWikiPageRepository(db)
	page := makeWikiPage(
		uuid.NewString(), "concept/"+uuid.NewString(), types.WikiPageTypeConcept, types.WikiPageStatusPublished,
	)
	page.SourceRefs = types.StringArray{"source-original"}
	page.ChunkRefs = types.StringArray{"chunk-original"}
	page.InLinks = types.StringArray{"incoming-original"}
	page.OutLinks = types.StringArray{"outgoing-original"}
	page.PageMetadata = types.JSON(`{"tag":"original"}`)
	require.NoError(t, repo.Create(context.Background(), page))
	stored, err := repo.GetByID(context.Background(), page.ID)
	require.NoError(t, err)
	return stored
}

func TestWikiPageSnapshotWrite(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			var db *gorm.DB
			if backend == "postgres" {
				db = setupWikiSnapshotPostgresDB(t)
			} else {
				db = setupWikiPagesTestDB(t)
				sqlDB, err := db.DB()
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			}
			repo := NewWikiPageRepository(db)
			changes := map[string]map[string]any{
				"version":               {"version": 7},
				"same version content":  {"content": "concurrent body"},
				"same version sources":  {"source_refs": types.StringArray{"concurrent-source"}},
				"same version chunks":   {"chunk_refs": types.StringArray{"concurrent-chunk"}},
				"same version metadata": {"page_metadata": types.JSON(`{"tag":"concurrent"}`)},
				"same version folder":   {"folder_id": "concurrent-folder"},
			}
			for name, updates := range changes {
				for _, contentWrite := range []bool{false, true} {
					method := "metadata"
					if contentWrite {
						method = "revision"
					}
					t.Run(method+" rejects "+name, func(t *testing.T) {
						page := createWikiSnapshotPage(t, db)
						ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
						attempt, _ := types.WikiPageWriteSnapshotFromContext(ctx)
						attempt.Content = "stale generated body"
						attempt.SourceRefs = append(attempt.SourceRefs, "my-source")
						require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).Updates(updates).Error)
						before, err := repo.GetByID(context.Background(), page.ID)
						require.NoError(t, err)
						if contentWrite {
							err = repo.UpdateWithRevision(
								ctx, attempt, makeWikiRevision(page, page.Version, types.WikiEditSourcePipeline),
							)
						} else {
							err = repo.UpdateMeta(ctx, attempt)
						}
						require.ErrorIs(t, err, ErrWikiPageConflict)
						require.Equal(t, page.Version, attempt.Version)
						after, err := repo.GetByID(context.Background(), page.ID)
						require.NoError(t, err)
						require.True(t, types.WikiPageWriteSnapshotMatches(before, after))
						require.Zero(t, countWikiRevisions(t, db, page.ID))
					})
				}
			}
			t.Run("successful revision", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
				attempt, _ := types.WikiPageWriteSnapshotFromContext(ctx)
				attempt.Content = "merged body"
				attempt.SourceRefs = append(attempt.SourceRefs, "my-source")
				require.NoError(t, repo.UpdateWithRevision(
					ctx, attempt, makeWikiRevision(page, page.Version, types.WikiEditSourcePipeline),
				))
				stored, err := repo.GetByID(context.Background(), page.ID)
				require.NoError(t, err)
				require.Equal(t, page.Version+1, stored.Version)
				require.Equal(t, "merged body", stored.Content)
				require.Equal(t, attempt.SourceRefs, stored.SourceRefs)
				require.EqualValues(t, 1, countWikiRevisions(t, db, page.ID))
			})
			t.Run("metadata preserves concurrent backlinks", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
				attempt, _ := types.WikiPageWriteSnapshotFromContext(ctx)
				attempt.SourceRefs = append(attempt.SourceRefs, "my-source")
				incoming := types.StringArray{"concurrent-incoming"}
				require.NoError(t, db.Model(&types.WikiPage{}).Where("id = ?", page.ID).
					Updates(map[string]any{"in_links": incoming, "updated_at": time.Now()}).Error)
				require.NoError(t, repo.UpdateMeta(ctx, attempt))
				stored, err := repo.GetByID(context.Background(), page.ID)
				require.NoError(t, err)
				require.Equal(t, page.Version, stored.Version)
				require.Equal(t, incoming, stored.InLinks)
				require.Equal(t, attempt.SourceRefs, stored.SourceRefs)
				require.Zero(t, countWikiRevisions(t, db, page.ID))
			})
			t.Run("unguarded metadata keeps existing contract", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				page.SourceRefs = types.StringArray{"replacement"}
				page.InLinks = types.StringArray{"replacement-incoming"}
				require.NoError(t, repo.UpdateMeta(context.Background(), page))
				stored, err := repo.GetByID(context.Background(), page.ID)
				require.NoError(t, err)
				require.Equal(t, page.SourceRefs, stored.SourceRefs)
				require.Equal(t, page.InLinks, stored.InLinks)
				require.Equal(t, page.Version, stored.Version)
			})
			t.Run("deleted page", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
				require.NoError(t, db.Delete(page).Error)
				require.ErrorIs(t, repo.UpdateWithRevision(
					ctx, page, makeWikiRevision(page, page.Version, types.WikiEditSourcePipeline),
				), ErrWikiPageNotFound)
				require.Zero(t, countWikiRevisions(t, db, page.ID))
			})
			t.Run("wrong identity fails closed", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
				page.TenantID++
				require.ErrorIs(t, repo.UpdateMeta(ctx, page), ErrWikiPageConflict)
				page.TenantID--
				page.ID = uuid.NewString()
				require.ErrorIs(t, repo.UpdateWithRevision(ctx, page, nil), ErrWikiPageConflict)
			})
			t.Run("cancelled save", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				ctx, cancel := context.WithCancel(types.WithWikiPageWriteSnapshot(context.Background(), page))
				cancel()
				require.ErrorIs(t, repo.UpdateMeta(ctx, page), context.Canceled)
				require.ErrorIs(t, repo.UpdateWithRevision(ctx, page, nil), context.Canceled)
			})
			t.Run("failed write rolls back inserted revision", func(t *testing.T) {
				page := createWikiSnapshotPage(t, db)
				injected := errors.New("injected wiki update failure")
				callback := "test:wiki_snapshot_rollback_" + uuid.NewString()
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
					if expected, ok := types.WikiPageWriteSnapshotFromContext(tx.Statement.Context); ok &&
						expected.ID == page.ID {
						// AddError 将错误写入事务状态，回调没有独立的错误返回值。
						_ = tx.AddError(injected)
					}
				}))
				t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
				ctx := types.WithWikiPageWriteSnapshot(context.Background(), page)
				attempt, _ := types.WikiPageWriteSnapshotFromContext(ctx)
				attempt.Content = "must roll back"
				require.ErrorIs(t, repo.UpdateWithRevision(
					ctx, attempt, makeWikiRevision(page, page.Version, types.WikiEditSourcePipeline),
				), injected)
				require.Equal(t, page.Version, attempt.Version)
				require.Zero(t, countWikiRevisions(t, db, page.ID))
				stored, err := repo.GetByID(context.Background(), page.ID)
				require.NoError(t, err)
				require.Equal(t, page.Content, stored.Content)
			})
		})
	}
}

// TestWikiPageSnapshotPostgresLocksThroughWrite 用真实锁超时证明检查与写入之间没有空窗。
func TestWikiPageSnapshotPostgresLocksThroughWrite(t *testing.T) {
	db := setupWikiSnapshotPostgresDB(t)
	repo := NewWikiPageRepository(db)
	page := createWikiSnapshotPage(t, db)
	ctx, cancel := context.WithTimeout(types.WithWikiPageWriteSnapshot(context.Background(), page), 10*time.Second)
	defer cancel()
	checked, allowWrite := make(chan struct{}), make(chan struct{})
	var checkedOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(allowWrite) }) }
	defer release()
	callback := "test:wiki_snapshot_lock_" + uuid.NewString()
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if expected, ok := types.WikiPageWriteSnapshotFromContext(tx.Statement.Context); ok && expected.ID == page.ID {
			checkedOnce.Do(func() { close(checked) })
			select {
			case <-allowWrite:
			case <-tx.Statement.Context.Done():
				_ = tx.AddError(tx.Statement.Context.Err())
			}
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(callback)) })
	attempt, _ := types.WikiPageWriteSnapshotFromContext(ctx)
	attempt.Content = "guarded body"
	saved := make(chan error, 1)
	go func() {
		saved <- repo.UpdateWithRevision(
			ctx, attempt, makeWikiRevision(page, page.Version, types.WikiEditSourcePipeline),
		)
	}()
	select {
	case <-checked:
	case err := <-saved:
		t.Fatalf("save returned before guarded write: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// 同版本元数据写也必须等待行锁，而不是从快照检查和正文写之间穿过去。
	err := db.WithContext(types.WithWikiPageWriteSnapshot(ctx, nil)).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL lock_timeout = '250ms'").Error; err != nil {
			return err
		}
		return tx.Model(&types.WikiPage{}).Where("id = ?", page.ID).
			Update("source_refs", types.StringArray{"concurrent-source"}).Error
	})
	release()
	saveErr := <-saved
	require.NoError(t, saveErr)
	var sqlState interface{ SQLState() string }
	require.ErrorAs(t, err, &sqlState)
	require.Equal(t, "55P03", sqlState.SQLState())
	stored, err := repo.GetByID(context.Background(), page.ID)
	require.NoError(t, err)
	require.Equal(t, "guarded body", stored.Content)
	require.Equal(t, page.SourceRefs, stored.SourceRefs)
}

// TestWikiPageSnapshotPostgresConcurrentContributions 验证同版本贡献竞争只允许一方提交，
// 另一方必须读取最新来源后重新合并；整个过程不制造内容版本或历史记录。
func TestWikiPageSnapshotPostgresConcurrentContributions(t *testing.T) {
	db := setupWikiSnapshotPostgresDB(t)
	repo := NewWikiPageRepository(db)
	page := createWikiSnapshotPage(t, db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	guarded := types.WithWikiPageWriteSnapshot(ctx, page)
	type contributionResult struct {
		source string
		err    error
	}
	ready := make(chan struct{}, 2)
	start := make(chan struct{})
	results := make(chan contributionResult, 2)
	for _, source := range []string{"source-a", "source-b"} {
		go func(source string) {
			attempt, _ := types.WikiPageWriteSnapshotFromContext(guarded)
			attempt.SourceRefs = append(attempt.SourceRefs, source)
			ready <- struct{}{}
			select {
			case <-start:
				results <- contributionResult{source: source, err: repo.UpdateMeta(guarded, attempt)}
			case <-ctx.Done():
				results <- contributionResult{source: source, err: ctx.Err()}
			}
		}(source)
	}
	// 两个调用先持有同一读快照，再同时进入数据库；不用 sleep 猜测调度顺序。
	for range 2 {
		select {
		case <-ready:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	close(start)
	var completed []contributionResult
	for range 2 {
		select {
		case result := <-results:
			completed = append(completed, result)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	successes, conflicts := 0, 0
	retrySource := ""
	for _, result := range completed {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, ErrWikiPageConflict):
			conflicts++
			retrySource = result.source
		default:
			t.Fatalf("unexpected save error for %s: %v", result.source, result.err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)

	latest, err := repo.GetByID(ctx, page.ID)
	require.NoError(t, err)
	retryCtx := types.WithWikiPageWriteSnapshot(ctx, latest)
	latest.SourceRefs = append(latest.SourceRefs, retrySource)
	require.NoError(t, repo.UpdateMeta(retryCtx, latest))
	stored, err := repo.GetByID(ctx, page.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"source-original", "source-a", "source-b"}, []string(stored.SourceRefs))
	require.Equal(t, page.Version, stored.Version)
	require.Equal(t, page.Content, stored.Content)
	require.Zero(t, countWikiRevisions(t, db, page.ID))
}
