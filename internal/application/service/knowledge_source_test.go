package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type sourceEditRepo struct {
	*replaceFileRepo
	beforePublish func()
	rejectPublish bool
}

func (r *sourceEditRepo) UpdateKnowledgeSourceIfUnchanged(
	ctx context.Context, previous *types.Knowledge, values map[string]interface{},
) (bool, error) {
	if r.beforePublish != nil {
		r.beforePublish()
	}
	if r.rejectPublish || knowledgeSourceRevision(previous) != knowledgeSourceRevision(&r.row) {
		return false, nil
	}
	return true, r.UpdateKnowledgeColumns(ctx, previous.ID, values)
}

type sourceEditStore struct {
	*replaceFileStore
	content string
}

func (f *sourceEditStore) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.content)), nil
}

func sourceEditFixture(t *testing.T) (*replaceFileHarness, *sourceEditRepo, *sourceEditStore) {
	t.Helper()
	h := newReplaceFileHarness(t)
	repo := &sourceEditRepo{replaceFileRepo: h.repo}
	store := &sourceEditStore{replaceFileStore: h.store, content: replaceFileOldContent}
	h.svc.repo, h.svc.fileSvc = repo, store
	return h, repo, store
}

func TestKnowledgeSourceReadSaveAndDuplicate(t *testing.T) {
	h, _, _ := sourceEditFixture(t)
	source, err := h.svc.GetKnowledgeSourceContent(h.ctx, h.original.ID)
	require.NoError(t, err)
	require.Equal(t, replaceFileOldContent, source.Content)
	require.Equal(t, h.original.FileName, source.FileName)
	unchanged, err := h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID, source.Content, source.Revision)
	require.NoError(t, err)
	require.Equal(t, h.original.ID, unchanged.ID)
	require.Empty(t, h.events, "identical content must not upload or reparse")

	updated, err := h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID, "# 新内容\r\n", source.Revision)
	require.NoError(t, err)
	require.Equal(t, h.original.ID, updated.ID)
	require.Equal(t, h.original.FolderPath, updated.FolderPath)
	require.Equal(t, h.original.Metadata, updated.Metadata)
	require.Equal(t, md5Hex("# 新内容\r\n"), updated.FileHash)
	require.Equal(t, types.ParseStatusPending, updated.ParseStatus)
	require.Len(t, h.tasks.payloads, 1)
	require.Contains(t, h.store.deleted, h.original.FilePath)
}

func TestKnowledgeSourceRejectsStaleEditorBeforeUpload(t *testing.T) {
	h, _, _ := sourceEditFixture(t)
	_, err := h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID, "new", "stale")
	var appErr *werrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, werrors.ErrConflict, appErr.Code)
	require.Empty(t, h.events)
}

func TestKnowledgeSourceConflictDuringUploadDoesNotCancelWinner(t *testing.T) {
	h, repo, _ := sourceEditFixture(t)
	revision := knowledgeSourceRevision(&h.original)
	repo.beforePublish = func() { repo.row.FilePath = "winner.md" }
	_, err := h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID, "new", revision)
	require.Error(t, err)
	require.Equal(t, "winner.md", repo.row.FilePath)
	require.Empty(t, h.tasks.payloads)
	require.Equal(t, []string{"save", "delete:new/file.md"}, h.events)
}

func TestKnowledgeSourceFailureKeepsPreviousSource(t *testing.T) {
	for _, failure := range []string{"storage", "database", "queue", "ambiguous-database"} {
		t.Run(failure, func(t *testing.T) {
			h, _, _ := sourceEditFixture(t)
			switch failure {
			case "storage":
				h.store.saveErr = errors.New("storage unavailable")
			case "database", "ambiguous-database":
				h.repo.failColumnsCall = 1
				h.repo.commitThenFail = failure == "ambiguous-database"
			case "queue":
				h.tasks.err = errors.New("queue unavailable")
			}
			_, err := h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID,
				"new", knowledgeSourceRevision(&h.original))
			if failure == "ambiguous-database" {
				require.NoError(t, err)
				require.Equal(t, "new/file.md", h.repo.row.FilePath)
				return
			}
			require.Error(t, err)
			require.Equal(t, h.original.FilePath, h.repo.row.FilePath)
			require.NotContains(t, h.store.deleted, h.original.FilePath)
		})
	}
}

func TestKnowledgeSourceRejectsUnsupportedAndBusyDocuments(t *testing.T) {
	for _, kind := range []string{"pdf", "manual", "pending", "processing", "finalizing", "deleting"} {
		t.Run(kind, func(t *testing.T) {
			h, _, _ := sourceEditFixture(t)
			switch kind {
			case "pdf":
				h.repo.row.FileType = kind
			case "manual":
				h.repo.row.Type = kind
			default:
				h.repo.row.ParseStatus = kind
			}
			_, err := h.svc.GetKnowledgeSourceContent(h.ctx, h.original.ID)
			require.Error(t, err)
			_, err = h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID,
				"new", knowledgeSourceRevision(&h.original))
			require.Error(t, err)
			require.Empty(t, h.events)
		})
	}
}

func TestKnowledgeSourceBoundsAndEncoding(t *testing.T) {
	for _, content := range []string{"\xff", "a\x00b", strings.Repeat("x", types.MaxKnowledgeSourceBytes+1)} {
		h, _, store := sourceEditFixture(t)
		store.content = content
		// Metadata may understate actual size; the reader must enforce its own bound.
		_, err := h.svc.GetKnowledgeSourceContent(h.ctx, h.original.ID)
		require.Error(t, err)
		_, err = h.svc.UpdateKnowledgeSourceContent(h.ctx, h.original.ID, content, knowledgeSourceRevision(&h.original))
		require.Error(t, err)
		require.Empty(t, h.events)
	}
}

func TestKnowledgeSourceRequiresWriteGrant(t *testing.T) {
	h, _, _ := sourceEditFixture(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	_, err := h.svc.GetKnowledgeSourceContent(ctx, h.original.ID)
	require.Error(t, err)
	_, err = h.svc.UpdateKnowledgeSourceContent(ctx, h.original.ID, "new", knowledgeSourceRevision(&h.original))
	require.Error(t, err)
	require.Empty(t, h.events)
}
