package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newKnowledgeFileVersionRepo(t *testing.T) (*knowledgeRepository, *gorm.DB, *types.Knowledge) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeFileVersion{}))
	before := &types.Knowledge{
		ID: "file-1", TenantID: 7, KnowledgeBaseID: "kb-1", Type: "file", FileVersion: 1,
		FilePath: "original/file.md", FileName: "file.md", FileType: "md", FileHash: "old-hash", FileSize: 3,
		ParseStatus: types.ParseStatusCompleted, Metadata: types.JSON(`{}`), CreatedAt: time.Now().Add(-time.Hour),
	}
	repo := &knowledgeRepository{db: db}
	require.NoError(t, repo.CreateKnowledge(context.Background(), before))
	return repo, db, before
}

func fileVersionReplacementColumns() map[string]interface{} {
	return map[string]interface{}{
		"file_version": 2, "file_path": "replacement/file.md", "file_hash": "new-hash",
		"parse_status": types.ParseStatusPending, "updated_at": time.Now(),
	}
}

func TestKnowledgeFileVersionAtomicArchiveAndConflict(t *testing.T) {
	repo, _, before := newKnowledgeFileVersionRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.ReplaceKnowledgeSource(ctx, before, fileVersionReplacementColumns()))
	current, err := repo.GetKnowledgeByID(ctx, before.TenantID, before.ID)
	require.NoError(t, err)
	require.Equal(t, 2, current.FileVersion)
	archived, err := repo.GetKnowledgeFileVersion(ctx, before.TenantID, before.ID, 1)
	require.NoError(t, err)
	require.Equal(t, before.FilePath, archived.FilePath)
	require.Equal(t, before.FileHash, archived.FileHash)
	require.True(t, before.CreatedAt.Equal(archived.CreatedAt))
	require.ErrorIs(t, repo.ReplaceKnowledgeSource(ctx, before, fileVersionReplacementColumns()),
		ErrKnowledgeFileVersionConflict)
	rows, total, err := repo.ListKnowledgeFileVersions(ctx, 7, before.ID, 10, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, rows, 1)
}

func TestKnowledgeFileVersionArchiveFailureRollsBackSource(t *testing.T) {
	repo, db, before := newKnowledgeFileVersionRepo(t)
	// An existing manifest violates the unique version constraint after the source
	// update: the transaction must undo that update as well.
	archived := before.FileVersionSnapshot()
	archived.ID = uuid.NewString()
	require.NoError(t, db.Create(archived).Error)
	require.Error(t, repo.ReplaceKnowledgeSource(context.Background(), before, fileVersionReplacementColumns()))
	current, err := repo.GetKnowledgeByID(context.Background(), 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, before.FileVersion, current.FileVersion)
	require.Equal(t, before.FilePath, current.FilePath)
	require.Equal(t, before.ParseStatus, current.ParseStatus)
}

func TestKnowledgeFileVersionCASGuards(t *testing.T) {
	for _, column := range []string{"tenant_id", "file_version", "file_path", "updated_at", "parse_status"} {
		t.Run(column, func(t *testing.T) {
			repo, db, before := newKnowledgeFileVersionRepo(t)
			values := map[string]interface{}{
				"tenant_id": uint64(9), "file_version": 3, "file_path": "other.md",
				"updated_at": before.UpdatedAt.Add(time.Second), "parse_status": types.ParseStatusProcessing,
			}
			require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", before.ID).
				UpdateColumn(column, values[column]).Error)
			require.ErrorIs(t,
				repo.ReplaceKnowledgeSource(context.Background(), before, fileVersionReplacementColumns()),
				ErrKnowledgeFileVersionConflict)
			var count int64
			require.NoError(t, db.Model(&types.KnowledgeFileVersion{}).Count(&count).Error)
			require.Zero(t, count, "a rejected source swap must not create history")
		})
	}
}

func TestKnowledgeFileVersionRestoreAndTenantIsolation(t *testing.T) {
	repo, _, before := newKnowledgeFileVersionRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.ReplaceKnowledgeSource(ctx, before, fileVersionReplacementColumns()))
	rows, total, err := repo.ListKnowledgeFileVersions(ctx, 9, before.ID, 10, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total)
	_, err = repo.GetKnowledgeFileVersion(ctx, 9, before.ID, 1)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, repo.DeleteKnowledgeFileVersions(ctx, 9, before.ID))
	restore := map[string]interface{}{
		"file_version": 1, "file_path": before.FilePath, "parse_status": types.ParseStatusFailed,
	}
	wrongTenant := *before
	wrongTenant.TenantID = 9
	require.ErrorIs(t, repo.RestoreKnowledgeSource(ctx, &wrongTenant, "replacement/file.md", restore),
		ErrKnowledgeFileVersionConflict)
	require.ErrorIs(t, repo.RestoreKnowledgeSource(ctx, before, "wrong/file.md", restore),
		ErrKnowledgeFileVersionConflict)
	_, err = repo.GetKnowledgeFileVersion(ctx, 7, before.ID, 1)
	require.NoError(t, err, "failed compensations must preserve history")
	require.NoError(t, repo.RestoreKnowledgeSource(ctx, before, "replacement/file.md", restore))
	current, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, 1, current.FileVersion)
	require.Equal(t, before.FilePath, current.FilePath)
	_, total, err = repo.ListKnowledgeFileVersions(ctx, 7, before.ID, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestKnowledgeFileVersionRejectsStaleWorkerFullRowUpdate(t *testing.T) {
	repo, _, before := newKnowledgeFileVersionRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.ReplaceKnowledgeSource(ctx, before, fileVersionReplacementColumns()))
	stale := *before
	stale.ParseStatus = types.ParseStatusCompleted
	require.ErrorIs(t, repo.UpdateKnowledge(ctx, &stale),
		ErrKnowledgeFileVersionConflict)
	current, err := repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, 2, current.FileVersion)
	require.Equal(t, "replacement/file.md", current.FilePath)
	require.Equal(t, types.ParseStatusPending, current.ParseStatus)
	// The worker for the current revision can still save processing results.
	current.ParseStatus = types.ParseStatusCompleted
	require.NoError(t, repo.UpdateKnowledge(ctx, current))
	current, err = repo.GetKnowledgeByID(ctx, 7, before.ID)
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusCompleted, current.ParseStatus)
	require.Equal(t, 2, current.FileVersion)
}
