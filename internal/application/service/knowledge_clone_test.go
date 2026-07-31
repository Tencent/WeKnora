package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const (
	phase30CloneTenantID   uint64 = 1
	phase30CloneSourceKBID        = "clone-source-kb"
	phase30CloneTargetKBID        = "clone-target-kb"
)

type phase30CloneKnowledgeRepo struct {
	interfaces.KnowledgeRepository

	delegate              interfaces.KnowledgeRepository
	beforeCreate          func(context.Context, *types.Knowledge) error
	createErrOverride     error
	updateErr             error
	createCalls           int
	updateCalls           int
	createSnapshots       []*types.Knowledge
	updateSnapshots       []*types.Knowledge
	lastCreateDelegateErr error
}

func (r *phase30CloneKnowledgeRepo) CreateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	r.createCalls++
	r.createSnapshots = append(r.createSnapshots, phase30CloneKnowledgeSnapshot(knowledge))
	if r.beforeCreate != nil {
		if err := r.beforeCreate(ctx, knowledge); err != nil {
			return err
		}
	}
	err := r.delegate.CreateKnowledge(ctx, knowledge)
	r.lastCreateDelegateErr = err
	if err != nil && r.createErrOverride != nil {
		return r.createErrOverride
	}
	return err
}

func (r *phase30CloneKnowledgeRepo) UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error {
	r.updateCalls++
	r.updateSnapshots = append(r.updateSnapshots, phase30CloneKnowledgeSnapshot(knowledge))
	if r.updateErr != nil {
		return r.updateErr
	}
	return r.delegate.UpdateKnowledge(ctx, knowledge)
}

func phase30CloneKnowledgeSnapshot(knowledge *types.Knowledge) *types.Knowledge {
	if knowledge == nil {
		return nil
	}
	snapshot := *knowledge
	snapshot.Tags = make([]*types.KnowledgeTag, len(knowledge.Tags))
	for i, tag := range knowledge.Tags {
		if tag != nil {
			tagSnapshot := *tag
			snapshot.Tags[i] = &tagSnapshot
		}
	}
	snapshot.Metadata = append(types.JSON(nil), knowledge.Metadata...)
	snapshot.LastFAQImportResult = append(types.JSON(nil), knowledge.LastFAQImportResult...)
	if knowledge.ProcessedAt != nil {
		processedAt := *knowledge.ProcessedAt
		snapshot.ProcessedAt = &processedAt
	}
	return &snapshot
}

type phase30CloneFileService struct {
	interfaces.FileService

	copyErr     error
	deleteErr   error
	copyCalls   int
	copiedPaths []string
	deleted     []string
}

func (s *phase30CloneFileService) CopyFile(
	_ context.Context,
	_ string,
	tenantID uint64,
	knowledgeID string,
) (string, error) {
	s.copyCalls++
	if s.copyErr != nil {
		return "", s.copyErr
	}
	path := fmt.Sprintf("local://clone-test/%d/%s/object-%d.bin", tenantID, knowledgeID, s.copyCalls)
	s.copiedPaths = append(s.copiedPaths, path)
	return path, nil
}

func (s *phase30CloneFileService) DeleteFile(_ context.Context, path string) error {
	s.deleted = append(s.deleted, path)
	return s.deleteErr
}

type phase30CloneKBService struct {
	interfaces.KnowledgeBaseService

	byID map[string]*types.KnowledgeBase
}

func (s *phase30CloneKBService) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	kb, ok := s.byID[id]
	if !ok {
		return nil, fmt.Errorf("knowledge base %s not found", id)
	}
	return kb, nil
}

type phase30CloneTenantRepo struct {
	interfaces.TenantRepository

	adjustErr   error
	adjustCalls int
}

func (r *phase30CloneTenantRepo) AdjustStorageUsed(
	_ context.Context,
	_ uint64,
	_ int64,
) error {
	r.adjustCalls++
	return r.adjustErr
}

type phase30CloneChunkRepo struct {
	interfaces.ChunkRepository

	sourceChunks []*types.Chunk
	listErr      error
	createErr    error
	listCalls    int
	createCalls  int
}

func (r *phase30CloneChunkRepo) ListPagedChunksByKnowledgeID(
	_ context.Context,
	_ uint64,
	_ string,
	page *types.Pagination,
	_ []types.ChunkType,
	_ string,
	_ string,
	_ string,
	_ string,
	_ string,
) ([]*types.Chunk, int64, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	if page.Page != 1 || len(r.sourceChunks) == 0 {
		return nil, int64(len(r.sourceChunks)), nil
	}
	return r.sourceChunks, int64(len(r.sourceChunks)), nil
}

func (r *phase30CloneChunkRepo) CreateChunks(_ context.Context, _ []*types.Chunk) error {
	r.createCalls++
	return r.createErr
}

type phase30CloneRetrieveEngine struct {
	interfaces.RetrieveEngineService

	copyCalls int
	copyErr   error
}

func (e *phase30CloneRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (e *phase30CloneRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *phase30CloneRetrieveEngine) CopyIndices(
	_ context.Context,
	_ string,
	_ map[string]string,
	_ map[string]string,
	_ string,
	_ int,
	_ string,
) error {
	e.copyCalls++
	return e.copyErr
}

type phase30CloneRetrieveRegistry struct {
	interfaces.RetrieveEngineRegistry

	engine interfaces.RetrieveEngineService
}

func (r *phase30CloneRetrieveRegistry) GetByStoreID(string) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type phase30CloneStoreOwnership struct{}

func (phase30CloneStoreOwnership) StoreOwnedBy(context.Context, string, uint64) (bool, error) {
	return true, nil
}

type phase30CloneEmbedder struct {
	embedding.Embedder
}

func (*phase30CloneEmbedder) GetDimensions() int { return 3 }

type phase30CloneModelService struct {
	interfaces.ModelService

	embedder embedding.Embedder
	err      error
}

func (s *phase30CloneModelService) GetEmbeddingModel(
	context.Context,
	string,
) (embedding.Embedder, error) {
	return s.embedder, s.err
}

func phase30CloneTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.New().String() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func phase30CloneContext() context.Context {
	tenant := &types.Tenant{ID: phase30CloneTenantID}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenant)
	return context.WithValue(ctx, types.TenantIDContextKey, phase30CloneTenantID)
}

func phase30CloneFixtures(filePath string) (*types.Knowledge, *types.KnowledgeBase, *types.KnowledgeBase) {
	storeID := "clone-test-store"
	sourceKB := &types.KnowledgeBase{
		ID:               phase30CloneSourceKBID,
		TenantID:         phase30CloneTenantID,
		VectorStoreID:    &storeID,
		EmbeddingModelID: "source-embedding-model",
	}
	targetKB := &types.KnowledgeBase{
		ID:               phase30CloneTargetKBID,
		TenantID:         phase30CloneTenantID,
		VectorStoreID:    &storeID,
		EmbeddingModelID: "target-embedding-model",
	}
	source := &types.Knowledge{
		ID:              "clone-source-knowledge",
		TenantID:        phase30CloneTenantID,
		KnowledgeBaseID: sourceKB.ID,
		Type:            "file",
		Channel:         types.ChannelWeb,
		Title:           "clone source title",
		Description:     "clone source description",
		Source:          "upload",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
		FileName:        "source.bin",
		FileType:        "bin",
		FileSize:        64,
		FileHash:        "clone-source-hash",
		FilePath:        filePath,
		StorageSize:     64,
	}
	return source, sourceKB, targetKB
}

func phase30CloneKBStub(
	sourceKB *types.KnowledgeBase,
	targetKB *types.KnowledgeBase,
) *phase30CloneKBService {
	return &phase30CloneKBService{byID: map[string]*types.KnowledgeBase{
		sourceKB.ID: sourceKB,
		targetKB.ID: targetKB,
	}}
}

func phase30CloneKnowledgeCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&types.Knowledge{}).Count(&count).Error)
	return count
}

func TestCloneKnowledge_CreateFailureDoesNotUpdateOrPersist(t *testing.T) {
	db := phase30CloneTestDB(t)
	require.NoError(t, db.Exec(`
		CREATE TRIGGER reject_processing_clone
		BEFORE INSERT ON knowledges
		WHEN NEW.title = 'force clone create failure' AND NEW.parse_status = 'processing'
		BEGIN
			SELECT RAISE(ABORT, 'forced clone create failure');
		END;
	`).Error)

	createErr := errors.New("phase 3.0 create sentinel")
	repoSpy := &phase30CloneKnowledgeRepo{
		delegate:          repository.NewKnowledgeRepository(db),
		createErrOverride: createErr,
	}
	fileSvc := &phase30CloneFileService{}
	source, sourceKB, targetKB := phase30CloneFixtures("local://source/document.bin")
	source.Title = "force clone create failure"
	svc := &knowledgeService{
		repo:      repoSpy,
		kbService: phase30CloneKBStub(sourceKB, targetKB),
		fileSvc:   fileSvc,
	}

	err := svc.cloneKnowledge(phase30CloneContext(), source, targetKB)

	require.ErrorIs(t, err, createErr)
	require.Error(t, repoSpy.lastCreateDelegateErr)
	require.Equal(t, 1, repoSpy.createCalls)
	require.Equal(t, 0, repoSpy.updateCalls)
	require.Len(t, fileSvc.copiedPaths, 1)
	require.Equal(t, fileSvc.copiedPaths, fileSvc.deleted)
	require.Equal(t, int64(0), phase30CloneKnowledgeCount(t, db))
	require.Equal(t, types.ParseStatusProcessing, repoSpy.createSnapshots[0].ParseStatus)
}

func TestCloneKnowledge_DownstreamFailureMarksPersistedKnowledgeFailed(t *testing.T) {
	db := phase30CloneTestDB(t)
	downstreamErr := errors.New("phase 3.0 downstream sentinel")
	repoSpy := &phase30CloneKnowledgeRepo{delegate: repository.NewKnowledgeRepository(db)}
	fileSvc := &phase30CloneFileService{}
	tenantRepo := &phase30CloneTenantRepo{adjustErr: downstreamErr}
	source, sourceKB, targetKB := phase30CloneFixtures("local://source/document.bin")
	svc := &knowledgeService{
		repo:       repoSpy,
		kbService:  phase30CloneKBStub(sourceKB, targetKB),
		tenantRepo: tenantRepo,
		fileSvc:    fileSvc,
	}

	err := svc.cloneKnowledge(phase30CloneContext(), source, targetKB)

	require.ErrorIs(t, err, downstreamErr)
	require.Equal(t, 1, repoSpy.createCalls)
	require.Equal(t, 1, repoSpy.updateCalls)
	require.Equal(t, 1, tenantRepo.adjustCalls)
	require.Equal(t, types.ParseStatusProcessing, repoSpy.createSnapshots[0].ParseStatus)
	require.Equal(t, types.ParseStatusFailed, repoSpy.updateSnapshots[0].ParseStatus)
	require.Equal(t, downstreamErr.Error(), repoSpy.updateSnapshots[0].ErrorMessage)
	require.Len(t, fileSvc.copiedPaths, 1)
	require.Equal(t, fileSvc.copiedPaths, fileSvc.deleted)
	require.Equal(t, int64(1), phase30CloneKnowledgeCount(t, db))

	var persisted types.Knowledge
	require.NoError(t, db.First(&persisted, "id = ?", repoSpy.createSnapshots[0].ID).Error)
	require.Equal(t, types.ParseStatusFailed, persisted.ParseStatus)
	require.Equal(t, downstreamErr.Error(), persisted.ErrorMessage)
}

func TestCloneKnowledge_CopyFailureBeforeDeferDoesNotPersist(t *testing.T) {
	db := phase30CloneTestDB(t)
	copyErr := errors.New("phase 3.0 copy sentinel")
	repoSpy := &phase30CloneKnowledgeRepo{delegate: repository.NewKnowledgeRepository(db)}
	fileSvc := &phase30CloneFileService{copyErr: copyErr}
	source, sourceKB, targetKB := phase30CloneFixtures("local://source/document.bin")
	svc := &knowledgeService{
		repo:      repoSpy,
		kbService: phase30CloneKBStub(sourceKB, targetKB),
		fileSvc:   fileSvc,
	}

	err := svc.cloneKnowledge(phase30CloneContext(), source, targetKB)

	require.ErrorIs(t, err, copyErr)
	require.Equal(t, 1, fileSvc.copyCalls)
	require.Empty(t, fileSvc.deleted)
	require.Equal(t, 0, repoSpy.createCalls)
	require.Equal(t, 0, repoSpy.updateCalls)
	require.Equal(t, int64(0), phase30CloneKnowledgeCount(t, db))
}

func TestCloneKnowledge_SuccessKeepsExistingCompletionBehavior(t *testing.T) {
	db := phase30CloneTestDB(t)
	repoSpy := &phase30CloneKnowledgeRepo{delegate: repository.NewKnowledgeRepository(db)}
	fileSvc := &phase30CloneFileService{}
	tenantRepo := &phase30CloneTenantRepo{}
	chunkRepo := &phase30CloneChunkRepo{}
	engine := &phase30CloneRetrieveEngine{}
	source, sourceKB, targetKB := phase30CloneFixtures("local://source/document.bin")
	svc := &knowledgeService{
		repo:           repoSpy,
		kbService:      phase30CloneKBStub(sourceKB, targetKB),
		tenantRepo:     tenantRepo,
		chunkRepo:      chunkRepo,
		fileSvc:        fileSvc,
		modelService:   &phase30CloneModelService{embedder: &phase30CloneEmbedder{}},
		retrieveEngine: &phase30CloneRetrieveRegistry{engine: engine},
		ownership:      phase30CloneStoreOwnership{},
	}

	err := svc.cloneKnowledge(phase30CloneContext(), source, targetKB)

	require.NoError(t, err)
	require.Equal(t, 1, repoSpy.createCalls)
	require.Equal(t, 1, repoSpy.updateCalls)
	require.Equal(t, types.ParseStatusCompleted, repoSpy.updateSnapshots[0].ParseStatus)
	require.Equal(t, "enabled", repoSpy.updateSnapshots[0].EnableStatus)
	require.Equal(t, 1, tenantRepo.adjustCalls)
	require.Equal(t, 1, chunkRepo.listCalls)
	require.Equal(t, 1, engine.copyCalls)
	require.Len(t, fileSvc.copiedPaths, 1)
	require.Empty(t, fileSvc.deleted)
	require.Equal(t, int64(1), phase30CloneKnowledgeCount(t, db))

	var persisted types.Knowledge
	require.NoError(t, db.First(&persisted, "id = ?", repoSpy.createSnapshots[0].ID).Error)
	require.Equal(t, types.ParseStatusCompleted, persisted.ParseStatus)
	require.Equal(t, "enabled", persisted.EnableStatus)
}

func TestCloneKnowledge_CreateConflictDoesNotUpdateExistingKnowledge(t *testing.T) {
	db := phase30CloneTestDB(t)
	conflictErr := errors.New("phase 3.0 create conflict sentinel")
	realRepo := repository.NewKnowledgeRepository(db)
	existing := &types.Knowledge{
		TenantID:        99,
		KnowledgeBaseID: "existing-kb",
		Type:            "file",
		Title:           "existing title",
		ParseStatus:     types.ParseStatusCompleted,
		EnableStatus:    "enabled",
	}
	repoSpy := &phase30CloneKnowledgeRepo{
		delegate:          realRepo,
		createErrOverride: conflictErr,
		beforeCreate: func(ctx context.Context, knowledge *types.Knowledge) error {
			existing.ID = knowledge.ID
			return realRepo.CreateKnowledge(ctx, existing)
		},
	}
	source, _, targetKB := phase30CloneFixtures("")
	source.Title = "replacement title"
	svc := &knowledgeService{repo: repoSpy}

	err := svc.cloneKnowledge(phase30CloneContext(), source, targetKB)

	require.ErrorIs(t, err, conflictErr)
	require.Error(t, repoSpy.lastCreateDelegateErr)
	require.Equal(t, 1, repoSpy.createCalls)
	require.Equal(t, 0, repoSpy.updateCalls)
	require.Equal(t, int64(1), phase30CloneKnowledgeCount(t, db))

	var persisted types.Knowledge
	require.NoError(t, db.First(&persisted, "id = ?", existing.ID).Error)
	require.Equal(t, existing.TenantID, persisted.TenantID)
	require.Equal(t, existing.KnowledgeBaseID, persisted.KnowledgeBaseID)
	require.Equal(t, existing.Title, persisted.Title)
	require.Equal(t, existing.ParseStatus, persisted.ParseStatus)
}

func TestCloneKnowledge_UpdateFailureDoesNotOverrideDownstreamError(t *testing.T) {
	db := phase30CloneTestDB(t)
	downstreamErr := errors.New("phase 3.0 downstream error")
	updateErr := errors.New("phase 3.0 update error")
	repoSpy := &phase30CloneKnowledgeRepo{
		delegate:  repository.NewKnowledgeRepository(db),
		updateErr: updateErr,
	}
	fileSvc := &phase30CloneFileService{}
	tenantRepo := &phase30CloneTenantRepo{adjustErr: downstreamErr}
	source, sourceKB, targetKB := phase30CloneFixtures("local://source/document.bin")
	svc := &knowledgeService{
		repo:       repoSpy,
		kbService:  phase30CloneKBStub(sourceKB, targetKB),
		tenantRepo: tenantRepo,
		fileSvc:    fileSvc,
	}

	err := svc.cloneKnowledge(phase30CloneContext(), source, targetKB)

	require.ErrorIs(t, err, downstreamErr)
	require.NotErrorIs(t, err, updateErr)
	require.Equal(t, 1, repoSpy.createCalls)
	require.Equal(t, 1, repoSpy.updateCalls)
	require.Equal(t, types.ParseStatusFailed, repoSpy.updateSnapshots[0].ParseStatus)
	require.Len(t, fileSvc.deleted, 1)
	require.Equal(t, int64(1), phase30CloneKnowledgeCount(t, db))

	var persisted types.Knowledge
	require.NoError(t, db.First(&persisted, "id = ?", repoSpy.createSnapshots[0].ID).Error)
	require.Equal(t, types.ParseStatusProcessing, persisted.ParseStatus)
}
