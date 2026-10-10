package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type inlineImageFileService struct {
	interfaces.FileService
	content string
	state   *inlineImageStorageState
}

type inlineImageStorageState struct {
	nextID    int
	failAt    int
	saveCalls int
	saved     []string
	deleted   []string
	catalog   *fakeBindCatalog
}

func (s inlineImageFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(s.content)), nil
}

func (s inlineImageFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	if s.state == nil {
		return "local://test/image.png", nil
	}
	s.state.saveCalls++
	if s.state.failAt > 0 && s.state.saveCalls == s.state.failAt {
		return "", errors.New("temporary object storage failure")
	}
	s.state.nextID++
	servingURL := fmt.Sprintf("local://test/image-%03d.png", s.state.nextID)
	s.state.saved = append(s.state.saved, servingURL)
	if s.state.catalog != nil {
		if s.state.catalog.resources == nil {
			s.state.catalog.resources = make(map[string]*types.StoredResource)
		}
		s.state.catalog.resources[servingURL] = &types.StoredResource{
			ID: fmt.Sprintf("resource-%03d", s.state.nextID), TenantID: 1,
		}
	}
	return servingURL, nil
}

func (s inlineImageFileService) DeleteFile(_ context.Context, filePath string) error {
	if s.state != nil {
		s.state.deleted = append(s.state.deleted, filePath)
	}
	return nil
}

type inlineImageChunkRepo struct {
	interfaces.ChunkRepository
	deleted bool
	created bool
	chunks  []*types.Chunk
}

type inlineImageTenantService struct{ interfaces.TenantService }

type inlineImageModelService struct {
	interfaces.ModelService
	calls int
}

func (s *inlineImageModelService) GetEmbeddingModel(context.Context, string) (embedding.Embedder, error) {
	s.calls++
	return nil, errors.New("embedding must not run")
}

type inlineImageTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r *inlineImageTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return r.tenant, nil
}

func (*inlineImageTenantRepo) AdjustStorageUsed(context.Context, uint64, int64) error { return nil }

type inlineImageSpanTracker struct {
	noopSpanTracker
	chunkingStarted bool
	chunkingFailed  bool
	chunking        *Span
}

func (t *inlineImageSpanTracker) BeginStage(
	_ context.Context, knowledgeID string, attempt int, stage string, _ types.JSONMap,
) *Span {
	if stage != types.StageChunking {
		return nil
	}
	t.chunkingStarted = true
	t.chunking = &Span{KnowledgeID: knowledgeID, Attempt: attempt, Name: stage, Kind: types.SpanKindStage}
	return t.chunking
}

func (t *inlineImageSpanTracker) LookupStage(context.Context, string, int, string) *Span {
	return t.chunking
}

func (t *inlineImageSpanTracker) FailSpan(context.Context, *Span, string, string, error) {
	t.chunkingFailed = true
}

func (r *inlineImageChunkRepo) DeleteChunksByKnowledgeID(context.Context, uint64, string) error {
	r.deleted = true
	return nil
}

func (r *inlineImageChunkRepo) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = true
	r.chunks = append([]*types.Chunk(nil), chunks...)
	return nil
}

func TestProcessDocumentChunksStoredInlineImagesOverLegacyLimit(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	image := `<img src="data:image/png;base64,` + b64 + `">`

	for _, count := range []int{31, 81} {
		t.Run(fmt.Sprintf("%d images", count), func(t *testing.T) {
			knowledge := &types.Knowledge{
				ID:              "knowledge-1",
				TenantID:        1,
				KnowledgeBaseID: "kb-1",
				FilePath:        "input.md",
				ParseStatus:     types.ParseStatusPending,
			}
			repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
			chunkRepo := &inlineImageChunkRepo{}
			tracker := &inlineImageSpanTracker{}
			catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
			storage := &inlineImageStorageState{catalog: catalog}
			fileSvc := inlineImageFileService{content: strings.Repeat(image+"\n", count), state: storage}
			svc := &knowledgeService{
				repo:            repo,
				kbService:       &reparseFailureKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}},
				tenantRepo:      &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
				tenantService:   &inlineImageTenantService{},
				fileSvc:         fileSvc,
				imageResolver:   docparser.NewImageResolver(),
				chunkRepo:       chunkRepo,
				graphEngine:     parentChildGraphRepo{},
				spanTracker:     tracker,
				resourceCatalog: catalog,
			}
			payload := types.DocumentProcessPayload{
				TenantID: 1, KnowledgeID: knowledge.ID, KnowledgeBaseID: "kb-1",
				FilePath: "input.md", FileName: "input.md", FileType: "md", Attempt: 1,
			}
			task := asynq.NewTask(types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload))

			err := svc.ProcessDocument(context.Background(), task)

			require.NoError(t, err)
			require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus)
			require.True(t, chunkRepo.created, "stored inline images must be chunked")
			require.True(t, tracker.chunkingStarted, "the chunking stage must appear in the trace")
			require.False(t, tracker.chunkingFailed, "the chunking stage must not be marked failed")
			require.Len(t, storage.saved, count, "each inline image must be stored")
			require.Len(t, uniqueInlineStrings(storage.saved), count, "each stored image must have a unique URL")
			require.Len(t, catalog.binds, count, "all stored images, including over-limit ones, must be bound")
			var chunkText strings.Builder
			for _, chunk := range chunkRepo.chunks {
				chunkText.WriteString(chunk.Content)
			}
			require.NotContains(t, chunkText.String(), "data:image",
				"chunk text must not contain inline image payloads")
			require.NotContains(t, chunkText.String(), "base64,", "chunk text must not contain inline base64")
			for _, servingURL := range storage.saved {
				require.Contains(t, chunkText.String(), servingURL, "stored image URL must remain in chunk text")
			}
		})
	}
}

func TestProcessDocumentChunksCodeExampleAndStoresOnlyRealInlineImage(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	codeExample := "Use `data:image/png;base64," + b64 + "` to illustrate a data URI."
	realImage := "![](data:image/png;base64," + b64 + ")"
	knowledge := &types.Knowledge{
		ID: "knowledge-inline-code-example", TenantID: 1, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", ParseStatus: types.ParseStatusPending,
	}
	chunkRepo := &inlineImageChunkRepo{}
	tracker := &inlineImageSpanTracker{}
	catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
	storage := &inlineImageStorageState{catalog: catalog}
	svc := &knowledgeService{
		repo:            &embedFailureKnowledgeRepo{knowledge: knowledge},
		kbService:       &reparseFailureKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 1}},
		tenantRepo:      &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
		tenantService:   &inlineImageTenantService{},
		fileSvc:         inlineImageFileService{content: codeExample + "\n\n" + realImage, state: storage},
		imageResolver:   docparser.NewImageResolver(),
		chunkRepo:       chunkRepo,
		graphEngine:     parentChildGraphRepo{},
		spanTracker:     tracker,
		resourceCatalog: catalog,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 1, KnowledgeID: knowledge.ID, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", FileName: "input.md", FileType: "md", Attempt: 1,
	}

	err := svc.ProcessDocument(context.Background(), asynq.NewTask(
		types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload)))

	require.NoError(t, err)
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.True(t, chunkRepo.created, "code example must reach chunking")
	require.True(t, tracker.chunkingStarted)
	require.False(t, tracker.chunkingFailed)
	require.Len(t, storage.saved, 1, "only the real inline image should be stored")
	require.Len(t, catalog.binds, 1)
	var chunkText strings.Builder
	for _, chunk := range chunkRepo.chunks {
		chunkText.WriteString(chunk.Content)
	}
	require.Contains(t, chunkText.String(), codeExample, "code example must remain literal text")
	require.Contains(t, chunkText.String(), storage.saved[0], "real image must be replaced with its stored URL")
}

func TestProcessDocumentBlocksUnresolvedImageDataURIBeforeChunking(t *testing.T) {
	knowledge := &types.Knowledge{
		ID: "knowledge-unresolved-inline-image", TenantID: 1, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", ParseStatus: types.ParseStatusPending,
	}
	chunkRepo := &inlineImageChunkRepo{}
	modelService := &inlineImageModelService{}
	svc := &knowledgeService{
		repo: &embedFailureKnowledgeRepo{knowledge: knowledge},
		kbService: &reparseFailureKBService{kb: &types.KnowledgeBase{
			ID: "kb-1", TenantID: 1, EmbeddingModelID: "embedding-1",
			IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		}},
		tenantRepo:    &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
		tenantService: &inlineImageTenantService{},
		fileSvc: inlineImageFileService{
			content: "Actual image: data:image/png;base64,not-valid-image-payload",
		},
		imageResolver: docparser.NewImageResolver(),
		chunkRepo:     chunkRepo,
		graphEngine:   parentChildGraphRepo{},
		modelService:  modelService,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 1, KnowledgeID: knowledge.ID, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", FileName: "input.md", FileType: "md", Attempt: 1,
	}

	err := svc.ProcessDocument(context.Background(), asynq.NewTask(
		types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload)))

	require.ErrorIs(t, err, asynq.SkipRetry)
	require.Equal(t, types.ParseStatusFailed, knowledge.ParseStatus)
	require.False(t, chunkRepo.created)
	require.Equal(t, 0, modelService.calls, "embedding must not run after unresolved image detection")
}

func TestProcessDocumentEnqueuesOnlyInlineImagesWithinEachSyntaxBudget(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	markdownImage := `![](data:image/png;base64,` + b64 + `)`
	htmlImage := `<img src="data:image/png;base64,` + b64 + `">`
	content := strings.Repeat(markdownImage+"\n", 31) + strings.Repeat(htmlImage+"\n", 31)
	knowledge := &types.Knowledge{
		ID: "knowledge-mixed-inline", TenantID: 1, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", ParseStatus: types.ParseStatusPending,
	}
	catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
	storage := &inlineImageStorageState{catalog: catalog}
	chunkRepo := &inlineImageChunkRepo{}
	enqueuer := &slotReleaseEnqueuer{}
	useLiteCounter(t, knowledge.ID)
	svc := &knowledgeService{
		repo: &embedFailureKnowledgeRepo{knowledge: knowledge},
		kbService: &reparseFailureKBService{kb: &types.KnowledgeBase{
			ID: "kb-1", TenantID: 1, VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"},
		}},
		tenantRepo:      &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
		tenantService:   &inlineImageTenantService{},
		fileSvc:         inlineImageFileService{content: content, state: storage},
		imageResolver:   docparser.NewImageResolver(),
		chunkRepo:       chunkRepo,
		graphEngine:     parentChildGraphRepo{},
		resourceCatalog: catalog,
		task:            enqueuer,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 1, KnowledgeID: knowledge.ID, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", FileName: "input.md", FileType: "md", Attempt: 1,
		EnableMultimodel: true,
	}

	err := svc.ProcessDocument(context.Background(), asynq.NewTask(
		types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload)))

	require.NoError(t, err)
	require.Len(t, storage.saved, 62)
	require.Len(t, uniqueInlineStrings(storage.saved), 62)
	require.Len(t, catalog.binds, 62)
	require.Equal(t, 60, enqueuer.imageEnqueued, "Markdown and HTML each keep their own 30-image quota")
	count, ok := liteCounter(knowledge.ID)
	require.True(t, ok)
	require.Equal(t, int64(60), count, "pending count must match the 60 scheduled tasks")
	require.True(t, chunkRepo.created)
	var chunkText strings.Builder
	for _, chunk := range chunkRepo.chunks {
		chunkText.WriteString(chunk.Content)
	}
	require.NotContains(t, chunkText.String(), "data:image")
	require.NotContains(t, chunkText.String(), "base64,")
}

type inlineLifecycleFileService struct {
	inlineImageFileService
	imageBytes []byte
}

func (s inlineLifecycleFileService) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	if path == "input.md" {
		return io.NopCloser(strings.NewReader(s.content)), nil
	}
	return io.NopCloser(bytes.NewReader(s.imageBytes)), nil
}

type inlineLifecycleTaskEnqueuer struct {
	interfaces.TaskEnqueuer
	imageTasks       []*asynq.Task
	postProcessTasks []*asynq.Task
}

func (e *inlineLifecycleTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	switch task.Type() {
	case types.TypeImageMultimodal:
		e.imageTasks = append(e.imageTasks, task)
	case types.TypeKnowledgePostProcess:
		e.postProcessTasks = append(e.postProcessTasks, task)
	}
	return &asynq.TaskInfo{ID: task.Type()}, nil
}

type inlineLifecycleKnowledgeRepo struct {
	embedFailureKnowledgeRepo
}

func (r *inlineLifecycleKnowledgeRepo) GetKnowledgeByIDOnly(
	context.Context, string,
) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *inlineLifecycleKnowledgeRepo) CompleteProcessingWithoutSubtasks(
	context.Context, string,
) (bool, error) {
	if r.knowledge.ParseStatus != types.ParseStatusProcessing {
		return false, nil
	}
	r.knowledge.ParseStatus = types.ParseStatusCompleted
	return true, nil
}

type inlineLifecycleKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *inlineLifecycleKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func (s *inlineLifecycleKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

const (
	inlineLifecycleOCRText     = "inline lifecycle OCR text extracted from the document image"
	inlineLifecycleCaptionText = "inline lifecycle caption describing the document image"
)

type inlineLifecycleVLM struct{ captionCalls, ocrCalls int }

func (m *inlineLifecycleVLM) Predict(_ context.Context, _ [][]byte, prompt string) (string, error) {
	if strings.Contains(prompt, "OCR assistant") {
		m.ocrCalls++
		return inlineLifecycleOCRText, nil
	}
	m.captionCalls++
	return inlineLifecycleCaptionText, nil
}

func (*inlineLifecycleVLM) GetModelName() string { return "inline-lifecycle-vlm" }
func (*inlineLifecycleVLM) GetModelID() string   { return "inline-lifecycle-vlm" }

type inlineLifecycleChunkRepo struct {
	*inlineImageChunkRepo
}

// CreateChunks appends across calls: 30 image tasks each persist their child
// chunks through this repo, and the test inspects the full set afterwards.
func (r *inlineLifecycleChunkRepo) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	r.created = true
	r.chunks = append(r.chunks, chunks...)
	return nil
}

func (*inlineLifecycleChunkRepo) UpdateChunk(context.Context, *types.Chunk) error { return nil }

type inlineLifecycleChunkService struct {
	interfaces.ChunkService
	repo *inlineLifecycleChunkRepo
}

func (s *inlineLifecycleChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

func (s *inlineLifecycleChunkService) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	for _, chunk := range s.repo.chunks {
		if chunk.ID == id {
			return chunk, nil
		}
	}
	return nil, fmt.Errorf("chunk %s not found", id)
}

type inlineLifecycleModelService struct {
	interfaces.ModelService
	model vlm.VLM
}

func (s inlineLifecycleModelService) GetVLMModel(context.Context, string) (vlm.VLM, error) {
	return s.model, nil
}

func TestProcessDocumentInlineImageTasksCompleteKnowledge(t *testing.T) {
	const knowledgeID = "knowledge-inline-image-lifecycle"
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	image := `<img src="data:image/png;base64,` + b64 + `">`
	content := strings.Repeat(image+"\n", 81)
	knowledge := &types.Knowledge{
		ID: knowledgeID, TenantID: 1, KnowledgeBaseID: "kb-inline-lifecycle",
		FilePath: "input.md", ParseStatus: types.ParseStatusPending,
	}
	require.NoError(t, knowledge.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		SummaryEnabled: processConfigBoolPtr(false),
	}))
	kb := &types.KnowledgeBase{
		ID: knowledge.KnowledgeBaseID, TenantID: knowledge.TenantID,
		Type:      types.KnowledgeBaseTypeDocument,
		VLMConfig: types.VLMConfig{Enabled: true, ModelID: "inline-lifecycle-vlm"},
	}
	repo := &inlineLifecycleKnowledgeRepo{embedFailureKnowledgeRepo: embedFailureKnowledgeRepo{knowledge: knowledge}}
	kbService := &inlineLifecycleKBService{kb: kb}
	catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
	storage := &inlineImageStorageState{catalog: catalog}
	fileSvc := inlineLifecycleFileService{
		inlineImageFileService: inlineImageFileService{content: content, state: storage},
		imageBytes:             inlineImageTestPNG(t),
	}
	chunkRepo := &inlineImageChunkRepo{}
	queue := &inlineLifecycleTaskEnqueuer{}
	useLiteCounter(t, knowledgeID)
	processService := &knowledgeService{
		repo: repo, kbService: kbService,
		tenantRepo:    &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
		tenantService: &inlineImageTenantService{},
		fileSvc:       fileSvc, imageResolver: docparser.NewImageResolver(),
		chunkRepo: chunkRepo, graphEngine: parentChildGraphRepo{},
		resourceCatalog: catalog, task: queue,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 1, KnowledgeID: knowledgeID, KnowledgeBaseID: kb.ID,
		FilePath: "input.md", FileName: "input.md", FileType: "md", Attempt: 1,
		EnableMultimodel: true,
	}

	require.NoError(t, processService.ProcessDocument(context.Background(), asynq.NewTask(
		types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload))))
	require.Len(t, storage.saved, 81)
	require.Len(t, catalog.binds, 81)
	require.Len(
		t, queue.imageTasks, 30,
		"only the first 30 HTML inline images in document order should run through multimodal processing",
	)
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus,
		"knowledge must stay processing while image tasks are queued")
	pending, ok := liteCounter(knowledgeID)
	require.True(t, ok)
	require.Equal(t, int64(30), pending)

	model := &inlineLifecycleVLM{}
	imageService := &ImageMultimodalService{
		chunkService: &inlineLifecycleChunkService{
			repo: &inlineLifecycleChunkRepo{inlineImageChunkRepo: chunkRepo},
		},
		knowledgeRepo: repo,
		kbService:     kbService,
		modelService:  inlineLifecycleModelService{model: model},
		tenantRepo:    &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
		fileSvc:       fileSvc,
		taskEnqueuer:  queue,
	}
	for _, task := range queue.imageTasks {
		require.NoError(t, imageService.Handle(context.Background(), task))
	}
	require.Equal(t, 30, model.captionCalls, "each image task should complete one caption call")
	require.Equal(t, 30, model.ocrCalls, "each image task should complete one OCR call")

	ownerByURL := make(map[string]string, len(queue.imageTasks))
	ownedImages := 0
	for _, task := range queue.imageTasks {
		var payload types.ImageMultimodalPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &payload))
		ownerByURL[payload.ImageURL] = payload.ChunkID
		if payload.ChunkID != "" {
			ownedImages++
		}
	}
	require.Positive(t, ownedImages, "scheduled images should mostly resolve to an owning text chunk")
	chunksByID := make(map[string]*types.Chunk, len(chunkRepo.chunks))
	ocrChunks, captionChunks := 0, 0
	for _, chunk := range chunkRepo.chunks {
		chunksByID[chunk.ID] = chunk
		switch chunk.ChunkType {
		case types.ChunkTypeImageOCR:
			ocrChunks++
			require.Equal(t, inlineLifecycleOCRText, chunk.Content)
		case types.ChunkTypeImageCaption:
			captionChunks++
			require.Equal(t, inlineLifecycleCaptionText, chunk.Content)
		}
	}
	require.Equal(t, 30, ocrChunks, "each scheduled image should produce an OCR chunk from the VLM output")
	require.Equal(t, 30, captionChunks, "each scheduled image should produce a caption chunk from the VLM output")
	for _, chunk := range chunkRepo.chunks {
		if chunk.ChunkType != types.ChunkTypeImageOCR && chunk.ChunkType != types.ChunkTypeImageCaption {
			continue
		}
		var infos []types.ImageInfo
		require.NoError(t, json.Unmarshal([]byte(chunk.ImageInfo), &infos))
		require.Len(t, infos, 1)
		owner, ok := ownerByURL[infos[0].URL]
		require.True(t, ok, "multimodal chunk %s must reference one of the scheduled images", chunk.ID)
		require.Equal(t, owner, chunk.ParentChunkID)
		if owner == "" {
			// imageChunkOwner leaves ownership unset when chunk overlap puts the
			// same URL in more than one chunk — the child stays unattached.
			continue
		}
		parent, ok := chunksByID[chunk.ParentChunkID]
		require.True(t, ok, "parent chunk %s must exist", chunk.ParentChunkID)
		require.Contains(t, parent.Content, infos[0].URL,
			"the parent text chunk must be the one carrying the image")
	}
	_, ok = liteCounter(knowledgeID)
	require.False(t, ok, "the pending counter should be removed when all 30 image tasks finish")
	require.Len(t, queue.postProcessTasks, 1,
		"the last completed image task should enqueue exactly one post-process task")
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus,
		"image completion should hand off to post-process before marking the document complete")

	postProcessRepo := &wikiEnqueueFailureChunkRepo{chunks: chunkRepo.chunks}
	postProcessService := &KnowledgePostProcessService{
		knowledgeRepo: repo,
		kbService:     kbService,
		chunkRepo:     postProcessRepo,
		taskEnqueuer:  queue,
	}
	require.NoError(t, postProcessService.Handle(context.Background(), queue.postProcessTasks[0]))
	require.Equal(t, types.ParseStatusCompleted, knowledge.ParseStatus,
		"post-process should move the document to Completed after all image work finishes")
}

func TestTriggerManualProcessingStoresAndLimitsInlineImages(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	image := `<img src="data:image/png;base64,` + b64 + `">`
	content := strings.Repeat(image+"\n", 81)
	knowledge := &types.Knowledge{
		ID: "knowledge-manual-inline", TenantID: 1, KnowledgeBaseID: "kb-1",
		ParseStatus: types.ParseStatusProcessing,
	}
	catalog := &fakeBindCatalog{resources: make(map[string]*types.StoredResource)}
	storage := &inlineImageStorageState{catalog: catalog}
	chunkRepo := &inlineImageChunkRepo{}
	enqueuer := &slotReleaseEnqueuer{}
	useLiteCounter(t, knowledge.ID)
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 1})
	svc := &knowledgeService{
		repo:            &embedFailureKnowledgeRepo{knowledge: knowledge},
		fileSvc:         inlineImageFileService{state: storage},
		imageResolver:   docparser.NewImageResolver(),
		resourceCatalog: catalog,
		chunkRepo:       chunkRepo,
		modelService:    embedFailureModelService{err: errors.New("embedding should be disabled")},
		graphEngine:     parentChildGraphRepo{},
		tenantRepo:      parentChildTenantRepo{},
		task:            enqueuer,
	}
	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, VLMConfig: types.VLMConfig{Enabled: true, ModelID: "vlm"},
	}

	err := svc.triggerManualProcessing(ctx, kb, knowledge, content, true)

	require.NoError(t, err)
	require.Len(t, storage.saved, 81)
	require.Len(t, uniqueInlineStrings(storage.saved), 81)
	require.Len(t, catalog.binds, 81, "all images must be bound, including the 51 beyond the AI quota")
	require.Equal(t, 30, enqueuer.imageEnqueued)
	count, ok := liteCounter(knowledge.ID)
	require.True(t, ok)
	require.Equal(t, int64(30), count, "pending count must match the 30 scheduled tasks")
	require.True(t, chunkRepo.created)
	var chunkText strings.Builder
	for _, chunk := range chunkRepo.chunks {
		chunkText.WriteString(chunk.Content)
	}
	require.NotContains(t, chunkText.String(), "data:image")
	require.NotContains(t, chunkText.String(), "base64,")
	for _, servingURL := range storage.saved {
		require.Contains(t, chunkText.String(), servingURL)
	}
}

func TestTriggerManualProcessingRetriesAndRollsBackStorageFailure(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	image := `<img src="data:image/png;base64,` + b64 + `">`
	knowledge := &types.Knowledge{
		ID: "knowledge-manual-storage-failure", TenantID: 1, KnowledgeBaseID: "kb-1",
		ParseStatus: types.ParseStatusProcessing,
	}
	storage := &inlineImageStorageState{failAt: 2}
	chunkRepo := &inlineImageChunkRepo{}
	repo := &embedFailureKnowledgeRepo{knowledge: knowledge}
	ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, &types.Tenant{ID: 1})
	ctx = types.WithTaskRetryMetadata(ctx, 0, 3)
	svc := &knowledgeService{
		repo:          repo,
		fileSvc:       inlineImageFileService{state: storage},
		imageResolver: docparser.NewImageResolver(),
		chunkRepo:     chunkRepo,
	}
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}

	err := svc.triggerManualProcessing(ctx, kb, knowledge, strings.Repeat(image+"\n", 2), true)

	require.ErrorContains(t, err, "temporary object storage failure")
	require.False(t, errors.Is(err, asynq.SkipRetry), "temporary storage failures must be retried")
	require.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus,
		"the row must remain processing until retries are exhausted")
	require.Empty(t, repo.updates, "do not persist a terminal failure on a retryable attempt")
	require.False(t, chunkRepo.created, "manual content must not reach chunking after storage failure")
	require.Len(t, storage.saved, 1)
	require.Equal(t, storage.saved, storage.deleted,
		"the successful partial save must be removed before the retry")
}

func TestImagesForMultimodalKeepsOtherImageSourcesUnchanged(t *testing.T) {
	images := make([]docparser.StoredImage, 0, 161)
	for i := 0; i < 81; i++ {
		images = append(images, docparser.StoredImage{
			ServingURL:     fmt.Sprintf("local://inline/%d", i),
			Inline:         true,
			SkipMultimodal: i >= 30,
		})
	}
	for i := 0; i < 50; i++ {
		images = append(images, docparser.StoredImage{ServingURL: fmt.Sprintf("local://pdf/%d", i)})
	}
	for i := 0; i < 30; i++ {
		images = append(images, docparser.StoredImage{ServingURL: fmt.Sprintf("local://remote/%d", i)})
	}

	selected := imagesForMultimodal(images)
	require.Len(t, selected, 110)
	for _, image := range selected {
		require.False(t, image.SkipMultimodal)
	}
}

func TestProcessDocumentRetriesInlineImageStorageFailure(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString(inlineImageTestPNG(t))
	image := `<img src="data:image/png;base64,` + b64 + `">`
	storage := &inlineImageStorageState{failAt: 2}
	knowledge := &types.Knowledge{
		ID: "knowledge-storage-failure", TenantID: 1, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", ParseStatus: types.ParseStatusPending,
	}
	chunkRepo := &inlineImageChunkRepo{}
	modelService := &inlineImageModelService{}
	svc := &knowledgeService{
		repo: &embedFailureKnowledgeRepo{knowledge: knowledge},
		kbService: &reparseFailureKBService{kb: &types.KnowledgeBase{
			ID: "kb-1", TenantID: 1, EmbeddingModelID: "embedding-1",
			IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		}},
		tenantRepo:    &inlineImageTenantRepo{tenant: &types.Tenant{ID: 1}},
		tenantService: &inlineImageTenantService{},
		fileSvc:       inlineImageFileService{content: strings.Repeat(image+"\n", 2), state: storage},
		imageResolver: docparser.NewImageResolver(),
		chunkRepo:     chunkRepo,
		graphEngine:   parentChildGraphRepo{},
		modelService:  modelService,
	}
	payload := types.DocumentProcessPayload{
		TenantID: 1, KnowledgeID: knowledge.ID, KnowledgeBaseID: "kb-1",
		FilePath: "input.md", FileName: "input.md", FileType: "md", Attempt: 1,
	}

	ctx := types.WithTaskRetryMetadata(context.Background(), 0, 3)
	err := svc.ProcessDocument(ctx, asynq.NewTask(
		types.TypeDocumentProcess, mustMarshalDocumentPayload(t, payload)))

	require.ErrorContains(t, err, "temporary object storage failure")
	require.False(t, errors.Is(err, asynq.SkipRetry))
	require.False(t, chunkRepo.created)
	require.Equal(t, 0, modelService.calls, "embedding must not be reached after storage failure")
	require.NotEqual(t, types.ParseStatusFailed, knowledge.ParseStatus,
		"a retryable storage failure must not mark the document terminally failed")
	require.Len(t, storage.saved, 1)
	require.Equal(t, storage.saved, storage.deleted, "partial inline saves must be rolled back before retry")
}

func uniqueInlineStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	return keysOfStringSet(seen)
}

func keysOfStringSet(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	return keys
}

func inlineImageTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 200, 150))
	for y := 0; y < 150; y++ {
		for x := 0; x < 200; x++ {
			img.Set(x, y, color.RGBA{R: 128, G: 128, B: 128, A: 255})
		}
	}
	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, img))
	return out.Bytes()
}

func mustMarshalDocumentPayload(t *testing.T, payload types.DocumentProcessPayload) []byte {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return data
}
