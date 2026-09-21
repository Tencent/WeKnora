package service

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
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
	row             types.Knowledge
	columnsCalls    int
	failColumnsCall int
	commitThenFail  bool
}

func (r *replaceFileRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	row := r.row
	return &row, nil
}

func (r *replaceFileRepo) UpdateKnowledge(_ context.Context, knowledge *types.Knowledge) error {
	r.row = *knowledge
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
		}
	}
}

type replaceFileStore struct {
	interfaces.FileService
	saveErr error
	saved   int
	deleted []string
	events  *[]string
}

func (f *replaceFileStore) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	if f.saveErr != nil {
		return "", f.saveErr
	}
	f.saved++
	*f.events = append(*f.events, "save")
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

	got, err := h.replace(t, content, "notes/sub/b.md",
		map[string]string{"source_updated_at": "2026-09-14T00:00:00Z"})

	require.NoError(t, err)
	require.Equal(t, h.original.ID, got.ID)
	row := h.repo.row
	assert.Equal(t, "new/file.md", row.FilePath)
	assert.Equal(t, md5Hex(content), row.FileHash)
	assert.Equal(t, int64(len(content)), row.FileSize)
	assert.Equal(t, "b.md", row.FileName)
	assert.Equal(t, "b.md", row.Title, "a title that mirrored the file name follows the rename")
	assert.Equal(t, "notes/sub", row.FolderPath)
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

func TestReplaceKnowledgeFileSourceUpdateFailureDiscardsNewFile(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.failColumnsCall = 1

	_, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.Error(t, err)
	assert.Equal(t, h.original, h.repo.row)
	assert.Equal(t, []string{"new/file.md"}, h.store.deleted, "the old file must never be deleted")
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

	_, err := h.replace(t, "# new body", "notes/sub/b.md", map[string]string{"source_updated_at": "later"})

	require.Error(t, err)
	row := h.repo.row
	assert.Equal(t, h.original.ID, row.ID)
	assert.Equal(t, h.original.FilePath, row.FilePath)
	assert.Equal(t, h.original.FileHash, row.FileHash)
	assert.Equal(t, h.original.FileSize, row.FileSize)
	assert.Equal(t, h.original.FileName, row.FileName)
	assert.Equal(t, h.original.Title, row.Title)
	assert.Equal(t, h.original.FolderPath, row.FolderPath)
	assert.JSONEq(t, string(h.original.Metadata), string(row.Metadata))
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

func TestReplaceKnowledgeFileDequeuesInProgressParse(t *testing.T) {
	h := newReplaceFileHarness(t)
	h.repo.row.ParseStatus = types.ParseStatusProcessing

	got, err := h.replace(t, "# new body", "notes/a.md", nil)

	require.NoError(t, err)
	require.Equal(t, h.original.ID, got.ID)
	assert.Contains(t, h.events, "dequeue:knowledge-1")
	assert.Equal(t, types.ParseStatusPending, h.repo.row.ParseStatus)
	assert.Equal(t, "disabled", h.repo.row.EnableStatus)
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
