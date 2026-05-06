package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/textproto"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type stubKnowledgeBaseService struct {
	kb  *types.KnowledgeBase
	err error
}

func (s *stubKnowledgeBaseService) CreateKnowledgeBase(context.Context, *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.kb, nil
}

func (s *stubKnowledgeBaseService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) GetKnowledgeBasesByIDsOnly(context.Context, []string) ([]*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) FillKnowledgeBaseCounts(context.Context, *types.KnowledgeBase) error {
	return errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) ListKnowledgeBases(context.Context) ([]*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) ListKnowledgeBasesByTenantID(context.Context, uint64) ([]*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) UpdateKnowledgeBase(context.Context, string, string, string, *types.KnowledgeBaseConfig) (*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) DeleteKnowledgeBase(context.Context, string) error {
	return errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) TogglePinKnowledgeBase(context.Context, string) (*types.KnowledgeBase, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) HybridSearch(context.Context, string, types.SearchParams) ([]*types.SearchResult, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) GetQueryEmbedding(context.Context, string, string) ([]float32, error) {
	return nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) ResolveEmbeddingModelKeys(context.Context, []*types.KnowledgeBase) map[string]string {
	return nil
}

func (s *stubKnowledgeBaseService) CopyKnowledgeBase(context.Context, string, string) (*types.KnowledgeBase, *types.KnowledgeBase, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *stubKnowledgeBaseService) GetRepository() interfaces.KnowledgeBaseRepository {
	return nil
}

func (s *stubKnowledgeBaseService) ProcessKBDelete(context.Context, *asynq.Task) error {
	return errors.New("not implemented")
}

type fakeUploadFileService struct {
	savePath        string
	saveErr         error
	saveCalls       int
	lastKnowledgeID string
}

func (s *fakeUploadFileService) CheckConnectivity(context.Context) error { return nil }

func (s *fakeUploadFileService) SaveFile(ctx context.Context, file *multipart.FileHeader, tenantID uint64, knowledgeID string) (string, error) {
	s.saveCalls++
	s.lastKnowledgeID = knowledgeID
	if s.saveErr != nil {
		return "", s.saveErr
	}
	return s.savePath, nil
}

func (s *fakeUploadFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", errors.New("not implemented")
}

func (s *fakeUploadFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (s *fakeUploadFileService) GetFileURL(context.Context, string) (string, error) {
	return "", errors.New("not implemented")
}

func (s *fakeUploadFileService) DeleteFile(context.Context, string) error { return nil }

type fakeTaskEnqueuer struct{}

func (e *fakeTaskEnqueuer) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	return &asynq.TaskInfo{ID: "task-1", Queue: "default"}, nil
}

func setupKnowledgeUploadTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	return db
}

func newKnowledgeUploadService(t *testing.T, fileSvc interfaces.FileService) (*knowledgeService, *gorm.DB) {
	t.Helper()

	db := setupKnowledgeUploadTestDB(t)
	repo := repository.NewKnowledgeRepository(db)

	return &knowledgeService{
		repo: repo,
		kbService: &stubKnowledgeBaseService{kb: &types.KnowledgeBase{
			ID:               "kb-1",
			Type:             types.KnowledgeBaseTypeDocument,
			EmbeddingModelID: "embed-1",
			StorageProviderConfig: &types.StorageProviderConfig{
				Provider: "local",
			},
		}},
		fileSvc: fileSvc,
		task:    &fakeTaskEnqueuer{},
	}, db
}

func newKnowledgeUploadTestContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 1})
}

func newMultipartFileHeader(t *testing.T, filename string, data []byte) *multipart.FileHeader {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	header.Set("Content-Type", "application/octet-stream")

	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	reader := multipart.NewReader(&buf, writer.Boundary())
	form, err := reader.ReadForm(int64(len(data)) + 1024)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, form.RemoveAll())
	})

	files := form.File["file"]
	require.Len(t, files, 1)
	return files[0]
}

func knowledgeCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()

	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	return count
}

func loadKnowledge(t *testing.T, db *gorm.DB, id string) *types.Knowledge {
	t.Helper()

	var knowledge types.Knowledge
	require.NoError(t, db.First(&knowledge, "id = ?", id).Error)
	return &knowledge
}

func TestCreateKnowledgeFromFile_DoesNotPersistWhenUploadFails(t *testing.T) {
	fileSvc := &fakeUploadFileService{saveErr: errors.New("upload failed")}
	service, db := newKnowledgeUploadService(t, fileSvc)

	file := newMultipartFileHeader(t, "issue-1099.txt", []byte("content for failed upload"))
	knowledge, err := service.CreateKnowledgeFromFile(newKnowledgeUploadTestContext(), "kb-1", file, nil, nil, "", "", "")

	require.Nil(t, knowledge)
	require.EqualError(t, err, "upload failed")
	require.Equal(t, int64(0), knowledgeCount(t, db))
	require.Equal(t, 1, fileSvc.saveCalls)
	require.NotEmpty(t, fileSvc.lastKnowledgeID)
}

func TestCreateKnowledgeFromFile_PersistsAfterSuccessfulUpload(t *testing.T) {
	fileSvc := &fakeUploadFileService{savePath: "tenants/1/knowledge/issue-1099.txt"}
	service, db := newKnowledgeUploadService(t, fileSvc)

	file := newMultipartFileHeader(t, "issue-1099.txt", []byte("content for successful upload"))
	knowledge, err := service.CreateKnowledgeFromFile(newKnowledgeUploadTestContext(), "kb-1", file, nil, nil, "", "", "")

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Equal(t, int64(1), knowledgeCount(t, db))
	require.NotEmpty(t, knowledge.ID)
	require.Equal(t, knowledge.ID, fileSvc.lastKnowledgeID)
	require.Equal(t, "tenants/1/knowledge/issue-1099.txt", knowledge.FilePath)

	stored := loadKnowledge(t, db, knowledge.ID)
	require.Equal(t, knowledge.FilePath, stored.FilePath)
	require.Equal(t, "issue-1099.txt", stored.FileName)
	require.Equal(t, "pending", stored.ParseStatus)
}
