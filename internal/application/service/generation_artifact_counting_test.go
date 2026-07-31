package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/models/asr"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

func TestGenerationArtifactProviderCountingMatrix(t *testing.T) {
	ctx := context.Background()
	runtime := artifact.NewRuntime(newMultimodalArtifactStore(), artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 4096,
	})
	cfg := &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{
		"parse":         true,
		"vlm_caption":   true,
		"summary":       true,
		"question":      true,
		"wiki_map":      true,
		"graph_extract": true,
	}}}
	tenantID := uint64(7)

	t.Run("parse", func(t *testing.T) {
		svc := &knowledgeService{artifactRuntime: runtime, config: cfg}
		reader := &fakeArtifactDocReader{results: []*types.ReadResult{
			{MarkdownContent: "parsed once"},
			{MarkdownContent: "parsed twice"},
		}}
		req := &types.ReadRequest{FileContent: []byte("same file"), FileName: "doc.md", FileType: "md", ParserEngine: "builtin"}
		if _, err := svc.callDocReaderWithArtifact(ctx, reader, req, tenantID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.callDocReaderWithArtifact(ctx, reader, req, tenantID); err != nil {
			t.Fatal(err)
		}
		if reader.calls != 1 {
			t.Fatalf("parse provider calls = %d, want 1 total and 0 on repeated input", reader.calls)
		}
	})

	t.Run("vlm", func(t *testing.T) {
		svc := &ImageMultimodalService{artifactRuntime: runtime, config: cfg}
		model := &fakeArtifactVLM{out: []string{"caption once", "caption twice"}}
		payload := types.ImageMultimodalPayload{TenantID: tenantID}
		vlmCfg := types.VLMConfig{ModelID: "vlm-1", DescriptionLanguage: "English"}
		if _, _, err := svc.predictVLMWithArtifact(ctx, model, payload, vlmCfg, []byte("same image"), "caption", "vlm_caption"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.predictVLMWithArtifact(ctx, model, payload, vlmCfg, []byte("same image"), "caption", "vlm_caption"); err != nil {
			t.Fatal(err)
		}
		if model.calls != 1 {
			t.Fatalf("vlm provider calls = %d, want 1 total and 0 on repeated input", model.calls)
		}
	})

	for _, stage := range []string{"summary", "question"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			svc := &knowledgeService{artifactRuntime: runtime, config: cfg}
			model := &fakeArtifactChat{out: []string{stage + " once", stage + " twice"}}
			messages := []chat.Message{{Role: "user", Content: "same " + stage + " prompt"}}
			if _, _, err := svc.chatContentWithArtifact(ctx, model, tenantID, stage, messages, &chat.ChatOptions{Temperature: 0.1}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := svc.chatContentWithArtifact(ctx, model, tenantID, stage, messages, &chat.ChatOptions{Temperature: 0.1}); err != nil {
				t.Fatal(err)
			}
			if model.calls != 1 {
				t.Fatalf("%s provider calls = %d, want 1 total and 0 on repeated input", stage, model.calls)
			}
		})
	}

	for _, stage := range []string{"wiki_map", "graph_extract"} {
		stage := stage
		t.Run(stage, func(t *testing.T) {
			model := &fakeArtifactChat{out: []string{stage + " once", stage + " twice"}}
			cached := newArtifactCachingChat(model, runtime, tenantID, stage)
			messages := []chat.Message{{Role: "user", Content: "same " + stage + " prompt"}}
			if _, err := cached.Chat(ctx, messages, &chat.ChatOptions{Temperature: 0.2}); err != nil {
				t.Fatal(err)
			}
			if _, err := cached.Chat(ctx, messages, &chat.ChatOptions{Temperature: 0.2}); err != nil {
				t.Fatal(err)
			}
			if model.calls != 1 {
				t.Fatalf("%s provider calls = %d, want 1 total and 0 on repeated input", stage, model.calls)
			}
		})
	}

	t.Run("embedding partial change", func(t *testing.T) {
		embedder := &processDocumentCountingEmbedder{}
		engine := retriever.NewKVHybridRetrieveEngine(
			&processDocumentCountingRetrieveRepo{},
			types.SQLiteRetrieverEngineType,
			retriever.WithArtifactRuntime(runtime),
		)
		prime := []*types.IndexInfo{
			{Content: "cached chunk", SourceID: "chunk-a"},
			{Content: "old chunk", SourceID: "chunk-b"},
		}
		mixed := []*types.IndexInfo{
			{Content: "cached chunk", SourceID: "chunk-a-rebound"},
			{Content: "changed chunk", SourceID: "chunk-b-new"},
		}
		ctx := context.WithValue(ctx, types.TenantIDContextKey, tenantID)

		if err := engine.BatchIndex(ctx, embedder, prime, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatalf("prime BatchIndex returned error: %v", err)
		}
		embedder.batchTexts = nil
		if err := engine.BatchIndex(ctx, embedder, mixed, []types.RetrieverType{types.VectorRetrieverType}); err != nil {
			t.Fatalf("mixed BatchIndex returned error: %v", err)
		}
		if len(embedder.batchTexts) != 1 || embedder.batchTexts[0] != "changed chunk" {
			t.Fatalf("embedding provider inputs after partial change = %v, want only [changed chunk]", embedder.batchTexts)
		}
	})
}

func (r *fakeArtifactDocReader) Reconnect(string) error { return nil }
func (r *fakeArtifactDocReader) IsConnected() bool      { return true }
func (r *fakeArtifactDocReader) ListEngines(context.Context, map[string]string) ([]types.ParserEngineInfo, error) {
	return nil, nil
}

type countingTaskRecorder struct {
	tasks []*asynq.Task
}

func (r *countingTaskRecorder) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	r.tasks = append(r.tasks, task)
	return &asynq.TaskInfo{ID: "task-1", Type: task.Type()}, nil
}

type processDocumentCountingFileService struct {
	interfaces.FileService
	content []byte
}

func (s processDocumentCountingFileService) CheckConnectivity(context.Context) error { return nil }

func (s processDocumentCountingFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (s processDocumentCountingFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}

func (s processDocumentCountingFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(s.content)), nil
}

func (s processDocumentCountingFileService) GetFileURL(context.Context, string) (string, error) {
	return "", nil
}

func (s processDocumentCountingFileService) DeleteFile(context.Context, string) error { return nil }

func (s processDocumentCountingFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

type processDocumentCountingTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r processDocumentCountingTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return r.tenant, nil
}

func (r processDocumentCountingTenantRepo) AdjustStorageUsed(context.Context, uint64, int64) error {
	return nil
}

type processDocumentCountingKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s processDocumentCountingKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s processDocumentCountingKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type processDocumentCountingKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r processDocumentCountingKnowledgeRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r processDocumentCountingKnowledgeRepo) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r processDocumentCountingKnowledgeRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	return nil
}

func (r processDocumentCountingKnowledgeRepo) UpdateKnowledgeColumn(context.Context, string, string, interface{}) error {
	return nil
}

func (r processDocumentCountingKnowledgeRepo) SetFinalizing(context.Context, string, int) (bool, error) {
	r.knowledge.ParseStatus = types.ParseStatusFinalizing
	return true, nil
}

func (r processDocumentCountingKnowledgeRepo) FinalizeSubtask(context.Context, string) (int, bool, error) {
	return 0, true, nil
}

type processDocumentCountingChunkService struct {
	interfaces.ChunkService
	chunks []*types.Chunk
}

func (s *processDocumentCountingChunkService) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	s.chunks = append(s.chunks, chunks...)
	return nil
}

func (s *processDocumentCountingChunkService) ListChunksByKnowledgeID(context.Context, string) ([]*types.Chunk, error) {
	return s.chunks, nil
}

type processDocumentCountingChunkRepo struct {
	interfaces.ChunkRepository
	chunkService *processDocumentCountingChunkService
}

func (s processDocumentCountingChunkRepo) ListActiveChunksByKnowledgeID(context.Context, uint64, string) ([]*types.Chunk, error) {
	return nil, nil
}

func (s processDocumentCountingChunkRepo) ListGenerationChunks(context.Context, uint64, string, string) ([]*types.Chunk, error) {
	if s.chunkService == nil {
		return nil, nil
	}
	return s.chunkService.chunks, nil
}

type processDocumentCountingGenerationRepo struct {
	interfaces.KnowledgeGenerationRepository
	generation    *types.KnowledgeGeneration
	readyCalls    int
	activateCalls int
}

func (r *processDocumentCountingGenerationRepo) Get(context.Context, uint64, string) (*types.KnowledgeGeneration, error) {
	return r.generation, nil
}

func (r *processDocumentCountingGenerationRepo) GetActive(context.Context, uint64, string) (*types.KnowledgeGeneration, error) {
	return nil, nil
}

func (r *processDocumentCountingGenerationRepo) Create(_ context.Context, generation *types.KnowledgeGeneration) error {
	r.generation = generation
	return nil
}

func (r *processDocumentCountingGenerationRepo) LatestAttempt(context.Context, uint64, string) (int, error) {
	if r.generation == nil {
		return 0, nil
	}
	return r.generation.Attempt, nil
}

func (r *processDocumentCountingGenerationRepo) MarkReady(context.Context, string, string) error {
	r.readyCalls++
	r.generation.State = types.KnowledgeGenerationStateReady
	return nil
}

func (r *processDocumentCountingGenerationRepo) ActivateIfCurrent(context.Context, string, int) (bool, error) {
	r.activateCalls++
	if r.generation != nil {
		r.generation.State = types.KnowledgeGenerationStateActive
	}
	return true, nil
}

func (r *processDocumentCountingGenerationRepo) MarkRetired(context.Context, string) error {
	if r.generation != nil {
		r.generation.State = types.KnowledgeGenerationStateRetired
	}
	return nil
}

type processDocumentCountingEmbedder struct {
	calls      int
	batchTexts []string
}

func (e *processDocumentCountingEmbedder) Embed(context.Context, string) ([]float32, error) {
	e.calls++
	return []float32{1}, nil
}

func (e *processDocumentCountingEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}

func (e *processDocumentCountingEmbedder) BatchEmbedWithPool(_ context.Context, _ embedding.Embedder, texts []string) ([][]float32, error) {
	e.calls++
	e.batchTexts = append(e.batchTexts, texts...)
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{float32(i + 1)}
	}
	return out, nil
}

func (e *processDocumentCountingEmbedder) GetModelName() string { return "counting-embedding" }
func (e *processDocumentCountingEmbedder) GetModelID() string   { return "embedding-1" }
func (e *processDocumentCountingEmbedder) GetDimensions() int   { return 1 }

type processDocumentCountingModelService struct {
	interfaces.ModelService
	embedder embedding.Embedder
}

func (s processDocumentCountingModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	return s.embedder, nil
}

func (s processDocumentCountingModelService) GetEmbeddingModelForTenant(context.Context, string, uint64) (embedding.Embedder, error) {
	return s.embedder, nil
}

func (s processDocumentCountingModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return nil, nil
}

func (s processDocumentCountingModelService) GetRerankModel(context.Context, string) (rerank.Reranker, error) {
	return nil, nil
}

func (s processDocumentCountingModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return nil, nil
}

func (s processDocumentCountingModelService) GetASRModel(context.Context, string) (asr.ASR, error) {
	return nil, nil
}

type processDocumentCountingRetrieveRepo struct {
	interfaces.RetrieveEngineRepository
	batchSaves int
}

func (r *processDocumentCountingRetrieveRepo) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (r *processDocumentCountingRetrieveRepo) EngineType() types.RetrieverEngineType {
	return types.SQLiteRetrieverEngineType
}

func (r *processDocumentCountingRetrieveRepo) Retrieve(context.Context, types.RetrieveParams) ([]*types.RetrieveResult, error) {
	return nil, nil
}

func (r *processDocumentCountingRetrieveRepo) Save(context.Context, *types.IndexInfo, map[string]any) error {
	return nil
}

func (r *processDocumentCountingRetrieveRepo) BatchSave(context.Context, []*types.IndexInfo, map[string]any) error {
	r.batchSaves++
	return nil
}

func (r *processDocumentCountingRetrieveRepo) EstimateStorageSize(context.Context, []*types.IndexInfo, map[string]any) int64 {
	return 0
}

func (r *processDocumentCountingRetrieveRepo) DeleteByChunkIDList(context.Context, []string, int, string) error {
	return nil
}

func (r *processDocumentCountingRetrieveRepo) DeleteBySourceIDList(context.Context, []string, int, string) error {
	return nil
}

func (r *processDocumentCountingRetrieveRepo) CopyIndices(context.Context, string, map[string]string, map[string]string, string, int, string) error {
	return nil
}

func (r *processDocumentCountingRetrieveRepo) DeleteByKnowledgeIDList(context.Context, []string, int, string) error {
	return nil
}

func (r *processDocumentCountingRetrieveRepo) BatchUpdateChunkEnabledStatus(context.Context, map[string]bool) error {
	return nil
}

func (r *processDocumentCountingRetrieveRepo) BatchUpdateChunkTagID(context.Context, map[string]string) error {
	return nil
}

type processDocumentCountingRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r processDocumentCountingRegistry) Register(interfaces.RetrieveEngineService) error { return nil }

func (r processDocumentCountingRegistry) GetRetrieveEngineService(types.RetrieverEngineType) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func (r processDocumentCountingRegistry) GetAllRetrieveEngineServices() []interfaces.RetrieveEngineService {
	return []interfaces.RetrieveEngineService{r.engine}
}

func (r processDocumentCountingRegistry) GetByStoreID(string) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func (r processDocumentCountingRegistry) GetOrLoadByStoreID(context.Context, uint64, string) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

func TestProcessDocumentReusesParseAndEmbeddingArtifactsOnRetry(t *testing.T) {
	ctx := context.Background()
	tenant := &types.Tenant{
		ID: 7,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.SQLiteRetrieverEngineType,
		}}},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize: 1024,
			ParserEngineRules: []types.ParserEngineRule{{
				FileTypes: []string{"md"},
				Engine:    "builtin",
			}},
		},
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		FilePath:        "local://doc.md",
		FileName:        "doc.md",
		FileType:        "md",
		ParseStatus:     types.ParseStatusPending,
	}
	generation := &types.KnowledgeGeneration{
		ID:          "generation-1",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		Attempt:     1,
		State:       types.KnowledgeGenerationStateBuilding,
	}
	reader := &fakeArtifactDocReader{results: []*types.ReadResult{
		{MarkdownContent: "alpha beta gamma"},
		{MarkdownContent: "changed result should stay cached"},
	}}
	embedder := &processDocumentCountingEmbedder{}
	retrieveRepo := &processDocumentCountingRetrieveRepo{}
	store := newMultimodalArtifactStore()
	runtime := artifact.NewRuntime(store, artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 4096,
	})
	engine := retriever.NewKVHybridRetrieveEngine(
		retrieveRepo,
		types.SQLiteRetrieverEngineType,
		retriever.WithArtifactRuntime(runtime),
	)
	chunkService := &processDocumentCountingChunkService{}
	svc := &knowledgeService{
		config: &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{
			"parse":     true,
			"embedding": true,
		}}},
		repo:            processDocumentCountingKnowledgeRepo{knowledge: knowledge},
		kbService:       processDocumentCountingKBService{kb: kb},
		tenantRepo:      processDocumentCountingTenantRepo{tenant: tenant},
		documentReader:  reader,
		fileSvc:         processDocumentCountingFileService{content: []byte("same source bytes")},
		modelService:    processDocumentCountingModelService{embedder: embedder},
		chunkService:    chunkService,
		chunkRepo:       processDocumentCountingChunkRepo{},
		generationRepo:  &processDocumentCountingGenerationRepo{generation: generation},
		retrieveEngine:  processDocumentCountingRegistry{engine: engine},
		task:            &manualReparseTaskEnqueuer{},
		artifactRuntime: runtime,
	}
	payload := types.DocumentProcessPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		FilePath:        "local://doc.md",
		FileName:        "doc.md",
		FileType:        "md",
		Attempt:         1,
		GenerationID:    "generation-1",
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes)

	if err := svc.ProcessDocument(ctx, task); err != nil {
		t.Fatalf("first ProcessDocument returned error: %v", err)
	}
	knowledge.ParseStatus = types.ParseStatusPending
	generation.State = types.KnowledgeGenerationStateBuilding
	if err := svc.ProcessDocument(ctx, task); err != nil {
		t.Fatalf("second ProcessDocument returned error: %v", err)
	}

	if reader.calls != 1 {
		t.Fatalf("docreader calls = %d puts=%d reqs=%+v, want 1 total and 0 on retry",
			reader.calls, store.puts, summarizeReadRequests(reader.reqs))
	}
	if embedder.calls != 1 {
		t.Fatalf("embedding provider calls = %d, want 1 total and 0 on retry", embedder.calls)
	}
	if retrieveRepo.batchSaves != 2 {
		t.Fatalf("vector writes = %d, want 2 hidden generation writes across retries", retrieveRepo.batchSaves)
	}
}

func TestReparseKnowledgeThenProcessDocumentRetryReusesParseAndEmbeddingArtifacts(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	tenant := &types.Tenant{
		ID: 7,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.SQLiteRetrieverEngineType,
		}}},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize: 1024,
			ParserEngineRules: []types.ParserEngineRule{{
				FileTypes: []string{"md"},
				Engine:    "builtin",
			}},
		},
	}
	knowledge := &types.Knowledge{
		ID:                 "knowledge-1",
		TenantID:           7,
		KnowledgeBaseID:    "kb-1",
		Type:               "file",
		FilePath:           "local://doc.md",
		FileName:           "doc.md",
		FileType:           "md",
		FileHash:           "same-hash",
		FileSize:           17,
		ParseStatus:        types.ParseStatusCompleted,
		ActiveGenerationID: "generation-active",
	}
	reader := &fakeArtifactDocReader{results: []*types.ReadResult{
		{MarkdownContent: "alpha beta gamma"},
		{MarkdownContent: "changed result should stay cached"},
	}}
	embedder := &processDocumentCountingEmbedder{}
	retrieveRepo := &processDocumentCountingRetrieveRepo{}
	store := newMultimodalArtifactStore()
	runtime := artifact.NewRuntime(store, artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 4096,
	})
	engine := retriever.NewKVHybridRetrieveEngine(
		retrieveRepo,
		types.SQLiteRetrieverEngineType,
		retriever.WithArtifactRuntime(runtime),
	)
	chunkService := &processDocumentCountingChunkService{}
	queue := &manualReparseTaskEnqueuer{}
	generationRepo := &processDocumentCountingGenerationRepo{}
	svc := &knowledgeService{
		config: &config.Config{
			ReparseGeneration: &config.ReparseGenerationConfig{Enabled: true, NoopFastPath: true},
			ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{
				"parse":     true,
				"embedding": true,
			}},
		},
		repo:            processDocumentCountingKnowledgeRepo{knowledge: knowledge},
		kbService:       processDocumentCountingKBService{kb: kb},
		tenantRepo:      processDocumentCountingTenantRepo{tenant: tenant},
		documentReader:  reader,
		fileSvc:         processDocumentCountingFileService{content: []byte("same source bytes")},
		modelService:    processDocumentCountingModelService{embedder: embedder},
		chunkService:    chunkService,
		chunkRepo:       processDocumentCountingChunkRepo{},
		generationRepo:  generationRepo,
		retrieveEngine:  processDocumentCountingRegistry{engine: engine},
		task:            queue,
		spanTracker:     &noOpReparseAttemptTracker{attempt: 1},
		artifactRuntime: runtime,
	}

	if _, err := svc.ReparseKnowledge(ctx, "knowledge-1", nil); err != nil {
		t.Fatalf("ReparseKnowledge returned error: %v", err)
	}
	if queue.task == nil {
		t.Fatal("ReparseKnowledge did not enqueue document processing")
	}
	documentTask := queue.task
	if generationRepo.generation == nil || generationRepo.generation.ID == "" {
		t.Fatal("ReparseKnowledge did not create generation")
	}
	if err := svc.ProcessDocument(context.Background(), documentTask); err != nil {
		t.Fatalf("first ProcessDocument returned error: %v", err)
	}
	knowledge.ParseStatus = types.ParseStatusPending
	generationRepo.generation.State = types.KnowledgeGenerationStateBuilding
	if err := svc.ProcessDocument(context.Background(), documentTask); err != nil {
		t.Fatalf("second ProcessDocument returned error: %v", err)
	}

	if reader.calls != 1 {
		t.Fatalf("docreader calls = %d puts=%d reqs=%+v, want 1 total and 0 on retry",
			reader.calls, store.puts, summarizeReadRequests(reader.reqs))
	}
	if embedder.calls != 1 {
		t.Fatalf("embedding provider calls = %d, want 1 total and 0 on retry", embedder.calls)
	}
	if retrieveRepo.batchSaves != 2 {
		t.Fatalf("vector writes = %d, want 2 hidden generation writes across reparse retry", retrieveRepo.batchSaves)
	}
	if generationRepo.readyCalls != 2 {
		t.Fatalf("generation ready calls = %d, want ready after each retry before activation", generationRepo.readyCalls)
	}
	if generationRepo.activateCalls != 0 {
		t.Fatalf("generation activation calls = %d, want ProcessDocument crash/retry path to leave activation to postprocess finalize", generationRepo.activateCalls)
	}
	if knowledge.ActiveGenerationID != "generation-active" {
		t.Fatalf("active generation = %q, want old active generation to remain visible until activation", knowledge.ActiveGenerationID)
	}
}

func TestProcessDocumentPostProcessCarriesGenerationFenceToEnrichmentTasks(t *testing.T) {
	ctx := context.Background()
	t.Setenv("NEO4J_ENABLE", "true")
	tenant := &types.Tenant{
		ID: 7,
		RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
			RetrieverType:       types.VectorRetrieverType,
			RetrieverEngineType: types.SQLiteRetrieverEngineType,
		}}},
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embedding-1",
		SummaryModelID:   "chat-1",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, GraphEnabled: true},
		ExtractConfig:    &types.ExtractConfig{Enabled: true},
		QuestionGenerationConfig: &types.QuestionGenerationConfig{
			Enabled:       true,
			QuestionCount: 2,
		},
		ChunkingConfig: types.ChunkingConfig{
			ChunkSize: 1024,
			ParserEngineRules: []types.ParserEngineRule{{
				FileTypes: []string{"md"},
				Engine:    "builtin",
			}},
		},
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		FilePath:        "local://doc.md",
		FileName:        "doc.md",
		FileType:        "md",
		ParseStatus:     types.ParseStatusPending,
	}
	generation := &types.KnowledgeGeneration{
		ID:          "generation-1",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		Attempt:     1,
		State:       types.KnowledgeGenerationStateBuilding,
	}
	reader := &fakeArtifactDocReader{results: []*types.ReadResult{{
		MarkdownContent: "alpha beta gamma",
	}}}
	embedder := &processDocumentCountingEmbedder{}
	retrieveRepo := &processDocumentCountingRetrieveRepo{}
	runtime := artifact.NewRuntime(newMultimodalArtifactStore(), artifact.RuntimeOptions{
		ReadEnabled: true, WriteEnabled: true, MaxInlineBytes: 4096,
	})
	engine := retriever.NewKVHybridRetrieveEngine(
		retrieveRepo,
		types.SQLiteRetrieverEngineType,
		retriever.WithArtifactRuntime(runtime),
	)
	chunkService := &processDocumentCountingChunkService{}
	chunkRepo := processDocumentCountingChunkRepo{chunkService: chunkService}
	queue := &manualReparseTaskEnqueuer{}
	svc := &knowledgeService{
		config: &config.Config{ArtifactCache: &config.ArtifactCacheConfig{Stages: map[string]bool{
			"parse":     true,
			"embedding": true,
		}}},
		repo:            processDocumentCountingKnowledgeRepo{knowledge: knowledge},
		kbService:       processDocumentCountingKBService{kb: kb},
		tenantRepo:      processDocumentCountingTenantRepo{tenant: tenant},
		documentReader:  reader,
		fileSvc:         processDocumentCountingFileService{content: []byte("same source bytes")},
		modelService:    processDocumentCountingModelService{embedder: embedder},
		chunkService:    chunkService,
		chunkRepo:       chunkRepo,
		generationRepo:  &processDocumentCountingGenerationRepo{generation: generation},
		retrieveEngine:  processDocumentCountingRegistry{engine: engine},
		task:            queue,
		artifactRuntime: runtime,
	}
	payload := types.DocumentProcessPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		FilePath:        "local://doc.md",
		FileName:        "doc.md",
		FileType:        "md",
		Attempt:         1,
		GenerationID:    "generation-1",
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.ProcessDocument(ctx, asynq.NewTask(types.TypeDocumentProcess, payloadBytes)); err != nil {
		t.Fatalf("ProcessDocument returned error: %v", err)
	}
	if queue.task == nil || queue.task.Type() != types.TypeKnowledgePostProcess {
		t.Fatalf("ProcessDocument enqueued %v, want postprocess task", queue.task)
	}

	enrichmentQueue := &countingTaskRecorder{}
	postProcess := &KnowledgePostProcessService{
		knowledgeRepo:  processDocumentCountingKnowledgeRepo{knowledge: knowledge},
		generationRepo: &processDocumentCountingGenerationRepo{generation: generation},
		kbService:      processDocumentCountingKBService{kb: kb},
		chunkService:   chunkService,
		chunkRepo:      chunkRepo,
		taskEnqueuer:   enrichmentQueue,
	}
	if err := postProcess.Handle(ctx, queue.task); err != nil {
		t.Fatalf("postprocess returned error: %v", err)
	}

	seen := map[string]int{}
	for _, task := range enrichmentQueue.tasks {
		seen[task.Type()]++
		switch task.Type() {
		case types.TypeSummaryGeneration:
			var got types.SummaryGenerationPayload
			if err := json.Unmarshal(task.Payload(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Attempt != 1 || got.GenerationID != "generation-1" {
				t.Fatalf("summary fence attempt=%d generation=%q", got.Attempt, got.GenerationID)
			}
		case types.TypeQuestionGeneration:
			var got types.QuestionGenerationPayload
			if err := json.Unmarshal(task.Payload(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Attempt != 1 || got.GenerationID != "generation-1" {
				t.Fatalf("question fence attempt=%d generation=%q", got.Attempt, got.GenerationID)
			}
		case types.TypeChunkExtract:
			var got types.ExtractChunkPayload
			if err := json.Unmarshal(task.Payload(), &got); err != nil {
				t.Fatal(err)
			}
			if got.Attempt != 1 || got.GenerationID != "generation-1" {
				t.Fatalf("graph fence attempt=%d generation=%q", got.Attempt, got.GenerationID)
			}
		}
	}
	if seen[types.TypeSummaryGeneration] != 1 ||
		seen[types.TypeQuestionGeneration] != 1 ||
		seen[types.TypeChunkExtract] != 1 ||
		seen[types.TypeWikiIngest] != 0 {
		t.Fatalf("enrichment tasks = %+v, want one summary, one question batch, one graph extract, and no wiki trigger", seen)
	}
}

func summarizeReadRequests(reqs []*types.ReadRequest) []map[string]any {
	out := make([]map[string]any, 0, len(reqs))
	for _, req := range reqs {
		if req == nil {
			continue
		}
		out = append(out, map[string]any{
			"file_name":       req.FileName,
			"file_type":       req.FileType,
			"title":           req.Title,
			"parser_engine":   req.ParserEngine,
			"request_id":      req.RequestID,
			"content_len":     len(req.FileContent),
			"override_count":  len(req.ParserEngineOverrides),
			"override_values": req.ParserEngineOverrides,
		})
	}
	return out
}
