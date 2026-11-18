package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	sqliteRetrieverRepo "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	retrieverSvc "github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var registerReparseSQLiteVec sync.Once

type reparseQueueCapture struct {
	mu    sync.Mutex
	tasks []*asynq.Task
}

func (q *reparseQueueCapture) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: fmt.Sprintf("task-%d", len(q.tasks)), Queue: types.QueueDefault}, nil
}

func (q *reparseQueueCapture) takeTasks(taskType string) []*asynq.Task {
	q.mu.Lock()
	defer q.mu.Unlock()
	var matched, rest []*asynq.Task
	for _, t := range q.tasks {
		if t.Type() == taskType {
			matched = append(matched, t)
		} else {
			rest = append(rest, t)
		}
	}
	q.tasks = rest
	return matched
}

type deterministicTestEmbedder struct {
	embedding.Embedder
	mu        sync.Mutex
	id        string
	dims      int
	failNextN int
}

func (e *deterministicTestEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	vecs, err := e.BatchEmbed(context.Background(), []string{text})
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (e *deterministicTestEmbedder) BatchEmbed(_ context.Context, texts []string) ([][]float32, error) {
	e.mu.Lock()
	if e.failNextN > 0 {
		e.failNextN--
		e.mu.Unlock()
		return nil, errors.New("429 rate limit from embedding provider")
	}
	e.mu.Unlock()
	out := make([][]float32, len(texts))
	for i, text := range texts {
		vec := make([]float32, e.dims)
		tokens := strings.Fields(strings.ToLower(text))
		if len(tokens) == 0 {
			vec[0] = 1
			out[i] = vec
			continue
		}
		for _, tok := range tokens {
			h := fnv.New32a()
			_, _ = h.Write([]byte(tok))
			slot := int(h.Sum32()) % e.dims
			vec[slot] += 1
		}
		out[i] = vec
	}
	return out, nil
}

func (e *deterministicTestEmbedder) BatchEmbedWithPool(
	ctx context.Context, _ embedding.Embedder, texts []string,
) ([][]float32, error) {
	return e.BatchEmbed(ctx, texts)
}

func (e *deterministicTestEmbedder) GetModelName() string { return e.id }
func (e *deterministicTestEmbedder) GetModelID() string   { return e.id }
func (e *deterministicTestEmbedder) GetDimensions() int   { return e.dims }

type reparseModelCatalog struct {
	interfaces.ModelService
	models map[string]*deterministicTestEmbedder
}

func (m *reparseModelCatalog) GetEmbeddingModel(_ context.Context, modelID string) (embedding.Embedder, error) {
	emb, ok := m.models[modelID]
	if !ok || emb == nil {
		return nil, fmt.Errorf("embedding model %q not found", modelID)
	}
	return emb, nil
}

type controllableDocReader struct {
	interfaces.DocumentReader
	mu       sync.Mutex
	markdown string
	readErr  error
	onRead   func()
}

func (r *controllableDocReader) IsConnected() bool { return true }

func (r *controllableDocReader) Read(_ context.Context, _ *types.ReadRequest) (*types.ReadResult, error) {
	r.mu.Lock()
	hook := r.onRead
	md := r.markdown
	err := r.readErr
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	if err != nil {
		return nil, err
	}
	return &types.ReadResult{MarkdownContent: md}, nil
}

func (r *controllableDocReader) Kind() string { return "remote" }

type recordingGraphRepo struct {
	interfaces.RetrieveGraphRepository
	deleted []types.NameSpace
}

func (g *recordingGraphRepo) DelGraph(_ context.Context, ns []types.NameSpace) error {
	g.deleted = append(g.deleted, ns...)
	return nil
}

type reparseLiveKBService struct {
	interfaces.KnowledgeBaseService
	db *gorm.DB
}

func (s *reparseLiveKBService) GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error) {
	var kb types.KnowledgeBase
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&kb).Error; err != nil {
		return nil, err
	}
	return &kb, nil
}

type reparseE2EHarness struct {
	t           *testing.T
	db          *gorm.DB
	tenant      *types.Tenant
	kb          *types.KnowledgeBase
	svc         *knowledgeService
	searchSvc   *knowledgeBaseService
	graphSearch interfaces.GraphSearchEvidenceRepository
	catalog     interfaces.ResourceCatalog
	fileSvc     interfaces.FileService
	queue       *reparseQueueCapture
	models      *reparseModelCatalog
	docReader   *controllableDocReader
	graphRepo   *recordingGraphRepo
}

func newReparseE2EHarness(t *testing.T) *reparseE2EHarness {
	t.Helper()
	registerReparseSQLiteVec.Do(sqlite_vec.Auto)

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, db.AutoMigrate(
		&types.Tenant{},
		&types.KnowledgeBase{},
		&types.Knowledge{},
		&types.Chunk{},
		&types.ChunkRevision{},
		&types.StoredResource{},
		&types.ResourceBinding{},
		&types.ResourceAccessGrant{},
	))
	require.NoError(t, db.Exec(spanTrackerTestDDL).Error)

	tenant := &types.Tenant{
		ID: 7, Name: "reparse-e2e",
		RetrieverEngines: types.RetrieverEngines{
			Engines: []types.RetrieverEngineParams{{
				RetrieverType:       types.VectorRetrieverType,
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
			}},
		},
	}
	require.NoError(t, db.Create(tenant).Error)

	kb := &types.KnowledgeBase{
		ID:               "kb-reparse",
		TenantID:         tenant.ID,
		Name:             "KB",
		Type:             types.KnowledgeBaseTypeDocument,
		EmbeddingModelID: "embed-4d",
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		ChunkingConfig:   types.ChunkingConfig{ChunkSize: 512, ChunkOverlap: 0},
	}
	require.NoError(t, db.Create(kb).Error)

	knowledgeRepo := repository.NewKnowledgeRepository(db)
	chunkRepo := repository.NewChunkRepository(db)
	tenantRepo := repository.NewTenantRepository(db)
	catalog := NewResourceCatalog(repository.NewResourceRepository(db))
	fileSvc := filesvc.NewResourceCatalogFileService(filesvc.NewLocalFileService(t.TempDir(), ""), catalog)

	sqliteRepo := sqliteRetrieverRepo.NewSQLiteRetrieveEngineRepository(db)
	hybridEngine := retrieverSvc.NewKVHybridRetrieveEngine(sqliteRepo, types.SQLiteRetrieverEngineType)
	registry := retrieverSvc.NewRetrieveEngineRegistry(nil, nil)
	require.NoError(t, registry.Register(hybridEngine))

	models := &reparseModelCatalog{
		models: map[string]*deterministicTestEmbedder{
			"embed-4d": {id: "embed-4d", dims: 4},
			"embed-8d": {id: "embed-8d", dims: 8},
		},
	}
	queue := &reparseQueueCapture{}
	docReader := &controllableDocReader{}
	graphRepo := &recordingGraphRepo{}
	kbSvc := &reparseLiveKBService{db: db}
	tracker := NewSpanTracker(repository.NewKnowledgeSpanRepository(db), db)

	svc := &knowledgeService{
		repo:            knowledgeRepo,
		kbService:       kbSvc,
		chunkRepo:       chunkRepo,
		chunkService:    &chunkService{chunkRepository: chunkRepo},
		tenantRepo:      tenantRepo,
		tenantService:   &inlineImageTenantService{},
		modelService:    models,
		retrieveEngine:  registry,
		graphEngine:     graphRepo,
		fileSvc:         fileSvc,
		imageResolver:   docparser.NewImageResolver(),
		resourceCatalog: catalog,
		documentReader:  docReader,
		task:            queue,
		spanTracker:     tracker,
	}
	searchSvc := &knowledgeBaseService{
		kgRepo:         knowledgeRepo,
		chunkRepo:      chunkRepo,
		modelService:   models,
		retrieveEngine: registry,
	}
	return &reparseE2EHarness{
		t:           t,
		db:          db,
		tenant:      tenant,
		kb:          kb,
		svc:         svc,
		searchSvc:   searchSvc,
		graphSearch: repository.NewGraphSearchEvidenceRepository(db),
		catalog:     catalog,
		fileSvc:     fileSvc,
		queue:       queue,
		models:      models,
		docReader:   docReader,
		graphRepo:   graphRepo,
	}
}

func (h *reparseE2EHarness) writeCtx() context.Context {
	var freshTenant types.Tenant
	require.NoError(h.t, h.db.Where("id = ?", h.tenant.ID).First(&freshTenant).Error)
	var freshKB types.KnowledgeBase
	require.NoError(h.t, h.db.Where("id = ?", h.kb.ID).First(&freshKB).Error)

	ctx := types.WithExecutionTenant(context.Background(), freshTenant.ID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &freshTenant)
	ctx, err := access.WithKBTaskWrite(ctx, &freshKB, freshTenant.ID)
	require.NoError(h.t, err)
	return ctx
}

func (h *reparseE2EHarness) vectorSearchWithModel(modelID, query string) []*types.SearchResult {
	h.t.Helper()
	ctx := h.writeCtx()
	emb, err := h.models.GetEmbeddingModel(ctx, modelID)
	require.NoError(h.t, err)
	qVec, err := emb.Embed(ctx, query)
	require.NoError(h.t, err)

	var freshTenant types.Tenant
	require.NoError(h.t, h.db.First(&freshTenant, "id = ?", h.tenant.ID).Error)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &freshTenant)
	engine, err := retrieverSvc.CreateRetrieveEngineForKB(ctx, h.svc.retrieveEngine, nil, freshTenant.ID, nil)
	require.NoError(h.t, err)

	raw, err := engine.Retrieve(ctx, []types.RetrieveParams{{
		Query:            query,
		Embedding:        qVec,
		KnowledgeBaseIDs: []string{h.kb.ID},
		TopK:             10,
		Threshold:        -1,
		RetrieverType:    types.VectorRetrieverType,
	}})
	require.NoError(h.t, err)

	var hits []*types.IndexWithScore
	for _, r := range raw {
		hits = append(hits, r.Results...)
	}
	results, err := h.searchSvc.processSearchResults(ctx, hits, 10, true)
	require.NoError(h.t, err)
	return results
}

func (h *reparseE2EHarness) ingestPDF(id, fileName, markdown, summary string) (*types.Knowledge, []string) {
	h.t.Helper()
	ctx := h.writeCtx()
	fh, err := bytesToFileHeader([]byte("%PDF-1.4 fixture "+id), fileName)
	require.NoError(h.t, err)
	filePath, err := h.fileSvc.SaveFile(ctx, fh, h.tenant.ID, id)
	require.NoError(h.t, err)

	doc := &types.Knowledge{
		ID:               id,
		TenantID:         h.tenant.ID,
		KnowledgeBaseID:  h.kb.ID,
		Type:             "file",
		Title:            fileName,
		FileName:         fileName,
		FileType:         "pdf",
		FilePath:         filePath,
		ParseStatus:      types.ParseStatusPending,
		EnableStatus:     "disabled",
		EmbeddingModelID: h.kb.EmbeddingModelID,
	}
	require.NoError(h.t, h.db.Create(doc).Error)

	h.docReader.markdown = markdown
	h.docReader.readErr = nil

	_, attempt, err := h.svc.spanTracker.OpenAttempt(ctx, id, "")
	require.NoError(h.t, err)
	payload, err := json.Marshal(types.DocumentProcessPayload{
		TenantID: h.tenant.ID, KnowledgeID: id, KnowledgeBaseID: h.kb.ID,
		FilePath: filePath, FileName: fileName, FileType: "pdf", Attempt: attempt,
	})
	require.NoError(h.t, err)
	require.NoError(h.t, h.svc.ProcessDocument(context.Background(), asynq.NewTask(types.TypeDocumentProcess, payload)))
	_ = h.queue.takeTasks(types.TypeKnowledgePostProcess)

	imageRefs := attachExtractedImagesToChunks(h.t, h.db, h.tenant.ID, id)
	require.NoError(h.t, h.db.Model(&types.Knowledge{}).Where("id = ?", id).Updates(map[string]any{
		"parse_status": types.ParseStatusCompleted,
		"description":  summary,
	}).Error)

	var persisted types.Knowledge
	require.NoError(h.t, h.db.First(&persisted, "id = ?", id).Error)
	return &persisted, imageRefs
}

func encodeSolidPNGDataURI(t *testing.T, c color.RGBA) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func attachExtractedImagesToChunks(t *testing.T, db *gorm.DB, tenantID uint64, knowledgeID string) []string {
	t.Helper()
	var chunks []*types.Chunk
	require.NoError(t, db.Where("tenant_id = ? AND knowledge_id = ?", tenantID, knowledgeID).Find(&chunks).Error)
	var urls []string
	for _, ch := range chunks {
		var infos []types.ImageInfo
		for _, ref := range types.ScanResourceReferences(ch.Content) {
			urls = append(urls, ref)
			infos = append(infos, types.ImageInfo{URL: ref, OriginalURL: ref})
		}
		if len(infos) > 0 {
			infoBytes, err := json.Marshal(infos)
			require.NoError(t, err)
			require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", ch.ID).
				Update("image_info", string(infoBytes)).Error)
		}
	}
	return urls
}

func resourceReadable(ctx context.Context, fileSvc interfaces.FileService, ref string) bool {
	rc, err := fileSvc.GetFile(ctx, ref)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	return true
}

// TestBatchReparseKeepsIndexedContentSearchableUntilWorkerSwapsIt covers #5002
// end-to-end through ProcessKnowledgeListReparse -> ReparseKnowledge ->
// ProcessDocument -> SQLite vector search + Graph Search + ResourceCatalog +
// Tenant storage quota:
//   - batch submission keeps chunks, vector hits, graph evidence, summary,
//     accounted storage and extracted images live while queued;
//   - a retryable BatchIndex error does not double-subtract StorageUsed;
//   - a workspace already >60% full passes the quota check because the
//     document's own previous StorageSize is discounted;
//   - content-deduplicated resource:// images still referenced by the new
//     parse keep their binding and bytes, while obsolete images and
//     previous-dimension vectors are cleaned up at swap time.
func TestBatchReparseKeepsIndexedContentSearchableUntilWorkerSwapsIt(t *testing.T) {
	h := newReparseE2EHarness(t)
	ctx := h.writeCtx()

	keptImageURI := encodeSolidPNGDataURI(t, color.RGBA{R: 200, G: 40, B: 40, A: 255})
	obsoleteImageURI := encodeSolidPNGDataURI(t, color.RGBA{R: 40, G: 40, B: 200, A: 255})

	initialMD := fmt.Sprintf("# Handbook\n\nalphauniquev1token\n\n![kept](%s)\n\n![obsolete](%s)\n",
		keptImageURI, obsoleteImageURI)
	indexedDoc, imageRefs := h.ingestPDF("doc-1", "handbook.pdf", initialMD, "v1 handbook summary")
	require.Len(t, imageRefs, 2)
	keptRef, obsoleteRef := imageRefs[0], imageRefs[1]
	require.True(t, resourceReadable(ctx, h.fileSvc, keptRef))
	require.True(t, resourceReadable(ctx, h.fileSvc, obsoleteRef))
	require.Positive(t, indexedDoc.StorageSize)

	// Tighten workspace quota so the already-indexed document alone occupies
	// ~65% of the quota; without discounting previousStorageSize, reparse fails.
	quota := indexedDoc.StorageSize + indexedDoc.StorageSize/2 + 256
	require.NoError(t, h.db.Model(&types.Tenant{}).Where("id = ?", h.tenant.ID).
		Update("storage_quota", quota).Error)

	// Switch the KB from 4-dim to 8-dim before triggering batch reparse.
	require.NoError(t, h.db.Model(&types.KnowledgeBase{}).Where("id = ?", h.kb.ID).
		Update("embedding_model_id", "embed-8d").Error)
	h.graphRepo.deleted = nil

	listPayload, err := json.Marshal(types.KnowledgeListReparsePayload{
		TenantID:        h.tenant.ID,
		KnowledgeBaseID: h.kb.ID,
		KnowledgeIDs:    []string{indexedDoc.ID},
	})
	require.NoError(t, err)
	require.NoError(t, h.svc.ProcessKnowledgeListReparse(context.Background(),
		asynq.NewTask(types.TypeKnowledgeListReparse, listPayload)))

	reparseTasks := h.queue.takeTasks(types.TypeDocumentProcess)
	require.Len(t, reparseTasks, 1)

	// === 1. While queued: everything retrieval serves remains intact ===
	var queuedDoc types.Knowledge
	require.NoError(t, h.db.First(&queuedDoc, "id = ?", indexedDoc.ID).Error)
	require.Equal(t, types.ParseStatusPending, queuedDoc.ParseStatus)
	require.Equal(t, "enabled", queuedDoc.EnableStatus, "Graph Search & QA listings require enable_status=enabled while queued")
	require.Equal(t, "embed-4d", queuedDoc.EmbeddingModelID, "row must still name the live 4-dim vectors until swapped")
	require.Equal(t, "v1 handbook summary", queuedDoc.Description)
	require.Equal(t, indexedDoc.StorageSize, queuedDoc.StorageSize)
	require.Empty(t, h.graphRepo.deleted, "submission must not drop graph data")

	hitsWhileQueued := h.vectorSearchWithModel("embed-4d", "alphauniquev1token")
	require.Len(t, hitsWhileQueued, 1)
	require.Contains(t, hitsWhileQueued[0].Content, "alphauniquev1token")
	require.Equal(t, "v1 handbook summary", hitsWhileQueued[0].KnowledgeDescription)

	graphRows, err := h.graphSearch.GetGraphSourceChunks(ctx, h.tenant.ID, h.kb.ID, []string{hitsWhileQueued[0].ID})
	require.NoError(t, err)
	require.Len(t, graphRows, 1, "Graph Search must keep serving live chunks while queued")
	require.True(t, resourceReadable(ctx, h.fileSvc, keptRef))
	require.True(t, resourceReadable(ctx, h.fileSvc, obsoleteRef))

	// === 2. Worker runs: retry 0 exhausts embedder backoff on 429, retry 1 succeeds ===
	h.docReader.markdown = fmt.Sprintf("# Handbook v2\n\nbetauniquev2token updated\n\n![kept](%s)\n", keptImageURI)
	h.models.models["embed-8d"].failNextN = 5

	retry0Ctx := types.WithTaskRetryMetadata(context.Background(), 0, 2)
	err = h.svc.ProcessDocument(retry0Ctx, reparseTasks[0])
	require.ErrorContains(t, err, "429 rate limit")

	var afterRetry0Tenant types.Tenant
	require.NoError(t, h.db.First(&afterRetry0Tenant, "id = ?", h.tenant.ID).Error)
	require.Equal(t, indexedDoc.StorageSize, afterRetry0Tenant.StorageUsed,
		"retryable failure must not subtract StorageSize before final state is persisted")

	retry1Ctx := types.WithTaskRetryMetadata(context.Background(), 1, 2)
	require.NoError(t, h.svc.ProcessDocument(retry1Ctx, reparseTasks[0]))
	require.NotEmpty(t, h.graphRepo.deleted, "worker swap must drop previous graph data")

	// === 3. After swap: v2 is searchable, v1 & 4d vectors & obsolete image are gone, kept image & quota are exact ===
	var swappedDoc types.Knowledge
	require.NoError(t, h.db.First(&swappedDoc, "id = ?", indexedDoc.ID).Error)
	require.Equal(t, "enabled", swappedDoc.EnableStatus)
	require.Equal(t, "embed-8d", swappedDoc.EmbeddingModelID)
	require.Empty(t, swappedDoc.Description, "previous summary clears when new chunks replace old content")
	require.Positive(t, swappedDoc.StorageSize)

	var finalTenant types.Tenant
	require.NoError(t, h.db.First(&finalTenant, "id = ?", h.tenant.ID).Error)
	require.Equal(t, swappedDoc.StorageSize, finalTenant.StorageUsed,
		"tenant StorageUsed must equal the replacement StorageSize (adjusted by delta, never inflated or negative)")

	require.Empty(t, h.vectorSearchWithModel("embed-4d", "alphauniquev1token"),
		"4-dim vectors from previous embedding model must be deleted during swap")
	v2Hits := h.vectorSearchWithModel("embed-8d", "betauniquev2token")
	require.Len(t, v2Hits, 1)
	require.Contains(t, v2Hits[0].Content, "betauniquev2token")
	require.NotContains(t, v2Hits[0].Content, "alphauniquev1token")

	require.True(t, resourceReadable(ctx, h.fileSvc, keptRef),
		"content-deduplicated resource:// image still referenced in v2 must survive swap")
	require.False(t, resourceReadable(ctx, h.fileSvc, obsoleteRef),
		"extracted image removed in v2 must be deleted during swap")
}

// TestReparseParseFailurePreservesPreviousSearchableContent reproduces the
// failure variant from #5002: DocReader goes down after reparse submission and
// ProcessDocument exhausts its retries. The row records ParseStatus=failed with
// the DocReader error, while its previous chunks, vector index, Graph Search
// evidence, extracted images and accounted storage all stay live.
func TestReparseParseFailurePreservesPreviousSearchableContent(t *testing.T) {
	h := newReparseE2EHarness(t)
	ctx := h.writeCtx()

	imgURI := encodeSolidPNGDataURI(t, color.RGBA{R: 50, G: 180, B: 80, A: 255})
	initialMD := fmt.Sprintf("# Spec\n\nsurvivortoken5002\n\n![diagram](%s)\n", imgURI)
	indexedDoc, imageRefs := h.ingestPDF("doc-fail", "spec.pdf", initialMD, "spec summary")
	require.Len(t, imageRefs, 1)

	submitted, err := h.svc.ReparseKnowledge(ctx, indexedDoc.ID, nil)
	require.NoError(t, err)
	require.NotNil(t, submitted)
	reparseTasks := h.queue.takeTasks(types.TypeDocumentProcess)
	require.Len(t, reparseTasks, 1)

	// DocReader goes down while the task is queued and fails through final retry.
	h.docReader.readErr = status.Error(codes.Unavailable, "connection error: dial tcp 127.0.0.1:50051: connect: connection refused")
	finalRetryCtx := types.WithTaskRetryMetadata(context.Background(), 3, 3)
	_ = h.svc.ProcessDocument(finalRetryCtx, reparseTasks[0])

	var failedDoc types.Knowledge
	require.NoError(t, h.db.First(&failedDoc, "id = ?", indexedDoc.ID).Error)
	require.Equal(t, types.ParseStatusFailed, failedDoc.ParseStatus)
	require.NotEmpty(t, failedDoc.ErrorMessage)
	require.Equal(t, "enabled", failedDoc.EnableStatus, "previous content is still live, so enable_status stays enabled")
	require.Equal(t, "spec summary", failedDoc.Description)
	require.Equal(t, indexedDoc.StorageSize, failedDoc.StorageSize)

	var tenantAfter types.Tenant
	require.NoError(t, h.db.First(&tenantAfter, "id = ?", h.tenant.ID).Error)
	require.Equal(t, indexedDoc.StorageSize, tenantAfter.StorageUsed)

	hits := h.vectorSearchWithModel("embed-4d", "survivortoken5002")
	require.Len(t, hits, 1, "document must keep answering vector searches after a failed reparse")
	graphRows, err := h.graphSearch.GetGraphSourceChunks(ctx, h.tenant.ID, h.kb.ID, []string{hits[0].ID})
	require.NoError(t, err)
	require.Len(t, graphRows, 1, "document must keep answering Graph Search after a failed reparse")
	require.True(t, resourceReadable(ctx, h.fileSvc, imageRefs[0]),
		"extracted image from the surviving parse must still be served")
}

// TestSupersededReparseWorkerDoesNotClobberNewerIndexedContent verifies that if
// an earlier reparse attempt finishes convert() after a newer attempt has
// already indexed its chunks and images, the stale attempt exits via
// processChunks's supersede guard before deleting anything.
func TestSupersededReparseWorkerDoesNotClobberNewerIndexedContent(t *testing.T) {
	h := newReparseE2EHarness(t)
	ctx := h.writeCtx()

	v1Img := encodeSolidPNGDataURI(t, color.RGBA{R: 120, G: 120, B: 10, A: 255})
	indexedDoc, _ := h.ingestPDF("doc-race", "race.pdf", fmt.Sprintf("# V1\n\nv1racetoken\n\n![v1](%s)\n", v1Img), "v1")

	// Reparse #1 (Attempt 2) is submitted.
	_, err := h.svc.ReparseKnowledge(ctx, indexedDoc.ID, nil)
	require.NoError(t, err)
	attempt2Tasks := h.queue.takeTasks(types.TypeDocumentProcess)
	require.Len(t, attempt2Tasks, 1)

	// While Attempt 2 is mid-convert(), a newer reparse (Attempt 3) is submitted
	// and completes with fresh content and its own extracted image.
	v3Img := encodeSolidPNGDataURI(t, color.RGBA{R: 10, G: 190, B: 190, A: 255})
	var v3ImageRef string
	var v3StorageSize int64
	h.docReader.markdown = "# Stale Attempt 2\n\nstaleattempt2token\n"
	h.docReader.onRead = func() {
		h.docReader.onRead = nil
		_, repErr := h.svc.ReparseKnowledge(h.writeCtx(), indexedDoc.ID, nil)
		require.NoError(t, repErr)
		attempt3Tasks := h.queue.takeTasks(types.TypeDocumentProcess)
		require.Len(t, attempt3Tasks, 1)

		h.docReader.markdown = fmt.Sprintf("# Fresh Attempt 3\n\nfreshattempt3token\n\n![v3](%s)\n", v3Img)
		require.NoError(t, h.svc.ProcessDocument(context.Background(), attempt3Tasks[0]))
		_ = h.queue.takeTasks(types.TypeKnowledgePostProcess)

		refs := attachExtractedImagesToChunks(t, h.db, h.tenant.ID, indexedDoc.ID)
		require.Len(t, refs, 1)
		v3ImageRef = refs[0]

		var completed types.Knowledge
		require.NoError(t, h.db.First(&completed, "id = ?", indexedDoc.ID).Error)
		v3StorageSize = completed.StorageSize
	}

	// Run Attempt 2: convert() returns "staleattempt2token", then Attempt 3
	// completes inside onRead, and Attempt 2 enters processChunks afterwards.
	require.NoError(t, h.svc.ProcessDocument(context.Background(), attempt2Tasks[0]))

	v3Hits := h.vectorSearchWithModel("embed-4d", "freshattempt3token")
	require.Len(t, v3Hits, 1, "stale Attempt 2 must not delete Attempt 3's chunks or vectors")
	require.Contains(t, v3Hits[0].Content, "freshattempt3token")
	require.True(t, resourceReadable(ctx, h.fileSvc, v3ImageRef),
		"stale Attempt 2 must not delete Attempt 3's extracted image")

	var finalTenant types.Tenant
	require.NoError(t, h.db.First(&finalTenant, "id = ?", h.tenant.ID).Error)
	require.Equal(t, v3StorageSize, finalTenant.StorageUsed,
		"stale Attempt 2 must not subtract Attempt 3's StorageUsed")
}

// TestReplaceKnowledgeFileRemovesRetiredSourceContentSynchronously verifies
// that ReplaceKnowledgeFile (tasksDequeued=true) still cleans up the retired
// file's chunks, index, images and storage synchronously so old file content
// is never served under the replacement filename while queued.
func TestReplaceKnowledgeFileRemovesRetiredSourceContentSynchronously(t *testing.T) {
	h := newReparseE2EHarness(t)
	ctx := h.writeCtx()

	oldImg := encodeSolidPNGDataURI(t, color.RGBA{R: 90, G: 20, B: 150, A: 255})
	indexedDoc, oldRefs := h.ingestPDF("doc-replace", "confidential-v1.pdf",
		fmt.Sprintf("# Secret\n\nsecretv1token\n\n![old](%s)\n", oldImg), "secret summary")
	require.Len(t, oldRefs, 1)
	require.Len(t, h.vectorSearchWithModel("embed-4d", "secretv1token"), 1)

	replacementFH, err := bytesToFileHeader([]byte("# Public Handbook\n\npublicv2token\n"), "public-v2.md")
	require.NoError(t, err)
	replaced, err := h.svc.ReplaceKnowledgeFile(ctx, indexedDoc.ID, replacementFH, "public-v2.md", nil)
	require.NoError(t, err)
	require.Equal(t, types.ParseStatusPending, replaced.ParseStatus)
	require.Equal(t, "disabled", replaced.EnableStatus)

	// Before the replacement worker runs, the retired file's content is already gone.
	require.Empty(t, h.vectorSearchWithModel("embed-4d", "secretv1token"),
		"retired file's chunks and vectors must not be served under the new filename")
	require.False(t, resourceReadable(ctx, h.fileSvc, oldRefs[0]),
		"retired file's extracted image must be removed synchronously")

	var midTenant types.Tenant
	require.NoError(t, h.db.First(&midTenant, "id = ?", h.tenant.ID).Error)
	require.Zero(t, midTenant.StorageUsed)

	// Running the queued replacement worker indexes the new file cleanly.
	tasks := h.queue.takeTasks(types.TypeDocumentProcess)
	require.Len(t, tasks, 1)
	require.NoError(t, h.svc.ProcessDocument(context.Background(), tasks[0]))

	v2Hits := h.vectorSearchWithModel("embed-4d", "publicv2token")
	require.Len(t, v2Hits, 1)
	require.Equal(t, "public-v2.md", v2Hits[0].KnowledgeFilename)
}
