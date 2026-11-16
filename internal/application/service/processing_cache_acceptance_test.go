package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/processingcache/gormstore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type acceptanceKnowledgeRepo struct{ *inlineLifecycleKnowledgeRepo }

func (r *acceptanceKnowledgeRepo) UpdateKnowledge(_ context.Context, k *types.Knowledge) error {
	*r.knowledge = *k
	return nil
}

func (r *acceptanceKnowledgeRepo) UpdateKnowledgeColumn(
	_ context.Context,
	_ string,
	column string,
	value interface{},
) error {
	if column == "metadata" {
		r.knowledge.Metadata = value.(types.JSON)
	}
	return nil
}

type acceptanceChunks struct{ *inlineLifecycleChunkRepo }

func (r *acceptanceChunks) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	r.chunks = nil
	return nil
}

func (r *acceptanceChunks) ListImageInfoByKnowledgeIDs(
	context.Context,
	uint64,
	[]string,
) ([]interfaces.ChunkImageInfo, error) {
	return nil, nil
}

type acceptanceQueue struct {
	inlineLifecycleTaskEnqueuer
	documents []*asynq.Task
}

func (q *acceptanceQueue) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	if task.Type() == types.TypeDocumentProcess {
		q.documents = append(q.documents, task)
		return &asynq.TaskInfo{ID: "parse"}, nil
	}
	return q.inlineLifecycleTaskEnqueuer.Enqueue(task, opts...)
}

type acceptanceVLM struct {
	inlineLifecycleVLM
	identity string
}

func (m *acceptanceVLM) ProcessingCacheIdentity() string { return m.identity }

// The cache uses the shipped SQLite migration and an actual close/reopen. The
// knowledge, chunk, queue and object store boundaries deliberately remain fakes.
func TestProcessingCacheAcceptanceReparseQueuedParseAndImageAfterReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cache.sqlite")
	open := func() *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path+"?_foreign_keys=on"), &gorm.Config{})
		require.NoError(t, err)
		raw, err := db.DB()
		require.NoError(t, err)
		raw.SetMaxOpenConns(1)
		return db
	}
	db := open()
	require.NoError(t, db.Exec("CREATE TABLE knowledges (id VARCHAR(36) PRIMARY KEY)").Error)
	migration, err := os.ReadFile("../../../migrations/sqlite/000053_processing_cache.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledges(id) VALUES (?)", "knowledge-1").Error)
	defer func() { raw, _ := db.DB(); require.NoError(t, raw.Close()) }()
	opts := processingcache.Options{ReadEnabled: true, WriteEnabled: true, MaxBytes: 1 << 20}
	harness := newReparseHarness(t, types.ParseStatusCompleted)
	k := &harness.repo.row
	k.TenantID = 1
	k.FileName = "input.docx"
	k.FileType = "docx"
	k.FilePath = "input.md"
	require.NoError(
		t,
		k.SetProcessOverrides(&types.KnowledgeProcessOverrides{SummaryEnabled: processConfigBoolPtr(false)}),
	)
	kb := &types.KnowledgeBase{
		ID:        "kb-1",
		TenantID:  1,
		Type:      types.KnowledgeBaseTypeDocument,
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"},
		ChunkingConfig: types.ChunkingConfig{
			ParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"docx"}, Engine: "cache-test"}},
		},
	}
	kbService := &inlineLifecycleKBService{kb: kb}
	repo := &acceptanceKnowledgeRepo{
		&inlineLifecycleKnowledgeRepo{embedFailureKnowledgeRepo: embedFailureKnowledgeRepo{knowledge: k}},
	}
	catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
	storage := &inlineImageStorageState{catalog: catalog}
	file := inlineLifecycleFileService{
		inlineImageFileService: inlineImageFileService{content: "same document bytes", state: storage},
		imageBytes:             inlineImageTestPNG(t),
	}
	chunks := &acceptanceChunks{&inlineLifecycleChunkRepo{&inlineImageChunkRepo{}}}
	queue := &acceptanceQueue{}
	reader := &cacheParseReader{
		identity: "acceptance-parser-v1",
		result: &types.ReadResult{
			MarkdownContent: "Useful document text\n![figure](images/a.png)",
			ImageRefs: []types.ImageRef{
				{Filename: "a.png", OriginalRef: "images/a.png", ImageData: inlineImageTestPNG(t)},
			},
		},
	}
	svc := harness.svc
	svc.repo = repo
	svc.kbService = kbService
	svc.task = queue
	svc.chunkRepo = chunks
	// Cleanup uses a dedicated fixture because this test validates model artifact
	// reuse and current references, not the resource catalog's deletion lifecycle.
	svc.chunkService = replaceFileChunkService{}
	svc.graphEngine = replaceFileGraph{}
	svc.tenantRepo = &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}}
	svc.tenantService = &inlineImageTenantService{}
	svc.fileSvc = file
	svc.imageResolver = docparser.NewImageResolver()
	svc.resourceCatalog = catalog
	svc.documentReader = reader
	model := &acceptanceVLM{identity: "acceptance-vlm-v1"}
	useLiteCounter(t, k.ID)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{ID: 1})
	ctx, err = access.WithKBTaskWrite(ctx, kb, 1)
	require.NoError(t, err)
	oldIDs := map[string]bool{}
	for round := 0; round < 3; round++ {
		if round == 2 {
			model.identity = "acceptance-vlm-v2"
		}
		runtime := processingcache.New(gormstore.New(db), opts)
		svc.processingCache = runtime
		_, err := svc.ReparseKnowledge(ctx, k.ID, nil)
		require.NoError(t, err)
		if round > 0 {
			var retained int64
			require.NoError(t, db.Table("processing_cache").Count(&retained).Error)
			require.GreaterOrEqual(t, retained, int64(3), "ReparseKnowledge must retain successful model artifacts")
		}
		require.Len(t, queue.documents, round+1)
		var payload types.DocumentProcessPayload
		require.NoError(t, json.Unmarshal(queue.documents[round].Payload(), &payload))
		require.Equal(t, round+1, payload.Attempt)
		require.NoError(t, svc.ProcessDocument(ctx, queue.documents[round]))
		require.NotEqual(t, types.ParseStatusFailed, k.ParseStatus, k.ErrorMessage)
		require.Len(t, queue.imageTasks, round+1)
		var imagePayload types.ImageMultimodalPayload
		require.NoError(t, json.Unmarshal(queue.imageTasks[round].Payload(), &imagePayload))
		require.Equal(t, payload.Attempt, imagePayload.Attempt)
		require.Equal(t, storage.saved[round], imagePayload.ImageURL)
		require.NotEmpty(t, imagePayload.ChunkID)
		require.False(t, oldIDs[imagePayload.ChunkID])
		imageService := &ImageMultimodalService{
			chunkService:    &inlineLifecycleChunkService{repo: chunks.inlineLifecycleChunkRepo},
			knowledgeRepo:   repo,
			kbService:       kbService,
			modelService:    inlineLifecycleModelService{model: model},
			tenantRepo:      svc.tenantRepo,
			fileSvc:         file,
			taskEnqueuer:    queue,
			processingCache: runtime,
			spanTracker:     svc.spanTracker,
		}
		require.NoError(t, imageService.Handle(ctx, queue.imageTasks[round]))
		require.Len(t, queue.postProcessTasks, round+1)
		var postPayload types.KnowledgePostProcessPayload
		require.NoError(t, json.Unmarshal(queue.postProcessTasks[round].Payload(), &postPayload))
		require.Equal(t, payload.Attempt, postPayload.Attempt)
		post := &KnowledgePostProcessService{
			knowledgeRepo: repo,
			kbService:     kbService,
			chunkRepo:     &wikiEnqueueFailureChunkRepo{chunks: chunks.chunks},
			taskEnqueuer:  queue,
			spanTracker:   svc.spanTracker,
		}
		require.NoError(t, post.Handle(ctx, queue.postProcessTasks[round]))
		require.Equal(t, types.ParseStatusCompleted, k.ParseStatus)
		require.Equal(t, 1, reader.calls)
		expectedCalls := 1
		if round == 2 {
			expectedCalls = 2
		}
		require.Equal(t, expectedCalls, model.ocrCalls)
		require.Equal(t, expectedCalls, model.captionCalls)
		require.Len(t, chunks.chunks, 3)
		for _, chunk := range chunks.chunks {
			require.False(t, oldIDs[chunk.ID])
			require.False(t, oldIDs[chunk.ParentChunkID])
			if round > 0 {
				require.NotContains(t, chunk.Content, storage.saved[0])
				require.NotContains(t, chunk.ImageInfo, storage.saved[0])
			}
			if chunk.ChunkType == types.ChunkTypeImageOCR || chunk.ChunkType == types.ChunkTypeImageCaption {
				require.Equal(t, imagePayload.ChunkID, chunk.ParentChunkID)
				require.Contains(t, chunk.ImageInfo, storage.saved[round])
			}
		}
		for _, chunk := range chunks.chunks {
			oldIDs[chunk.ID] = true
		}
		var count int64
		require.NoError(t, db.Table("processing_cache").Count(&count).Error)
		require.GreaterOrEqual(t, count, int64(3))
		if round == 0 {
			raw, _ := db.DB()
			require.NoError(t, raw.Close())
			db = open()
		}
	}
	require.NotEqual(t, storage.saved[0], storage.saved[1])
	require.Equal(t, 3, svc.spanTracker.(*attemptTracker).opened)
}
