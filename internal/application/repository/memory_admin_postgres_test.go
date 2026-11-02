package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// TestMemoryAdminPostgres 在独立 schema 验证真实行锁、分页 SQL 与并发状态变更。
func TestMemoryAdminPostgres(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MEMORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MEMORY_TEST_POSTGRES_DSN to run PostgreSQL integration tests")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
	schema := "memory_admin_" + uuid.NewString()[:8]
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema+",public"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(8)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.MemorySubject{}, &types.MemoryItem{}, &types.MemoryTombstone{},
		&types.MemoryTopicStat{}, &types.MemoryDocAffinity{}, &types.MemoryItemEmbedding{},
		&types.MemoryExtractionSession{}))
	repo := NewMemoryRepository(db)
	ctx := context.Background()
	scope := interfaces.MemoryScope{TenantID: 7, SubjectID: "im_user:channel:customer"}
	subject, err := repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	foreign := interfaces.MemoryScope{TenantID: 8, SubjectID: scope.SubjectID}
	_, err = repo.EnsureSubject(ctx, foreign)
	require.NoError(t, err)
	active := &types.MemoryItem{
		ID: uuid.NewString(), Kind: types.MemoryKindFact, NormalizedKey: "confirmed",
		Content: "confirmed fact", Status: types.MemoryStatusActive, ValidFrom: time.Now(),
	}
	require.NoError(t, repo.SaveItem(ctx, scope, active, ""))

	t.Run("concurrent pending saves stay bounded", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 16)
		for i := range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				item := &types.MemoryItem{
					ID: uuid.NewString(), Kind: types.MemoryKindFact,
					NormalizedKey: fmt.Sprintf("pending-%d", i), Content: fmt.Sprintf("proposal %d", i),
					Status: types.MemoryStatusPending, ValidFrom: time.Now(),
				}
				errs <- repo.SaveItemWithPendingLimit(ctx, scope, item, "", 4)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		rows, total, err := repo.ListSubjects(ctx, 7, 1, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, rows, 1)
		require.Equal(t, subject.ID, rows[0].ID)
		require.Equal(t, types.PrincipalIMUser, rows[0].PrincipalType)
		require.EqualValues(t, 17, rows[0].ItemCount)
		require.EqualValues(t, 1, rows[0].ActiveCount)
		require.EqualValues(t, 4, rows[0].PendingCount)
		require.False(t, rows[0].LastActivityAt.IsZero())
		got, err := repo.GetSubjectByID(ctx, 8, subject.ID)
		require.NoError(t, err)
		require.Nil(t, got)
	})

	t.Run("confirm and reject serialize", func(t *testing.T) {
		item := &types.MemoryItem{
			ID: uuid.NewString(), Kind: types.MemoryKindFact, NormalizedKey: "decision",
			Content: "concurrent decision", Status: types.MemoryStatusPending, ValidFrom: time.Now(),
		}
		require.NoError(t, repo.SaveItemWithPendingLimit(ctx, scope, item, "", 4))
		start := make(chan struct{})
		confirmed, rejected := make(chan error, 1), make(chan error, 1)
		go func() { <-start; confirmed <- repo.ConfirmPendingItem(ctx, scope, item.ID) }()
		go func() { <-start; rejected <- repo.RejectPendingItem(ctx, scope, item.ID) }()
		close(start)
		confirmErr, rejectErr := <-confirmed, <-rejected
		require.False(t, confirmErr == nil && rejectErr == nil,
			"a reject must not delete a concurrently confirmed fact")
		got, err := repo.GetItem(ctx, scope, item.ID)
		require.NoError(t, err)
		if confirmErr == nil {
			require.ErrorIs(t, rejectErr, types.ErrMemoryConflict)
			require.NotNil(t, got)
			require.Equal(t, types.MemoryStatusActive, got.Status)
		} else {
			require.NoError(t, rejectErr)
			require.ErrorIs(t, confirmErr, gorm.ErrRecordNotFound)
			require.Nil(t, got)
		}
	})

	t.Run("clear remains tenant scoped", func(t *testing.T) {
		marker := &types.MemoryItem{
			ID: uuid.NewString(), TenantID: 8, SubjectID: foreign.SubjectID,
			Kind: types.MemoryKindFact, Content: "foreign fact", Status: types.MemoryStatusActive,
		}
		require.NoError(t, repo.CreateItem(ctx, marker))
		removed, err := repo.ClearSubjectMemory(ctx, scope)
		require.NoError(t, err)
		require.Greater(t, removed, int64(0))
		got, err := repo.GetItem(ctx, foreign, marker.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		_, total, err := repo.ListItems(ctx, scope, "", 20, 0)
		require.NoError(t, err)
		require.Zero(t, total)
	})

	t.Run("clear fences overlapping writes and late derived state", func(t *testing.T) {
		for range 12 {
			snapshot, err := repo.GetSubject(ctx, scope)
			require.NoError(t, err)
			staleScope := scope.WithGeneration(snapshot.ExtractionState.Generation)
			item := &types.MemoryItem{
				ID: uuid.NewString(), Kind: types.MemoryKindProfile,
				Content: "pre-clear private profile", NormalizedKey: uuid.NewString(),
				Status: types.MemoryStatusActive, ValidFrom: time.Now(),
			}
			start := make(chan struct{})
			written, cleared := make(chan error, 1), make(chan error, 1)
			go func() { <-start; written <- repo.SaveItemWithPendingLimit(ctx, staleScope, item, "", 4) }()
			go func() {
				<-start
				_, err := repo.ClearSubjectMemory(ctx, scope)
				cleared <- err
			}()
			close(start)
			writeErr, clearErr := <-written, <-cleared
			require.NoError(t, clearErr)
			require.True(t, writeErr == nil || errors.Is(writeErr, types.ErrMemoryGenerationChanged), "%v", writeErr)
			_, total, err := repo.ListItems(ctx, scope, "", 20, 0)
			require.NoError(t, err)
			require.Zero(t, total, "the old write either precedes clear or is rejected by its generation")
			require.ErrorIs(t, repo.UpdateSubjectBlock(ctx, staleScope, "pre-clear private profile", 1),
				types.ErrMemoryGenerationChanged)
			require.ErrorIs(t, repo.BumpDocAffinity(ctx, staleScope, []types.MemoryDocAffinity{{
				KnowledgeID: "private-doc", Title: "pre-clear title",
			}}), types.ErrMemoryGenerationChanged)
			snapshot, err = repo.GetSubject(ctx, scope)
			require.NoError(t, err)
			require.Empty(t, snapshot.BlockText)
			freshScope := scope.WithGeneration(snapshot.ExtractionState.Generation)
			fresh := &types.MemoryItem{
				ID: uuid.NewString(), Kind: types.MemoryKindFact, Content: "fresh fact",
				NormalizedKey: uuid.NewString(), Status: types.MemoryStatusActive, ValidFrom: time.Now(),
			}
			require.NoError(t, repo.SaveItemWithPendingLimit(ctx, freshScope, fresh, "", 4))
		}
		_, total, err := repo.ListItems(ctx, foreign, "", 20, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, total, "the same subject identifier in another tenant remains untouched")
	})
}
