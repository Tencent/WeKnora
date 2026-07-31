package service

import (
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessSyncCancelsWhenKnowledgeBaseDeleted(t *testing.T) {
	ds := &types.DataSource{
		ID:              "ds-1",
		TenantID:        1,
		KnowledgeBaseID: "kb-deleted",
		Type:            types.ConnectorTypeRSS,
		Status:          types.DataSourceStatusActive,
	}
	dsRepo := newKBDeleteDSRepo("kb-deleted", ds)
	syncLog := &types.SyncLog{
		ID:           "log-1",
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		Status:       types.SyncLogStatusRunning,
		StartedAt:    time.Now().UTC(),
	}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}

	svc := &DataSourceService{
		dsRepo:      dsRepo,
		syncLogRepo: syncLogRepo,
		kbService:   &processSyncKBService{getErr: apprepo.ErrKnowledgeBaseNotFound},
	}

	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: ds.ID,
		TenantID:     ds.TenantID,
		SyncLogID:    syncLog.ID,
	})
	require.NoError(t, err)

	err = svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	require.NoError(t, err)

	updated := syncLogRepo.logs[syncLog.ID]
	require.NotNil(t, updated)
	assert.Equal(t, types.SyncLogStatusCanceled, updated.Status)
	assert.Equal(t, "knowledge base has been deleted", updated.ErrorMessage)
	require.NotNil(t, updated.FinishedAt)
}

type processSyncKBService struct {
	getErr error
}

func (s *processSyncKBService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, s.getErr
}
func (s *processSyncKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, s.getErr
}
func (s *processSyncKBService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) FillKnowledgeBaseCounts(context.Context, *types.KnowledgeBase) error {
	return nil
}
func (s *processSyncKBService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) UpdateKnowledgeBase(
	context.Context, string, string, string, *types.KnowledgeBaseConfig,
) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) DeleteKnowledgeBase(context.Context, string) error { return nil }
func (s *processSyncKBService) TogglePinKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, nil
}
func (s *processSyncKBService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return nil, nil
}
func (s *processSyncKBService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}
func (s *processSyncKBService) CopyKnowledgeBase(context.Context, string, string) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, nil
}
func (s *processSyncKBService) DuplicateKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, nil
}
func (s *processSyncKBService) GetRepository() interfaces.KnowledgeBaseRepository { return nil }
func (s *processSyncKBService) ProcessKBDelete(context.Context, *asynq.Task) error {
	return nil
}

var _ interfaces.KnowledgeBaseService = (*processSyncKBService)(nil)

type processSyncSyncLogRepo struct {
	logs map[string]*types.SyncLog
}

func (r *processSyncSyncLogRepo) Create(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}
func (r *processSyncSyncLogRepo) FindByID(_ context.Context, id string) (*types.SyncLog, error) {
	log, ok := r.logs[id]
	if !ok {
		return nil, errors.New("sync log not found")
	}
	return log, nil
}
func (r *processSyncSyncLogRepo) FindByDataSource(context.Context, string, int, int) ([]*types.SyncLog, error) {
	return nil, nil
}
func (r *processSyncSyncLogRepo) FindLatest(context.Context, string) (*types.SyncLog, error) {
	return nil, nil
}
func (r *processSyncSyncLogRepo) HasRunningSync(context.Context, string) (bool, error) {
	return false, nil
}
func (r *processSyncSyncLogRepo) Update(_ context.Context, log *types.SyncLog) error {
	r.logs[log.ID] = log
	return nil
}
func (r *processSyncSyncLogRepo) UpdateResult(_ context.Context, log *types.SyncLog) error {
	return r.Update(context.Background(), log)
}
func (r *processSyncSyncLogRepo) CancelPendingByDataSource(context.Context, string) error {
	return nil
}
func (r *processSyncSyncLogRepo) CleanupOldLogs(context.Context, int) error { return nil }

func TestAllFetchedItemsFailedError(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  2,
		Failed: 2,
		Errors: []types.SyncItemError{{Message: "doc one: export failed"}},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "all fetched items failed during sync (2/2)")
	assert.Contains(t, err.Error(), "doc one: export failed")
}

func TestAllFetchedItemsFailedErrorIgnoresPartialFailure(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Created: 1,
		Failed:  2,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorIgnoresSkippedItems(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:   3,
		Skipped: 3,
	})
	require.NoError(t, err)
}

func TestAllFetchedItemsFailedErrorTruncatesLongDetail(t *testing.T) {
	err := allFetchedItemsFailedError(&types.SyncResult{
		Total:  1,
		Failed: 1,
		Errors: []types.SyncItemError{{Message: strings.Repeat("x", 600)}},
	})
	require.Error(t, err)
	assert.LessOrEqual(t, len(err.Error()), 560)
	assert.Contains(t, err.Error(), "...")
}

type datasourceFolderKnowledgeRepository struct {
	interfaces.KnowledgeRepository
	existing *types.Knowledge
}

func (r *datasourceFolderKnowledgeRepository) FindByMetadataKey(
	context.Context,
	uint64,
	string,
	string,
	string,
) (*types.Knowledge, error) {
	return r.existing, nil
}

type datasourceFolderKnowledgeService struct {
	interfaces.KnowledgeService
	repo        interfaces.KnowledgeRepository
	fileFolders []string
	fileTagIDs  [][]string
	urlFolders  []string
	urlTagIDs   [][]string
	deleteCalls int
}

func (s *datasourceFolderKnowledgeService) GetRepository() interfaces.KnowledgeRepository {
	return s.repo
}

func (s *datasourceFolderKnowledgeService) DeleteKnowledge(context.Context, string) error {
	s.deleteCalls++
	return nil
}

func (s *datasourceFolderKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	tagIDs []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	s.fileFolders = append(s.fileFolders, folderID)
	s.fileTagIDs = append(s.fileTagIDs, append([]string(nil), tagIDs...))
	if folderID != types.DocumentFolderRootID {
		return nil, apprepo.ErrDocumentFolderNotFound
	}
	return &types.Knowledge{FolderID: folderID}, nil
}

func (s *datasourceFolderKnowledgeService) CreateKnowledgeFromURL(
	_ context.Context,
	_ string,
	_ string,
	_ string,
	_ string,
	_ *bool,
	_ string,
	tagIDs []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	s.urlFolders = append(s.urlFolders, folderID)
	s.urlTagIDs = append(s.urlTagIDs, append([]string(nil), tagIDs...))
	if folderID != types.DocumentFolderRootID {
		return nil, apprepo.ErrDocumentFolderNotFound
	}
	return &types.Knowledge{FolderID: folderID}, nil
}

func newDatasourceFolderRaceService() (*DataSourceService, *datasourceFolderKnowledgeService) {
	repo := &datasourceFolderKnowledgeRepository{existing: &types.Knowledge{
		ID:       "knowledge-old",
		FolderID: "folder-deleted",
	}}
	knowledgeService := &datasourceFolderKnowledgeService{repo: repo}
	return &DataSourceService{knowledgeService: knowledgeService}, knowledgeService
}

func TestIngestItemFileRetriesAtRootWhenPreservedFolderIsDeleted(t *testing.T) {
	svc, knowledgeService := newDatasourceFolderRaceService()
	ds := &types.DataSource{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "test",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "external-1",
		FileName:   "document.txt",
		Content:    []byte("content"),
	}, []string{"tag-1"})

	require.NoError(t, err)
	assert.True(t, isUpdate)
	assert.Equal(t, 1, knowledgeService.deleteCalls)
	assert.Equal(t, []string{"folder-deleted", types.DocumentFolderRootID}, knowledgeService.fileFolders)
	assert.Equal(t, [][]string{{"tag-1"}, {"tag-1"}}, knowledgeService.fileTagIDs)
}

func TestIngestItemURLRetriesAtRootWhenPreservedFolderIsDeleted(t *testing.T) {
	svc, knowledgeService := newDatasourceFolderRaceService()
	ds := &types.DataSource{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            "test",
	}

	isUpdate, err := svc.ingestItem(context.Background(), ds, &types.FetchedItem{
		ExternalID: "external-1",
		FileName:   "document.html",
		URL:        "https://example.com/document.html",
	}, []string{"tag-1"})

	require.NoError(t, err)
	assert.True(t, isUpdate)
	assert.Equal(t, 1, knowledgeService.deleteCalls)
	assert.Equal(t, []string{"folder-deleted", types.DocumentFolderRootID}, knowledgeService.urlFolders)
	assert.Equal(t, [][]string{{"tag-1"}, {"tag-1"}}, knowledgeService.urlTagIDs)
}
