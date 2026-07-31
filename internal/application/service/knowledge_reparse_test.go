package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type manualReparseTaskEnqueuer struct {
	task *asynq.Task
}

func (e *manualReparseTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.task = task
	return &asynq.TaskInfo{ID: "task-1"}, nil
}

type allocatingManualAttemptTracker struct {
	noopSpanTracker
	attempt int
}

func (t allocatingManualAttemptTracker) OpenAttempt(
	context.Context, string, string,
) (*Span, int, error) {
	return nil, t.attempt, nil
}

type noOpReparseAttemptTracker struct {
	noopSpanTracker
	attempt          int
	finalizeCalls    int
	finalizeAttempt  int
	finalizeStatus   string
	finalizeMetadata types.JSONMap
}

func (t *noOpReparseAttemptTracker) OpenAttempt(
	context.Context, string, string,
) (*Span, int, error) {
	return nil, t.attempt, nil
}

func (t *noOpReparseAttemptTracker) FinalizeAttempt(
	_ context.Context, _ string, attempt int, status string,
	output types.JSONMap, _, _ string,
) {
	t.finalizeCalls++
	t.finalizeAttempt = attempt
	t.finalizeStatus = status
	t.finalizeMetadata = output
}

type noOpReparseKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge         *types.Knowledge
	updateCalls       int
	updateColumnCalls int
}

func (r *noOpReparseKnowledgeRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

func (r *noOpReparseKnowledgeRepo) UpdateKnowledge(context.Context, *types.Knowledge) error {
	r.updateCalls++
	return nil
}

func (r *noOpReparseKnowledgeRepo) UpdateKnowledgeColumn(context.Context, string, string, interface{}) error {
	r.updateColumnCalls++
	return nil
}

type noOpReparseKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s noOpReparseKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

type noOpReparseGenerationRepo struct {
	interfaces.KnowledgeGenerationRepository
	active      *types.KnowledgeGeneration
	created     *types.KnowledgeGeneration
	createCalls int
}

func (r *noOpReparseGenerationRepo) GetActive(context.Context, uint64, string) (*types.KnowledgeGeneration, error) {
	return r.active, nil
}

func (r *noOpReparseGenerationRepo) Create(_ context.Context, generation *types.KnowledgeGeneration) error {
	r.createCalls++
	if generation != nil {
		copied := *generation
		r.created = &copied
	}
	return nil
}

type countingTaskEnqueuer struct {
	tasks int
}

func (e *countingTaskEnqueuer) Enqueue(*asynq.Task, ...asynq.Option) (*asynq.TaskInfo, error) {
	e.tasks++
	return &asynq.TaskInfo{ID: "task-1"}, nil
}

func TestResetKnowledgeForReparseClearsPreviousAttemptState(t *testing.T) {
	processedAt := time.Now()
	knowledge := &types.Knowledge{
		ParseStatus:          types.ParseStatusFailed,
		EnableStatus:         "enabled",
		Description:          "previous description",
		ProcessedAt:          &processedAt,
		ErrorMessage:         "embedding request failed: context deadline exceeded",
		EmbeddingModelID:     "old-model",
		PendingSubtasksCount: 3,
	}
	kb := &types.KnowledgeBase{EmbeddingModelID: "new-model"}

	resetKnowledgeForReparse(knowledge, kb)

	require.Equal(t, types.ParseStatusPending, knowledge.ParseStatus)
	require.Equal(t, "disabled", knowledge.EnableStatus)
	require.Empty(t, knowledge.Description)
	require.Nil(t, knowledge.ProcessedAt)
	require.Empty(t, knowledge.ErrorMessage)
	require.Equal(t, "new-model", knowledge.EmbeddingModelID)
	require.Zero(t, knowledge.PendingSubtasksCount)
}

func TestReparseKnowledgeNoopFastPathSkipsCleanupAndEnqueue(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	knowledge := &types.Knowledge{
		ID:                 "knowledge-1",
		TenantID:           7,
		KnowledgeBaseID:    "kb-1",
		Type:               "file",
		FilePath:           "storage/doc.md",
		FileName:           "doc.md",
		FileType:           "md",
		FileHash:           "same-file-hash",
		FileSize:           123,
		ActiveGenerationID: "generation-active",
	}
	kb := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		EmbeddingModelID: "embedding-1",
		SummaryModelID:   "chat-1",
	}
	eff := ResolveProcessConfig(kb, nil)
	sourceDigest, err := knowledgeSourceDigest(knowledge)
	require.NoError(t, err)
	pipelineDigest, err := knowledgePipelineDigest(kb, eff)
	require.NoError(t, err)

	repo := &noOpReparseKnowledgeRepo{knowledge: knowledge}
	generationRepo := &noOpReparseGenerationRepo{active: &types.KnowledgeGeneration{
		ID:             "generation-active",
		TenantID:       7,
		KnowledgeID:    "knowledge-1",
		Attempt:        3,
		State:          types.KnowledgeGenerationStateActive,
		SourceDigest:   sourceDigest,
		PipelineDigest: pipelineDigest,
	}}
	queue := &countingTaskEnqueuer{}
	tracker := &noOpReparseAttemptTracker{attempt: 4}
	svc := &knowledgeService{
		config: &config.Config{ReparseGeneration: &config.ReparseGenerationConfig{
			Enabled:      true,
			NoopFastPath: true,
		}},
		repo:           repo,
		kbService:      noOpReparseKBService{kb: kb},
		generationRepo: generationRepo,
		task:           queue,
		spanTracker:    tracker,
	}

	got, err := svc.ReparseKnowledge(ctx, "knowledge-1", nil)

	require.NoError(t, err)
	require.Same(t, knowledge, got)
	require.Zero(t, repo.updateCalls, "no-op reparse must not reset parse status or mutate the knowledge row")
	require.Zero(t, repo.updateColumnCalls, "no-op reparse must not reset pending subtask counters")
	require.Zero(t, generationRepo.createCalls, "no-op reparse must not create a hidden generation")
	require.Zero(t, queue.tasks, "no-op reparse must not enqueue document parsing")
	require.Equal(t, 1, tracker.finalizeCalls)
	require.Equal(t, 4, tracker.finalizeAttempt)
	require.Equal(t, types.SpanStatusDone, tracker.finalizeStatus)
	require.Equal(t, true, tracker.finalizeMetadata["reparse_noop"])
}

func TestReparseKnowledgePipelineChangeCreatesGenerationAndEnqueues(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	knowledge := &types.Knowledge{
		ID:                 "knowledge-1",
		TenantID:           7,
		KnowledgeBaseID:    "kb-1",
		Type:               "file",
		FilePath:           "storage/doc.md",
		FileName:           "doc.md",
		FileType:           "md",
		FileHash:           "same-file-hash",
		FileSize:           123,
		ActiveGenerationID: "generation-active",
	}
	activeKB := &types.KnowledgeBase{
		ID:               "kb-1",
		TenantID:         7,
		EmbeddingModelID: "embedding-1",
		SummaryModelID:   "chat-1",
	}
	sourceDigest, err := knowledgeSourceDigest(knowledge)
	require.NoError(t, err)
	activePipelineDigest, err := knowledgePipelineDigest(activeKB, ResolveProcessConfig(activeKB, nil))
	require.NoError(t, err)

	reparseKB := *activeKB
	reparseKB.EmbeddingModelID = "embedding-2"
	repo := &noOpReparseKnowledgeRepo{knowledge: knowledge}
	generationRepo := &noOpReparseGenerationRepo{active: &types.KnowledgeGeneration{
		ID:             "generation-active",
		TenantID:       7,
		KnowledgeID:    "knowledge-1",
		Attempt:        3,
		State:          types.KnowledgeGenerationStateActive,
		SourceDigest:   sourceDigest,
		PipelineDigest: activePipelineDigest,
	}}
	queue := &manualReparseTaskEnqueuer{}
	tracker := &noOpReparseAttemptTracker{attempt: 4}
	svc := &knowledgeService{
		config: &config.Config{ReparseGeneration: &config.ReparseGenerationConfig{
			Enabled:      true,
			NoopFastPath: true,
		}},
		repo:           repo,
		kbService:      noOpReparseKBService{kb: &reparseKB},
		generationRepo: generationRepo,
		task:           queue,
		spanTracker:    tracker,
	}

	got, err := svc.ReparseKnowledge(ctx, "knowledge-1", nil)

	require.NoError(t, err)
	require.Same(t, knowledge, got)
	require.Equal(t, 1, generationRepo.createCalls)
	require.NotNil(t, generationRepo.created)
	require.Equal(t, sourceDigest, generationRepo.created.SourceDigest)
	require.NotEqual(t, activePipelineDigest, generationRepo.created.PipelineDigest)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, 1, repo.updateColumnCalls)
	require.NotNil(t, queue.task)

	var payload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	require.Equal(t, 4, payload.Attempt)
	require.Equal(t, generationRepo.created.ID, payload.GenerationID)
	require.Zero(t, tracker.finalizeCalls)
}

func TestEnqueueManualProcessingCarriesGenerationFence(t *testing.T) {
	queue := &manualReparseTaskEnqueuer{}
	svc := &knowledgeService{task: queue}
	knowledge := &types.Knowledge{
		ID:              "knowledge-1",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	_, err := svc.enqueueManualProcessing(context.Background(), knowledge, "# updated", true, manualProcessOptions{
		Attempt:      4,
		GenerationID: "generation-4",
	})
	require.NoError(t, err)
	require.NotNil(t, queue.task)

	var payload types.ManualProcessPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	require.Equal(t, 4, payload.Attempt)
	require.Equal(t, "generation-4", payload.GenerationID)
	require.True(t, payload.NeedCleanup)
}

func TestNewDataTableSummaryTaskCarriesGenerationFence(t *testing.T) {
	queue := &manualReparseTaskEnqueuer{}

	err := NewDataTableSummaryTask(context.Background(), queue, 7, "knowledge-1", "summary-model", "embedding-model",
		dataTableSummaryTaskOptions{Attempt: 4, GenerationID: "generation-4"})

	require.NoError(t, err)
	require.NotNil(t, queue.task)

	var payload DataTableSummaryPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	require.Equal(t, uint64(7), payload.TenantID)
	require.Equal(t, "knowledge-1", payload.KnowledgeID)
	require.Equal(t, "summary-model", payload.SummaryModel)
	require.Equal(t, "embedding-model", payload.EmbeddingModel)
	require.Equal(t, 4, payload.Attempt)
	require.Equal(t, "generation-4", payload.GenerationID)
}

func TestEnqueueManualProcessingAllocatesAttemptForLegacyCaller(t *testing.T) {
	queue := &manualReparseTaskEnqueuer{}
	svc := &knowledgeService{
		task:        queue,
		spanTracker: allocatingManualAttemptTracker{attempt: 9},
	}
	knowledge := &types.Knowledge{
		ID:              "knowledge-legacy",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
	}

	_, err := svc.enqueueManualProcessing(context.Background(), knowledge, "# updated", true)
	require.NoError(t, err)
	require.NotNil(t, queue.task)

	var payload types.ManualProcessPayload
	require.NoError(t, json.Unmarshal(queue.task.Payload(), &payload))
	require.Equal(t, 9, payload.Attempt)
	require.Empty(t, payload.GenerationID)
	require.True(t, payload.NeedCleanup)
}

func TestApplyProcessChunkGenerationFieldsSetsLogicalIdentity(t *testing.T) {
	chunk := &types.Chunk{
		Content:    "hello",
		ChunkType:  types.ChunkTypeText,
		ChunkIndex: 1,
		StartAt:    0,
		EndAt:      5,
	}

	applyProcessChunkGenerationFields(chunk, "gen-1", 0)

	require.Equal(t, "gen-1", chunk.GenerationID)
	require.NotEmpty(t, chunk.LogicalChunkKey)
	require.NotEmpty(t, chunk.ArtifactDigest)
	require.NotEmpty(t, chunk.ID)

	same := &types.Chunk{
		Content:    "hello",
		ChunkType:  types.ChunkTypeText,
		ChunkIndex: 1,
		StartAt:    0,
		EndAt:      5,
	}
	applyProcessChunkGenerationFields(same, "gen-1", 0)
	require.Equal(t, chunk.ID, same.ID)
	require.Equal(t, chunk.LogicalChunkKey, same.LogicalChunkKey)
	require.Equal(t, chunk.ArtifactDigest, same.ArtifactDigest)

	otherGeneration := &types.Chunk{
		Content:    "hello",
		ChunkType:  types.ChunkTypeText,
		ChunkIndex: 1,
		StartAt:    0,
		EndAt:      5,
	}
	applyProcessChunkGenerationFields(otherGeneration, "gen-2", 0)
	require.NotEqual(t, chunk.ID, otherGeneration.ID)
	require.Equal(t, chunk.LogicalChunkKey, otherGeneration.LogicalChunkKey)

	changedContent := &types.Chunk{
		Content:    "changed",
		ChunkType:  types.ChunkTypeText,
		ChunkIndex: 1,
		StartAt:    0,
		EndAt:      5,
	}
	applyProcessChunkGenerationFields(changedContent, "gen-1", 0)
	require.NotEqual(t, chunk.ArtifactDigest, changedContent.ArtifactDigest)
	require.NotEqual(t, chunk.LogicalChunkKey, changedContent.LogicalChunkKey)
}

func TestGenerationManifestDigestChangesWithContent(t *testing.T) {
	first := []*types.Chunk{{
		ID:              "c1",
		Content:         "hello",
		ChunkType:       types.ChunkTypeText,
		ChunkIndex:      1,
		LogicalChunkKey: "k1",
		ArtifactDigest:  "a1",
	}}
	second := []*types.Chunk{{
		ID:              "c1",
		Content:         "changed",
		ChunkType:       types.ChunkTypeText,
		ChunkIndex:      1,
		LogicalChunkKey: "k1",
		ArtifactDigest:  "a1",
	}}

	d1, err := generationManifestDigest(first)
	require.NoError(t, err)
	d2, err := generationManifestDigest(second)
	require.NoError(t, err)

	require.NotEqual(t, d1, d2)
}

func TestKnowledgeSourceDigestForManualIgnoresRevisionMetadata(t *testing.T) {
	first := &types.Knowledge{Type: types.KnowledgeTypeManual}
	require.NoError(t, first.SetManualMetadata(&types.ManualKnowledgeMetadata{
		Content:   "# same",
		Format:    types.ManualKnowledgeFormatMarkdown,
		Status:    types.ManualKnowledgeStatusPublish,
		Version:   1,
		UpdatedAt: "2026-07-29T00:00:00Z",
	}))
	second := &types.Knowledge{Type: types.KnowledgeTypeManual}
	require.NoError(t, second.SetManualMetadata(&types.ManualKnowledgeMetadata{
		Content:   "# same",
		Format:    types.ManualKnowledgeFormatMarkdown,
		Status:    types.ManualKnowledgeStatusPublish,
		Version:   2,
		UpdatedAt: "2026-07-30T00:00:00Z",
	}))
	changed := &types.Knowledge{Type: types.KnowledgeTypeManual}
	require.NoError(t, changed.SetManualMetadata(&types.ManualKnowledgeMetadata{
		Content:   "# changed",
		Format:    types.ManualKnowledgeFormatMarkdown,
		Status:    types.ManualKnowledgeStatusPublish,
		Version:   2,
		UpdatedAt: "2026-07-30T00:00:00Z",
	}))

	firstDigest, err := knowledgeSourceDigest(first)
	require.NoError(t, err)
	secondDigest, err := knowledgeSourceDigest(second)
	require.NoError(t, err)
	changedDigest, err := knowledgeSourceDigest(changed)
	require.NoError(t, err)

	require.Equal(t, firstDigest, secondDigest)
	require.NotEqual(t, firstDigest, changedDigest)
}

func TestKnowledgeSourceDigestForFileIgnoresProcessConfigMetadata(t *testing.T) {
	first := &types.Knowledge{
		Type:     "file",
		FilePath: "storage/doc.md",
		FileName: "doc.md",
		FileType: "md",
		FileHash: "same-file-hash",
		FileSize: 123,
		Metadata: types.JSON(`{"parser_job_id":"job-1"}`),
	}
	second := *first
	require.NoError(t, second.SetProcessOverrides(&types.KnowledgeProcessOverrides{
		ChunkingConfig: &types.ChunkingConfig{ChunkSize: 512},
	}))

	firstDigest, err := knowledgeSourceDigest(first)
	require.NoError(t, err)
	secondDigest, err := knowledgeSourceDigest(&second)
	require.NoError(t, err)

	require.Equal(t, firstDigest, secondDigest)
}

func TestKnowledgePipelineDigestChangesWithDependentConfig(t *testing.T) {
	baseKB := &types.KnowledgeBase{
		EmbeddingModelID: "embedding-1",
		SummaryModelID:   "summary-1",
		VLMConfig: types.VLMConfig{
			Enabled:            true,
			ModelID:            "vlm-1",
			CustomInstructions: "read labels",
		},
	}
	baseDigest, err := knowledgePipelineDigest(baseKB, ResolveProcessConfig(baseKB, nil))
	require.NoError(t, err)

	embeddingChanged := *baseKB
	embeddingChanged.EmbeddingModelID = "embedding-2"
	embeddingDigest, err := knowledgePipelineDigest(&embeddingChanged, ResolveProcessConfig(&embeddingChanged, nil))
	require.NoError(t, err)
	require.NotEqual(t, baseDigest, embeddingDigest)

	vlmPromptChanged := *baseKB
	vlmPromptChanged.VLMConfig.CustomInstructions = "read serial numbers"
	vlmDigest, err := knowledgePipelineDigest(&vlmPromptChanged, ResolveProcessConfig(&vlmPromptChanged, nil))
	require.NoError(t, err)
	require.NotEqual(t, baseDigest, vlmDigest)
}

func TestCleanupProcessChunksGenerationPathRequiresGenerationRepository(t *testing.T) {
	svc := &knowledgeService{}

	err := svc.cleanupProcessChunks(context.Background(), 7, "knowledge-1", "generation-1")

	require.ErrorContains(t, err, "generation chunk cleanup requires chunk repository")
}

type finalizingKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	finalizeCalls int
}

func (r *finalizingKnowledgeRepo) FinalizeSubtask(context.Context, string) (int, bool, error) {
	r.finalizeCalls++
	return 0, true, nil
}

type activatingGenerationRepo struct {
	interfaces.KnowledgeGenerationRepository
	activateCalls int
	retiredIDs    []string
}

func (r *activatingGenerationRepo) ActivateIfCurrent(context.Context, string, int) (bool, error) {
	r.activateCalls++
	return true, nil
}

func (r *activatingGenerationRepo) MarkRetired(_ context.Context, generationID string) error {
	r.retiredIDs = append(r.retiredIDs, generationID)
	return nil
}

func (r *activatingGenerationRepo) MarkFailed(context.Context, string, string) error {
	return errors.New("should not mark failed in this test")
}

type latestAttemptTracker struct {
	noopSpanTracker
	latest int
}

func (t latestAttemptTracker) LatestAttempt(context.Context, string) int {
	return t.latest
}

type generationGCKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	knowledge *types.Knowledge
}

func (r *generationGCKnowledgeRepo) GetKnowledgeByID(context.Context, uint64, string) (*types.Knowledge, error) {
	return r.knowledge, nil
}

type generationGCChunkRepo struct {
	interfaces.ChunkRepository
	chunks      []*types.Chunk
	deleteCalls int
	order       *[]string
}

func (r *generationGCChunkRepo) ListGenerationChunks(context.Context, uint64, string, string) ([]*types.Chunk, error) {
	return r.chunks, nil
}

func (r *generationGCChunkRepo) DeleteChunksByGenerationID(context.Context, uint64, string, string) error {
	r.deleteCalls++
	if r.order != nil {
		*r.order = append(*r.order, "chunks")
	}
	return nil
}

type generationGCTenantRepo struct {
	interfaces.TenantRepository
	tenant *types.Tenant
}

func (r *generationGCTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	if r.tenant != nil {
		return r.tenant, nil
	}
	return &types.Tenant{ID: 7}, nil
}

type generationGCRepo struct {
	interfaces.KnowledgeGenerationRepository
	purgeCalls int
}

func (r *generationGCRepo) MarkPurged(context.Context, string) error {
	r.purgeCalls++
	return nil
}

type failingGCGraphRepo struct {
	interfaces.RetrieveGraphRepository
	err error
}

func (r failingGCGraphRepo) DelGraph(context.Context, []types.NameSpace) error {
	return r.err
}

type generationGCRetrieveEngine struct {
	interfaces.RetrieveEngineService
	err        error
	deletedIDs []string
	order      *[]string
}

func (e *generationGCRetrieveEngine) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (e *generationGCRetrieveEngine) EngineType() types.RetrieverEngineType {
	return types.SQLiteRetrieverEngineType
}

func (e *generationGCRetrieveEngine) Retrieve(context.Context, types.RetrieveParams) ([]*types.RetrieveResult, error) {
	return nil, nil
}

func (e *generationGCRetrieveEngine) DeleteByChunkIDList(_ context.Context, ids []string, _ int, _ string) error {
	e.deletedIDs = append([]string(nil), ids...)
	if e.order != nil {
		*e.order = append(*e.order, "vector")
	}
	return e.err
}

type orderedGCGraphRepo struct {
	interfaces.RetrieveGraphRepository
	namespaces []types.NameSpace
	order      *[]string
}

func (r *orderedGCGraphRepo) DelGraph(_ context.Context, namespaces []types.NameSpace) error {
	r.namespaces = append([]types.NameSpace(nil), namespaces...)
	if r.order != nil {
		*r.order = append(*r.order, "graph")
	}
	return nil
}

func TestGenerationGCPreservesChunksWhenExternalGraphDeleteFails(t *testing.T) {
	chunkRepo := &generationGCChunkRepo{chunks: []*types.Chunk{{ID: "chunk-1"}}}
	generationRepo := &generationGCRepo{}
	svc := &knowledgeService{
		repo:           &generationGCKnowledgeRepo{knowledge: &types.Knowledge{ID: "knowledge-1", TenantID: 7, KnowledgeBaseID: "kb-1"}},
		chunkRepo:      chunkRepo,
		tenantRepo:     &generationGCTenantRepo{},
		generationRepo: generationRepo,
		graphEngine:    failingGCGraphRepo{err: errors.New("graph unavailable")},
	}

	err := svc.gcGeneration(context.Background(), &types.KnowledgeGeneration{
		ID:          "generation-1",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		State:       types.KnowledgeGenerationStateRetired,
	})

	require.ErrorContains(t, err, "graph unavailable")
	require.Zero(t, chunkRepo.deleteCalls)
	require.Zero(t, generationRepo.purgeCalls)
}

func TestGenerationGCPreservesChunksWhenVectorDeleteFails(t *testing.T) {
	chunkRepo := &generationGCChunkRepo{chunks: []*types.Chunk{{ID: "chunk-1"}}}
	generationRepo := &generationGCRepo{}
	engine := &generationGCRetrieveEngine{err: errors.New("vector unavailable")}
	graphRepo := &orderedGCGraphRepo{}
	tenant := &types.Tenant{ID: 7, RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
		RetrieverType:       types.VectorRetrieverType,
		RetrieverEngineType: types.SQLiteRetrieverEngineType,
	}}}}
	svc := &knowledgeService{
		repo: &generationGCKnowledgeRepo{knowledge: &types.Knowledge{
			ID:               "knowledge-1",
			TenantID:         7,
			KnowledgeBaseID:  "kb-1",
			EmbeddingModelID: "embedding-1",
			Type:             "file",
		}},
		kbService:      processDocumentCountingKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		tenantRepo:     &generationGCTenantRepo{tenant: tenant},
		modelService:   processDocumentCountingModelService{embedder: &processDocumentCountingEmbedder{}},
		chunkRepo:      chunkRepo,
		generationRepo: generationRepo,
		retrieveEngine: processDocumentCountingRegistry{engine: engine},
		graphEngine:    graphRepo,
	}

	err := svc.gcGeneration(context.Background(), &types.KnowledgeGeneration{
		ID:          "generation-1",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		State:       types.KnowledgeGenerationStateRetired,
	})

	require.ErrorContains(t, err, "vector unavailable")
	require.Equal(t, []string{"chunk-1"}, engine.deletedIDs)
	require.Empty(t, graphRepo.namespaces)
	require.Zero(t, chunkRepo.deleteCalls)
	require.Zero(t, generationRepo.purgeCalls)
}

func TestGenerationGCDeletesVectorsGraphChunksThenPurges(t *testing.T) {
	order := []string{}
	chunkRepo := &generationGCChunkRepo{
		chunks: []*types.Chunk{{ID: "chunk-1"}, {ID: "chunk-2"}},
		order:  &order,
	}
	generationRepo := &generationGCRepo{}
	engine := &generationGCRetrieveEngine{order: &order}
	graphRepo := &orderedGCGraphRepo{order: &order}
	tenant := &types.Tenant{ID: 7, RetrieverEngines: types.RetrieverEngines{Engines: []types.RetrieverEngineParams{{
		RetrieverType:       types.VectorRetrieverType,
		RetrieverEngineType: types.SQLiteRetrieverEngineType,
	}}}}
	svc := &knowledgeService{
		repo: &generationGCKnowledgeRepo{knowledge: &types.Knowledge{
			ID:               "knowledge-1",
			TenantID:         7,
			KnowledgeBaseID:  "kb-1",
			EmbeddingModelID: "embedding-1",
			Type:             "file",
		}},
		kbService:      processDocumentCountingKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		tenantRepo:     &generationGCTenantRepo{tenant: tenant},
		modelService:   processDocumentCountingModelService{embedder: &processDocumentCountingEmbedder{}},
		chunkRepo:      chunkRepo,
		generationRepo: generationRepo,
		retrieveEngine: processDocumentCountingRegistry{engine: engine},
		graphEngine:    graphRepo,
	}

	err := svc.gcGeneration(context.Background(), &types.KnowledgeGeneration{
		ID:          "generation-1",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		State:       types.KnowledgeGenerationStateRetired,
	})

	require.NoError(t, err)
	require.Equal(t, []string{"vector", "graph", "chunks"}, order)
	require.Equal(t, []string{"chunk-1", "chunk-2"}, engine.deletedIDs)
	require.Len(t, graphRepo.namespaces, 1)
	require.Equal(t, "generation-1", graphRepo.namespaces[0].Generation)
	require.Equal(t, 1, chunkRepo.deleteCalls)
	require.Equal(t, 1, generationRepo.purgeCalls)
}

func TestGenerationGCSkipsActiveGeneration(t *testing.T) {
	chunkRepo := &generationGCChunkRepo{chunks: []*types.Chunk{{ID: "chunk-1"}}}
	generationRepo := &generationGCRepo{}
	graphRepo := &orderedGCGraphRepo{}
	svc := &knowledgeService{
		repo: &generationGCKnowledgeRepo{knowledge: &types.Knowledge{
			ID:                 "knowledge-1",
			TenantID:           7,
			KnowledgeBaseID:    "kb-1",
			ActiveGenerationID: "generation-active",
		}},
		chunkRepo:      chunkRepo,
		tenantRepo:     &generationGCTenantRepo{},
		generationRepo: generationRepo,
		graphEngine:    graphRepo,
	}

	err := svc.gcGeneration(context.Background(), &types.KnowledgeGeneration{
		ID:          "generation-active",
		TenantID:    7,
		KnowledgeID: "knowledge-1",
		State:       types.KnowledgeGenerationStateActive,
	})

	require.NoError(t, err)
	require.Empty(t, graphRepo.namespaces)
	require.Zero(t, chunkRepo.deleteCalls)
	require.Zero(t, generationRepo.purgeCalls)
}

func TestFinalizeGenerationSubtaskRechecksAttemptBeforePublish(t *testing.T) {
	knowledgeRepo := &finalizingKnowledgeRepo{}
	generationRepo := &activatingGenerationRepo{}

	finalizeGenerationSubtaskDetachedWithHook(
		context.Background(),
		knowledgeRepo,
		generationRepo,
		latestAttemptTracker{latest: 2},
		"knowledge-1",
		"summary",
		"generation-1",
		1,
		nil,
		false,
		true,
		nil,
	)

	require.Zero(t, knowledgeRepo.finalizeCalls)
	require.Zero(t, generationRepo.activateCalls)
	require.Equal(t, []string{"generation-1"}, generationRepo.retiredIDs)
}

func TestDataTableSummaryChunksCarryGenerationIdentity(t *testing.T) {
	svc := &DataTableSummaryService{}
	resources := &extractionResources{
		knowledge: &types.Knowledge{
			ID:              "knowledge-1",
			TenantID:        7,
			KnowledgeBaseID: "kb-1",
		},
		generationID: "generation-1",
	}

	chunks := svc.buildChunks(resources, "table summary", "column summary")

	require.Len(t, chunks, 2)
	for _, chunk := range chunks {
		require.Equal(t, "generation-1", chunk.GenerationID)
		require.NotEmpty(t, chunk.LogicalChunkKey)
		require.NotEmpty(t, chunk.ArtifactDigest)
		require.NotEmpty(t, chunk.ID)
	}
	require.NotEqual(t, chunks[0].ID, chunks[1].ID)
	require.Equal(t, chunks[0].ID, chunks[1].ParentChunkID)
}

func TestFinalizeGenerationSubtaskSkipsCallerSupersededWithoutRetiring(t *testing.T) {
	knowledgeRepo := &finalizingKnowledgeRepo{}
	generationRepo := &activatingGenerationRepo{}

	finalizeGenerationSubtaskDetachedWithHook(
		context.Background(),
		knowledgeRepo,
		generationRepo,
		latestAttemptTracker{latest: 1},
		"knowledge-1",
		"summary",
		"generation-1",
		1,
		nil,
		true,
		true,
		nil,
	)

	require.Zero(t, knowledgeRepo.finalizeCalls)
	require.Zero(t, generationRepo.activateCalls)
	require.Empty(t, generationRepo.retiredIDs)
}

func TestGenerationChunkMatcherUsesLCSForRepeatedParagraphs(t *testing.T) {
	oldA := &types.Chunk{ID: "old-a", ChunkType: types.ChunkTypeText, Content: "repeat", ChunkIndex: 0, LogicalChunkKey: "logical-a"}
	oldB := &types.Chunk{ID: "old-b", ChunkType: types.ChunkTypeText, Content: "middle", ChunkIndex: 1, LogicalChunkKey: "logical-b"}
	oldC := &types.Chunk{ID: "old-c", ChunkType: types.ChunkTypeText, Content: "repeat", ChunkIndex: 2, LogicalChunkKey: "logical-c"}
	for _, chunk := range []*types.Chunk{oldA, oldB, oldC} {
		digest, err := chunkArtifactDigest(chunk)
		require.NoError(t, err)
		chunk.ArtifactDigest = digest
	}

	newInserted := &types.Chunk{ID: "new-x", ChunkType: types.ChunkTypeText, Content: "inserted", ChunkIndex: 0}
	newA := &types.Chunk{ID: "new-a", ChunkType: types.ChunkTypeText, Content: "repeat", ChunkIndex: 1}
	newB := &types.Chunk{ID: "new-b", ChunkType: types.ChunkTypeText, Content: "middle", ChunkIndex: 2}
	newC := &types.Chunk{ID: "new-c", ChunkType: types.ChunkTypeText, Content: "repeat", ChunkIndex: 3}
	newChunks := []*types.Chunk{newInserted, newA, newB, newC}
	for i, chunk := range newChunks {
		applyProcessChunkGenerationFields(chunk, "gen-lcs", i)
	}

	matchGenerationChunkGroup("gen-lcs", []*types.Chunk{oldA, oldB, oldC}, newChunks, func(chunk *types.Chunk) bool {
		return chunk.ChunkType == types.ChunkTypeText
	})

	require.NotEqual(t, "logical-a", newInserted.LogicalChunkKey)
	require.Equal(t, "logical-a", newA.LogicalChunkKey)
	require.Equal(t, "logical-b", newB.LogicalChunkKey)
	require.Equal(t, "logical-c", newC.LogicalChunkKey)
	require.NotEmpty(t, newA.ID)
	require.NotEqual(t, newA.ID, newC.ID)
}
