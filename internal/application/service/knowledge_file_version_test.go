package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// Exercise the production versioned branch, including GORM full-row updates
// during reparse, rather than teaching the legacy replacement fake to archive.
func newVersionedReplaceHarness(
	t *testing.T,
) (*replaceFileHarness, *gorm.DB, interfaces.KnowledgeFileVersionRepository) {
	t.Helper()
	h := newReplaceFileHarness(t)
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}, &types.KnowledgeFileVersion{}))
	h.svc.repo = repository.NewKnowledgeRepository(db)
	require.NoError(t, h.svc.repo.CreateKnowledge(h.ctx, &h.original))
	return h, db, h.svc.repo.(interfaces.KnowledgeFileVersionRepository)
}

func TestVersionedReplaceRetainsOriginalAndListsCurrentFirst(t *testing.T) {
	h, _, versions := newVersionedReplaceHarness(t)
	got, err := h.replace(t, "updated body", "notes/b.md", nil)
	require.NoError(t, err)
	require.Equal(t, 2, got.FileVersion)
	row, err := h.svc.repo.GetKnowledgeByID(h.ctx, 7, h.original.ID)
	require.NoError(t, err)
	require.Equal(t, 2, row.FileVersion, "reparse full-row Save must preserve source version")
	require.NotNil(t, row.FileVersionCreatedAt)
	require.Equal(t, "new/file.md", row.FilePath)
	require.Empty(t, h.store.deleted, "original remains downloadable")
	require.Len(t, h.tasks.payloads, 1)
	archived, err := versions.GetKnowledgeFileVersion(h.ctx, 7, h.original.ID, 1)
	require.NoError(t, err)
	require.Equal(t, h.original.FilePath, archived.FilePath)
	require.Equal(t, h.original.FileHash, archived.FileHash)
	first, err := h.svc.ListKnowledgeFileVersions(h.ctx, h.original.ID, 1, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, first.Total)
	require.Len(t, first.Items, 1)
	require.True(t, first.Items[0].IsCurrent)
	require.Equal(t, 2, first.Items[0].Version)
	second, err := h.svc.ListKnowledgeFileVersions(h.ctx, h.original.ID, 1, 1)
	require.NoError(t, err)
	require.Len(t, second.Items, 1)
	require.False(t, second.Items[0].IsCurrent)
	require.Equal(t, 1, second.Items[0].Version)
}

func TestVersionedReplaceFailureRestoresSourceAndRemovesArchive(t *testing.T) {
	h, _, versions := newVersionedReplaceHarness(t)
	h.tasks.err = errors.New("queue unavailable")
	_, err := h.replace(t, "updated body", "notes/b.md", nil)
	require.Error(t, err)
	row, err := h.svc.repo.GetKnowledgeByID(h.ctx, 7, h.original.ID)
	require.NoError(t, err)
	require.Equal(t, h.original.FilePath, row.FilePath)
	require.Equal(t, h.original.FileHash, row.FileHash)
	require.Equal(t, h.original.FileName, row.FileName)
	require.Equal(t, 1, row.FileVersion)
	require.Nil(t, row.FileVersionCreatedAt)
	require.Equal(t, types.ParseStatusFailed, row.ParseStatus)
	require.Equal(t, []string{"new/file.md"}, h.store.deleted)
	_, total, err := versions.ListKnowledgeFileVersions(h.ctx, 7, h.original.ID, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestVersionedReplaceDuplicateDoesNotCreateHistory(t *testing.T) {
	h, _, versions := newVersionedReplaceHarness(t)
	_, err := h.replace(t, replaceFileOldContent, "notes/a.md", map[string]string{"source_updated_at": "later"})
	var duplicate *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &duplicate)
	require.Empty(t, h.events)
	_, total, err := versions.ListKnowledgeFileVersions(h.ctx, 7, h.original.ID, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	row, err := h.svc.repo.GetKnowledgeByID(h.ctx, 7, h.original.ID)
	require.NoError(t, err)
	require.Equal(t, "later", row.GetMetadata()["source_updated_at"])
	require.Equal(t, 1, row.FileVersion)
}

func TestVersionedReplaceRejectsBusyOrStaleUploadBeforeSaving(t *testing.T) {
	for _, status := range []string{
		types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing, types.ParseStatusCompleted,
	} {
		t.Run(status, func(t *testing.T) {
			h, db, versions := newVersionedReplaceHarness(t)
			require.NoError(t, db.Model(&types.Knowledge{}).Where("id = ?", h.original.ID).
				Update("parse_status", status).Error)
			expected := 1
			if status == types.ParseStatusCompleted {
				expected = 2
			}
			file, err := bytesToFileHeader([]byte("updated body"), "a.md")
			require.NoError(t, err)
			_, err = h.svc.UploadKnowledgeFileVersion(h.ctx, h.original.ID, file, expected)
			var appErr *werrors.AppError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, werrors.ErrConflict, appErr.Code)
			require.Empty(t, h.events)
			_, total, err := versions.ListKnowledgeFileVersions(h.ctx, 7, h.original.ID, 10, 0)
			require.NoError(t, err)
			require.Zero(t, total)
		})
	}
}

type versionDownloadStore struct {
	*replaceFileStore
	paths []string
}

func (s *versionDownloadStore) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	s.paths = append(s.paths, path)
	return io.NopCloser(strings.NewReader("bytes from " + path)), nil
}

func TestVersionedDownloadResolvesRetainedSource(t *testing.T) {
	h, _, _ := newVersionedReplaceHarness(t)
	_, err := h.replace(t, "updated body", "notes/b.md", nil)
	require.NoError(t, err)
	store := &versionDownloadStore{replaceFileStore: h.store}
	h.svc.fileSvc = store
	for _, tt := range []struct {
		version    int
		name, path string
	}{
		{1, "a.md", "old/file.md"}, {2, "b.md", "new/file.md"},
	} {
		file, name, err := h.svc.GetKnowledgeFileVersion(h.ctx, h.original.ID, tt.version)
		require.NoError(t, err)
		body, err := io.ReadAll(file)
		require.NoError(t, err)
		require.NoError(t, file.Close())
		require.Equal(t, tt.name, name)
		require.Equal(t, "bytes from "+tt.path, string(body))
	}
	require.Equal(t, []string{"old/file.md", "new/file.md"}, store.paths)
	_, _, err = h.svc.GetKnowledgeFileVersion(h.ctx, h.original.ID, 99)
	var appErr *werrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, werrors.ErrNotFound, appErr.Code)
	require.Len(t, store.paths, 2, "missing version must not open current bytes")
}

func TestVersionedHistoryCleanupDeletesRetainedFilesAndManifest(t *testing.T) {
	h, _, versions := newVersionedReplaceHarness(t)
	_, err := h.replace(t, "updated body", "notes/b.md", nil)
	require.NoError(t, err)
	cleanupKnowledgeFileVersions(h.ctx, h.svc.repo, 7, h.original.ID,
		func(string) interfaces.FileService { return h.store })
	require.Equal(t, []string{"old/file.md"}, h.store.deleted)
	_, total, err := versions.ListKnowledgeFileVersions(h.ctx, 7, h.original.ID, 10, 0)
	require.NoError(t, err)
	require.Zero(t, total)
}
