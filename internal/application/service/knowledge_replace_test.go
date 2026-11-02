package service

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// replaceFileRepo is a single-row knowledge store. UpdateKnowledgeColumns can
// fail on a chosen call, either before applying the write or after it (a write
// that committed but reported an error).
type replaceFileRepo struct {
	interfaces.KnowledgeRepository
	row                types.Knowledge
	columnsCalls       int
	failColumnsCall    int
	commitThenFail     bool
	beforeSourceUpdate func(int)
	afterFailedCommit  func()
}

func (r *replaceFileRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	row := r.row
	return &row, nil
}

func (r *replaceFileRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	folder, kb := r.row.FolderPath, r.row.KnowledgeBaseID
	knowledge.UpdatedAt = time.Now().UTC()
	r.row = *knowledge
	r.row.FolderPath, r.row.KnowledgeBaseID = folder, kb
	return nil
}

func (r *replaceFileRepo) UpdateKnowledgeColumn(_ context.Context, _ string, column string, value interface{}) error {
	r.apply(map[string]interface{}{column: value})
	return nil
}

func (r *replaceFileRepo) UpdateKnowledgeColumns(_ context.Context, _ string, values map[string]interface{}) error {
	r.columnsCalls++
	if r.columnsCalls != r.failColumnsCall {
		r.apply(values)
		return nil
	}
	if r.commitThenFail {
		r.apply(values)
	}
	return errors.New("database unavailable")
}

// Mirror the repository CAS contract, with hooks for service interleavings.
func (r *replaceFileRepo) UpdateKnowledgeSourceIfCurrent(
	ctx context.Context,
	before *types.Knowledge,
	values map[string]interface{},
) (*types.Knowledge, error) {
	if r.beforeSourceUpdate != nil {
		r.beforeSourceUpdate(r.columnsCalls + 1)
	}
	if before.ID != r.row.ID || before.TenantID != r.row.TenantID || before.KnowledgeBaseID != r.row.KnowledgeBaseID ||
		before.FilePath != r.row.FilePath ||
		before.FileHash != r.row.FileHash ||
		r.row.ParseStatus == types.ParseStatusDeleting {
		return nil, repository.ErrKnowledgeFolderChanged
	}
	if err := access.RejectMovingKnowledge(&r.row); err != nil {
		return nil, err
	}
	if status, ok := values["parse_status"].(string); ok {
		if status == types.ParseStatusPending &&
			(r.row.ParseStatus == types.ParseStatusPending ||
				r.row.ParseStatus == types.ParseStatusProcessing ||
				r.row.ParseStatus == types.ParseStatusFinalizing ||
				r.row.ParseStatus != before.ParseStatus) {
			return nil, repository.ErrKnowledgeFolderChanged
		}
		if status == types.ParseStatusFailed &&
			(!r.row.UpdatedAt.Equal(before.UpdatedAt) ||
				r.row.ParseStatus != before.ParseStatus) {
			return nil, repository.ErrKnowledgeFolderChanged
		}
	}
	if before.GetMetadata()[types.KnowledgeReparseMetadataKey] !=
		r.row.GetMetadata()[types.KnowledgeReparseMetadataKey] {
		return nil, repository.ErrKnowledgeFolderChanged
	}
	if metadata, ok := values["metadata"].(types.JSON); ok {
		fields, _ := metadata.Map()
		if fields == nil {
			fields = map[string]interface{}{}
		}
		if token := r.row.GetMetadata()[types.KnowledgeReparseMetadataKey]; token != "" {
			fields[types.KnowledgeReparseMetadataKey] = token
		} else {
			delete(fields, types.KnowledgeReparseMetadataKey)
		}
		encoded, _ := json.Marshal(fields)
		values["metadata"] = types.JSON(encoded)
	}
	if _, ok := values["folder_path"]; ok {
		return nil, errors.New("source update cannot move folders")
	}
	if err := r.UpdateKnowledgeColumns(ctx, before.ID, values); err != nil {
		if r.commitThenFail && r.afterFailedCommit != nil {
			r.afterFailedCommit()
		}
		return nil, err
	}
	row := r.row
	return &row, nil
}

func (r *replaceFileRepo) ClaimKnowledgeForReparse(
	_ context.Context,
	before *types.Knowledge,
	allowPending bool,
) (*types.Knowledge, error) {
	if before.ID != r.row.ID ||
		before.TenantID != r.row.TenantID ||
		before.KnowledgeBaseID != r.row.KnowledgeBaseID ||
		before.FilePath != r.row.FilePath ||
		before.FileHash != r.row.FileHash ||
		!before.UpdatedAt.Equal(r.row.UpdatedAt) ||
		before.ParseStatus != r.row.ParseStatus {
		return nil, repository.ErrKnowledgeFolderChanged
	}
	switch before.ParseStatus {
	case types.ParseStatusCompleted, types.ParseStatusFailed, types.ParseStatusCancelled:
	case types.ParseStatusPending:
		if !allowPending {
			return nil, repository.ErrKnowledgeFolderChanged
		}
	default:
		return nil, repository.ErrKnowledgeFolderChanged
	}
	r.row.ParseStatus = types.ParseStatusProcessing
	r.row.EnableStatus = "disabled"
	fields, _ := before.Metadata.Map()
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields[types.KnowledgeReparseMetadataKey] = fmt.Sprintf("claim-%d", time.Now().UnixNano())
	encoded, _ := json.Marshal(fields)
	r.row.Metadata = types.JSON(encoded)
	r.row.UpdatedAt = time.Now().UTC()
	row := r.row
	return &row, nil
}

func (r *replaceFileRepo) FailKnowledgeReparseClaim(
	_ context.Context,
	claim *types.Knowledge,
	message string,
) (*types.Knowledge, error) {
	if claim.ID != r.row.ID ||
		claim.KnowledgeBaseID != r.row.KnowledgeBaseID ||
		claim.FilePath != r.row.FilePath ||
		claim.GetMetadata()[types.KnowledgeReparseMetadataKey] !=
			r.row.GetMetadata()[types.KnowledgeReparseMetadataKey] ||
		r.row.ParseStatus != types.ParseStatusProcessing {
		return nil, repository.ErrKnowledgeFolderChanged
	}
	r.row.ParseStatus = types.ParseStatusFailed
	r.row.ErrorMessage = message
	r.row.UpdatedAt = time.Now().UTC()
	row := r.row
	return &row, nil
}

func (r *replaceFileRepo) apply(values map[string]interface{}) {
	for column, value := range values {
		switch column {
		case "title":
			r.row.Title = value.(string)
		case "file_name":
			r.row.FileName = value.(string)
		case "folder_path":
			r.row.FolderPath = value.(string)
		case "file_type":
			r.row.FileType = value.(string)
		case "file_size":
			r.row.FileSize = value.(int64)
		case "file_hash":
			r.row.FileHash = value.(string)
		case "file_path":
			r.row.FilePath = value.(string)
		case "metadata":
			r.row.Metadata = value.(types.JSON)
		case "parse_status":
			r.row.ParseStatus = value.(string)
		case "enable_status":
			r.row.EnableStatus = value.(string)
		case "error_message":
			r.row.ErrorMessage = value.(string)
		case "updated_at":
			r.row.UpdatedAt = value.(time.Time)
		}
	}
}

type replaceFileStore struct {
	interfaces.FileService
	saveErr          error
	saved            int
	deleted          []string
	events           *[]string
	beforeSaveReturn func()
}

func (f *replaceFileStore) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	if f.saveErr != nil {
		return "", f.saveErr
	}
	f.saved++
	*f.events = append(*f.events, "save")
	if f.beforeSaveReturn != nil {
		f.beforeSaveReturn()
	}
	return "new/file.md", nil
}

func (f *replaceFileStore) DeleteFile(_ context.Context, filePath string) error {
	f.deleted = append(f.deleted, filePath)
	*f.events = append(*f.events, "delete:"+filePath)
	return nil
}

type replaceFileEnqueuer struct {
	err      error
	payloads []types.DocumentProcessPayload
	events   *[]string
}

func (e *replaceFileEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	if e.err != nil {
		return nil, e.err
	}
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return nil, err
	}
	e.payloads = append(e.payloads, payload)
	*e.events = append(*e.events, "enqueue")
	return &asynq.TaskInfo{ID: "task-1", Queue: types.QueueDefault}, nil
}

type replaceFileChunks struct{ interfaces.ChunkRepository }

func (replaceFileChunks) ListImageInfoByKnowledgeIDs(
	context.Context, uint64, []string,
) ([]interfaces.ChunkImageInfo, error) {
	return nil, nil
}

func (replaceFileChunks) DeleteChunksByKnowledgeID(context.Context, uint64, string) error { return nil }

type replaceFileChunkService struct{ interfaces.ChunkService }

func (replaceFileChunkService) GetRepository() interfaces.ChunkRepository { return replaceFileChunks{} }

type replaceFileGraph struct {
	interfaces.RetrieveGraphRepository
}

func (replaceFileGraph) DelGraph(context.Context, []types.NameSpace) error { return nil }

type replaceFileInspector struct {
	fakeTaskInspector
	events *[]string
}

func (i *replaceFileInspector) CancelTasksForKnowledge(_ context.Context, knowledgeID string) (int, int, error) {
	*i.events = append(*i.events, "dequeue:"+knowledgeID)
	return 1, 0, nil
}

type replaceFileHarness struct {
	svc      *knowledgeService
	repo     *replaceFileRepo
	store    *replaceFileStore
	tasks    *replaceFileEnqueuer
	events   []string
	original types.Knowledge
	ctx      context.Context
}

const replaceFileOldContent = "old"

func newReplaceFileHarness(t *testing.T) *replaceFileHarness {
	t.Helper()
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7}
	h := &replaceFileHarness{}
	h.original = types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		Title:           "a.md",
		FileName:        "a.md",
		FolderPath:      "notes",
		FileType:        "md",
		FileSize:        int64(len(replaceFileOldContent)),
		FileHash:        md5Hex(replaceFileOldContent),
		FilePath:        "old/file.md",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
		Metadata:        types.JSON(`{"external_id":"notes/a.md","extra":{"nested":true}}`),
	}
	h.repo = &replaceFileRepo{row: h.original}
	h.store = &replaceFileStore{events: &h.events}
	h.tasks = &replaceFileEnqueuer{events: &h.events}
	h.svc = &knowledgeService{
		repo:          h.repo,
		kbService:     &reparseFailureKBService{kb: kb},
		fileSvc:       h.store,
		task:          h.tasks,
		taskInspector: &replaceFileInspector{events: &h.events},
		chunkService:  replaceFileChunkService{},
		chunkRepo:     replaceFileChunks{},
		graphEngine:   replaceFileGraph{},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 7})
	ctx, err := access.WithKBTaskWrite(ctx, kb, 7)
	require.NoError(t, err)
	h.ctx = ctx
	return h
}

func (h *replaceFileHarness) replace(
	t *testing.T, content, customFileName string, metadata map[string]string,
) (*types.Knowledge, error) {
	t.Helper()
	return h.replaceNamed(t, content, "upload.md", customFileName, metadata)
}

func (h *replaceFileHarness) replaceNamed(
	t *testing.T, content, filename, customFileName string, metadata map[string]string,
) (*types.Knowledge, error) {
	t.Helper()
	fh, err := bytesToFileHeader([]byte(content), filename)
	require.NoError(t, err)
	return h.svc.ReplaceKnowledgeFile(h.ctx, h.original.ID, fh, customFileName, metadata)
}

func md5Hex(content string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(content)))
}

func TestReplaceKnowledgeFilePreservesIDAndReparsesNewContent(t *testing.T) {
	h := newReplaceFileHarness(t)
	content := "# new body"

	got, err := h.replace(t, content, "notes/b.md",
		map[string]string{"source_updated_at": "2026-09-14T00:00:00Z"})

	require.NoError(t, err)
	require.Equal(t, h.original.ID, got.ID)
	row := h.repo.row
	assert.Equal(t, "new/file.md", row.FilePath)
	assert.Equal(t, md5Hex(content), row.FileHash)
	assert.Equal(t, int64(len(content)), row.FileSize)
	assert.Equal(t, "b.md", row.FileName)
	assert.Equal(t, "b.md", row.Title, "a title that mirrored the file name follows the rename")
	assert.Equal(t, "notes", row.FolderPath)
	assert.Equal(t, types.ParseStatusPending, row.ParseStatus)

	metadata, err := row.Metadata.Map()
	require.NoError(t, err)
	assert.Equal(t, "notes/a.md", metadata["external_id"])
	assert.Equal(t, "2026-09-14T00:00:00Z", metadata["source_updated_at"])
	assert.Equal(t, map[string]interface{}{"nested": true}, metadata["extra"], "unmanaged entries survive")

	require.Len(t, h.tasks.payloads, 1)
	assert.Equal(t, h.original.ID, h.tasks.payloads[0].KnowledgeID)
	assert.Equal(t, "new/file.md", h.tasks.payloads[0].FilePath)
	assert.Equal(t, []string{"save", "dequeue:knowledge-1", "enqueue", "delete:old/file.md"}, h.events,
		"queued parse tasks are dropped before reparse; the old file is deleted only after enqueue")
}

func TestReplaceKnowledgeFileUnchangedContentSkipsReparse(t *testing.T) {
	h := newReplaceFileHarness(t)

	got, err := h.replace(t, replaceFileOldContent, "notes/a.md", map[string]string{"source_updated_at": "later"})

	var dupErr *types.DuplicateKnowledgeError
	require.ErrorAs(t, err, &dupErr)
	require.Equal(t, h.original.ID, got.ID)
	assert.Equal(t, h.original.ID, dupErr.Knowledge.ID)
	assert.Zero(t, h.store.saved)
	assert.Empty(t, h.tasks.payloads)
	assert.Empty(t, h.store.deleted)
	assert.Equal(t, h.original.FilePath, h.repo.row.FilePath)
	assert.Equal(t, "later", h.repo.row.GetMetadata()["source_updated_at"], "changed metadata is still persisted")
}

func TestReplaceKnowledgeFileSaveFailureLeavesKnowledgeUntouched(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.store.saveErr = errors.New("storage unavailable")

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.ErrorIs(t, err, h.store.saveErr)
	assert.Equal(t, h.original, h.repo.row)
	assert.Empty(t, h.store.deleted)
	assert.Empty(t, h.tasks.payloads)
}

func TestReplaceKnowledgeFileUncertainSourceUpdateRetainsNewFile(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.failColumnsCall = 1

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.Error(t, err)
	assert.Equal(t, h.original, h.repo.row)
	assert.Empty(t, h.store.deleted, "an uncertain commit may have made this blob another replacement's backup")
	assert.Empty(t, h.tasks.payloads)
}

func TestReplaceKnowledgeFileCommittedUpdateReportedAsFailedContinuesReparse(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.failColumnsCall = 1
	h.repo.commitThenFail = true

	got, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.NoError(t, err)
	require.Equal(t, h.original.ID, got.ID)
	assert.Equal(t, "new/file.md", h.repo.row.FilePath)
	assert.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
	assert.Equal(t, []string{"old/file.md"}, h.store.deleted)
	require.Len(t, h.tasks.payloads, 1)
}

func TestReplaceKnowledgeFileReparseFailureRestoresPreviousSource(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.tasks.err = errors.New("queue unavailable")

	_, err := h.replace(t, "# new body", "notes/b.md", map[string]string{"source_updated_at": "later"})

	require.Error(t, err)
	row := h.repo.row
	assert.Equal(t, h.original.ID, row.ID)
	assert.Equal(t, h.original.FilePath, row.FilePath)
	assert.Equal(t, h.original.FileHash, row.FileHash)
	assert.Equal(t, h.original.FileSize, row.FileSize)
	assert.Equal(t, h.original.FileName, row.FileName)
	assert.Equal(t, h.original.Title, row.Title)
	assert.Equal(t, h.original.FolderPath, row.FolderPath)
	fields, mapErr := row.Metadata.Map()
	require.NoError(t, mapErr)
	require.NotEmpty(t, fields[types.KnowledgeReparseMetadataKey], "server attempt survives source compensation")
	delete(fields, types.KnowledgeReparseMetadataKey)
	encoded, marshalErr := json.Marshal(fields)
	require.NoError(t, marshalErr)
	assert.JSONEq(t, string(h.original.Metadata), string(encoded))
	assert.Equal(t, types.ParseStatusFailed, row.ParseStatus, "the old index may already be cleaned up")
	assert.Equal(t, []string{"new/file.md"}, h.store.deleted)
}

func TestReplaceKnowledgeFileRestoreFailureKeepsBothFiles(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.tasks.err = errors.New("queue unavailable")
	h.repo.failColumnsCall = 2 // the restore write

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.Error(t, err)
	assert.Equal(t, "new/file.md", h.repo.row.FilePath)
	assert.Empty(t, h.store.deleted)
}

func TestReplaceKnowledgeFileRejectsNonFileKnowledge(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.row.Type = types.KnowledgeTypeManual

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.Error(t, err)
	assert.Zero(t, h.store.saved)
}

func TestReplaceKnowledgeFileRejectsUnsupportedFileType(t *testing.T) {
	h := newReplaceFileHarness(t)

	_, err := h.replace(t, "MZ", "notes/tool.exe", nil)

	require.Error(t, err)
	assert.Zero(t, h.store.saved)
	assert.Equal(t, h.original, h.repo.row)
}

func TestReplaceKnowledgeFileEmptyCustomNameKeepsFolder(t *testing.T) {
	h := newReplaceFileHarness(t)

	_, err := h.replaceNamed(t, "# new body", "a.md", "", nil)

	require.NoError(t, err)
	assert.Equal(t, "notes", h.repo.row.FolderPath)
	assert.Equal(t, "a.md", h.repo.row.FileName)
}

func TestReplaceKnowledgeFileBareCustomNameKeepsFolder(t *testing.T) {
	h := newReplaceFileHarness(t)

	_, err := h.replace(t, "# new body", "b.md", nil)

	require.NoError(t, err)
	assert.Equal(t, "notes", h.repo.row.FolderPath, "a basename must not move the document to the KB root")
	assert.Equal(t, "b.md", h.repo.row.FileName)
}

func TestReplaceKnowledgeFileIgnoresStorageQuota(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.ctx = context.WithValue(h.ctx, types.TenantInfoContextKey, &types.Tenant{
		ID: 7, StorageQuota: 1, StorageUsed: 1,
	})

	got, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.NoError(t, err)
	require.Equal(t, h.original.ID, got.ID)
	assert.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
}

func TestReplaceKnowledgeFileRejectsInProgressParse(t *testing.T) {
	for _, status := range []string{
		types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing,
	} {
		h := newReplaceFileHarness(t)
		h.repo.row.ParseStatus = status
		_, err := h.replace(t, "# new body", "notes/a.md", nil)
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, 409, appErr.HTTPCode)
		require.Empty(t, h.events, "busy source must not save blobs or cancel another attempt")
	}
}

func TestReplaceKnowledgeFileRejectsFAQKnowledgeBase(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.svc.kbService = &reparseFailureKBService{kb: &types.KnowledgeBase{
		ID: "kb-1", TenantID: 7, Type: types.KnowledgeBaseTypeFAQ,
	}}

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.Error(t, err)
	assert.Zero(t, h.store.saved)
}

func TestIsKnowledgeSourceReplaced(t *testing.T) {
	h := newReplaceFileHarness(t)
	loaded := h.original
	assert.False(t, h.svc.isKnowledgeSourceReplaced(h.ctx, &loaded))

	h.repo.row.FilePath = "new/file.md"
	assert.True(t, h.svc.isKnowledgeSourceReplaced(h.ctx, &loaded))
}

func TestUpdateKnowledgeUnlessSourceReplacedSkipsStaleSave(t *testing.T) {
	h := newReplaceFileHarness(t)
	stale := h.original
	stale.ParseStatus = types.ParseStatusFailed
	h.repo.row.FilePath = "new/file.md"

	require.NoError(t, h.svc.updateKnowledgeUnlessSourceReplaced(h.ctx, &stale))
	assert.Equal(t, "new/file.md", h.repo.row.FilePath)
	assert.NotEqual(t, types.ParseStatusFailed, h.repo.row.ParseStatus)
}

func TestReplaceKnowledgeFileRejectsImplicitFolderMove(t *testing.T) {
	h := newReplaceFileHarness(t)
	_, err := h.replace(t, "# new body", "elsewhere/b.md", nil)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 400, appErr.HTTPCode)
	require.Zero(t, h.store.saved)
	require.Zero(t, h.repo.columnsCalls)
	require.Equal(t, h.original, h.repo.row)
}

func TestReplaceKnowledgeFilePreservesRenameDuringSave(t *testing.T) {
	for _, name := range []string{"b.md", "notes/b.md"} {
		t.Run(name, func(t *testing.T) {
			h := newReplaceFileHarness(t)
			h.store.beforeSaveReturn = func() { h.repo.row.FolderPath = "renamed/child" }
			got, err := h.replace(t, "# new body", name, nil)
			require.NoError(t, err)
			require.Equal(t, "renamed/child", got.FolderPath)
			require.Equal(t, "renamed/child", h.repo.row.FolderPath)
			require.Equal(t, "new/file.md", h.repo.row.FilePath)
		})
	}
}

func TestReplaceKnowledgeFileCompensationPreservesLaterFolder(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.tasks.err = errors.New("queue unavailable")
	h.repo.beforeSourceUpdate = func(call int) {
		if call == 2 {
			h.repo.row.FolderPath = "later/location"
		}
	}
	_, err := h.replace(t, "# new body", "notes/b.md", nil)
	require.Error(t, err)
	require.Equal(t, "later/location", h.repo.row.FolderPath)
	require.Equal(t, h.original.FilePath, h.repo.row.FilePath)
	require.Equal(t, []string{"new/file.md"}, h.store.deleted)
}

func TestReplaceKnowledgeFileCompensationCannotOverwriteNewerSource(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.tasks.err = errors.New("queue unavailable")
	h.repo.beforeSourceUpdate = func(call int) {
		if call == 2 {
			h.repo.row.FilePath = "later/file.md"
			h.repo.row.FileHash = "later-hash"
			h.repo.row.FolderPath = "later/location"
		}
	}
	_, err := h.replace(t, "# new body", "b.md", nil)
	require.Error(t, err)
	require.Equal(t, "later/file.md", h.repo.row.FilePath)
	require.Equal(t, "later-hash", h.repo.row.FileHash)
	require.Equal(t, "later/location", h.repo.row.FolderPath)
	require.Empty(t, h.store.deleted, "the newer replacement may need new/file.md to roll back")
	// The newer replacement can subsequently roll back to this attempt's
	// source. It must still exist, even though it was not the current row path
	// when the older compensation failed.
	later := h.repo.row
	h.repo.beforeSourceUpdate = nil
	_, restoreErr := h.repo.UpdateKnowledgeSourceIfCurrent(h.ctx, &later, map[string]interface{}{
		"file_path": "new/file.md", "file_hash": md5Hex("# new body"),
	})
	require.NoError(t, restoreErr)
	require.Equal(t, "new/file.md", h.repo.row.FilePath)
	require.NotContains(t, h.store.deleted, "new/file.md")
}

func TestReplaceKnowledgeFileUncertainCommitRetainsNewerReplacementBackup(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.failColumnsCall = 1
	h.repo.commitThenFail = true
	h.repo.afterFailedCommit = func() {
		h.repo.row.FilePath = "later/file.md"
		h.repo.row.FileHash = "later-hash"
	}
	_, err := h.replace(t, "# new body", "b.md", nil)
	require.Error(t, err)
	require.Equal(t, "later/file.md", h.repo.row.FilePath)
	require.Empty(t, h.store.deleted, "uncertain commit may have made new/file.md the newer attempt's backup")
	require.Empty(t, h.tasks.payloads)
}

func TestReplaceKnowledgeFileRejectsCrossKBChangeDuringSave(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.store.beforeSaveReturn = func() { h.repo.row.KnowledgeBaseID = "other-kb" }
	_, err := h.replace(t, "# new body", "b.md", nil)
	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, 409, appErr.HTTPCode)
	require.Equal(t, "other-kb", h.repo.row.KnowledgeBaseID)
	require.Equal(t, h.original.FilePath, h.repo.row.FilePath)
	require.Equal(t, []string{"save", "delete:new/file.md"}, h.events)
	require.Empty(t, h.tasks.payloads)
}

func TestReplaceKnowledgeFileUnknownCommitDoesNotContinueInNewKB(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.failColumnsCall = 1
	h.repo.commitThenFail = true
	h.repo.afterFailedCommit = func() { h.repo.row.KnowledgeBaseID = "other-kb" }
	_, err := h.replace(t, "# new body", "b.md", nil)
	require.Error(t, err)
	require.Equal(t, "other-kb", h.repo.row.KnowledgeBaseID)
	require.Equal(t, "new/file.md", h.repo.row.FilePath)
	require.Equal(t, []string{"save"}, h.events, "referenced blob retained; no new-KB tasks cancelled")
	require.Empty(t, h.tasks.payloads)
}

func TestReplacementMetadataCannotSetReparseClaim(t *testing.T) {
	metadata, err := mergeKnowledgeMetadata(
		types.JSON(`{"_knowledge_reparse_claim":"server-token"}`),
		map[string]string{types.KnowledgeReparseMetadataKey: "user-token", "user": "value"},
	)
	require.NoError(t, err)
	fields, err := metadata.Map()
	require.NoError(t, err)
	require.Equal(t, "server-token", fields[types.KnowledgeReparseMetadataKey])
	require.Equal(t, "value", fields["user"])
}
