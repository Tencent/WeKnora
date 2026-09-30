package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

// These tests cover the recompute path that runs after the image vector switch
// flips. image_vector chunks are the only ones whose stored content is not what
// was embedded: it is `![image](url)`, so re-embedding one silently replaces a
// real image embedding with the embedding of a markdown link — no error, just a
// knowledge base that quietly stops finding images.

// reindexEngine records the two things that must be kept apart: which vectors
// were deleted and rebuilt, and which only had their visibility toggled.
type reindexEngine = imageVectorSyncEngine

type reindexRegistry struct {
	interfaces.RetrieveEngineRegistry
	engine interfaces.RetrieveEngineService
}

func (r reindexRegistry) GetRetrieveEngineService(
	types.RetrieverEngineType,
) (interfaces.RetrieveEngineService, error) {
	return r.engine, nil
}

type reindexKBRepo struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (r reindexKBRepo) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return r.kb, nil
}

type reindexKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	byKB     []*types.Knowledge
	listedKB string
	err      error
}

func (r *reindexKnowledgeRepo) ListKnowledgeByKnowledgeBaseID(
	_ context.Context, _ uint64, kbID string,
) ([]*types.Knowledge, error) {
	r.listedKB = kbID
	return r.byKB, r.err
}

func (r *reindexKnowledgeRepo) GetKnowledgeByID(
	_ context.Context, _ uint64, id string,
) (*types.Knowledge, error) {
	return &types.Knowledge{ID: id, KnowledgeBaseID: "kb-reindex", TenantID: 1}, nil
}

type reindexChunkRepo struct {
	interfaces.ChunkRepository
	chunks      map[string][]*types.Chunk
	listedIDs   []string
	listedTypes []types.ChunkType
}

// Honours the type filter: a stub that ignored it would hand back image chunks
// regardless and the assertions would pass without production listing them.
func (r *reindexChunkRepo) ListChunksByKnowledgeIDAndTypes(
	_ context.Context, _ uint64, knowledgeID string, chunkTypes []types.ChunkType,
) ([]*types.Chunk, error) {
	r.listedIDs = append(r.listedIDs, knowledgeID)
	r.listedTypes = append(r.listedTypes, chunkTypes...)
	want := make(map[types.ChunkType]bool, len(chunkTypes))
	for _, ct := range chunkTypes {
		want[ct] = true
	}
	var out []*types.Chunk
	for _, chunk := range r.chunks[knowledgeID] {
		if want[chunk.ChunkType] {
			out = append(out, chunk)
		}
	}
	return out, nil
}

type reindexTenantRepo struct {
	interfaces.TenantRepository
}

// Async handlers have no ambient tenant, and the engine factory reads it off
// the context.
func (r reindexTenantRepo) GetTenantByID(context.Context, uint64) (*types.Tenant, error) {
	return &types.Tenant{
		RetrieverEngines: types.RetrieverEngines{
			Engines: []types.RetrieverEngineParams{{
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			}},
		},
	}, nil
}

const reindexKBID = "kb-reindex"

func newReindexService(
	t *testing.T, kb *types.KnowledgeBase, engine *reindexEngine,
	chunkRepo *reindexChunkRepo, knowledgeRepo *reindexKnowledgeRepo,
) *knowledgeService {
	t.Helper()
	kb.ID = reindexKBID
	// Every fixture here is tenant 1, and the handler grants the KB write by
	// checking the KB's tenant against the payload's — a KB without one would
	// fail that check for a reason no test is about.
	kb.TenantID = 1
	kb.EmbeddingModelID = "model-1"
	return &knowledgeService{
		repo:           knowledgeRepo,
		kbService:      reindexKBRepo{kb: kb},
		chunkRepo:      chunkRepo,
		tenantRepo:     reindexTenantRepo{},
		modelService:   &capabilityModelService{embedder: &capabilityEmbedder{supportsImage: true}},
		retrieveEngine: reindexRegistry{engine: engine},
	}
}

func reindexContext() context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
		RetrieverEngines: types.RetrieverEngines{
			Engines: []types.RetrieverEngineParams{{
				RetrieverEngineType: types.SQLiteRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			}},
		},
	})
}

func reindexTextChunk(id, content string, isEnabled bool) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		ChunkType:       types.ChunkTypeText,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: reindexKBID,
		TenantID:        1,
		IsEnabled:       isEnabled,
		Content:         content,
	}
}

func reindexImageChunk(id string) *types.Chunk {
	return &types.Chunk{
		ID:              id,
		ChunkType:       types.ChunkTypeImageVector,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: reindexKBID,
		TenantID:        1,
		IsEnabled:       true,
		Content:         "![image](https://example.test/red.png)",
	}
}

// Main case: every text vector moves into the chat-template space, while the
// image vectors already there are left alone and made visible again.
func TestReindexKnowledgeVectorsRebuildsTextAndTogglesImages(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{
		"knowledge-1": {
			reindexTextChunk("text-1", "一段关于红色消防车的说明", true),
			reindexTextChunk("text-2", "一段关于蓝色自行车的说明", false),
			reindexImageChunk("image-1"),
		},
	}}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	svc := newReindexService(t, kb, engine, chunkRepo, &reindexKnowledgeRepo{})

	require.NoError(t, svc.reindexKnowledgeVectors(reindexContext(), kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1}))

	// Both text chunks are deleted and rebuilt: the disabled one too, because
	// its stale vector is still in the index until it is removed.
	require.Equal(t, []string{"text-1", "text-2"}, engine.deletedIDs)
	require.Len(t, engine.indexed, 1, "only the enabled chunk gets a new vector")
	require.Contains(t, engine.indexed[0].Content, "红色消防车")

	// The image vector is untouched but visible again.
	require.NotContains(t, engine.deletedIDs, "image-1",
		"the image vector is still a faithful embedding of an image that has not changed")
	for _, info := range engine.indexed {
		require.NotContains(t, info.Content, "![image]",
			"a markdown link must never be embedded in place of the picture")
	}
	require.Equal(t, map[string]bool{"image-1": true}, engine.enabledUpdates)

	// Guard against someone "simplifying" this by dropping image_vector from
	// the query — the toggle would then silently become a no-op.
	require.Contains(t, chunkRepo.listedTypes, types.ChunkTypeImageVector)
}

// The other direction: image vectors are hidden rather than deleted, so
// flipping the switch back on restores them without touching storage.
func TestReindexKnowledgeVectorsHidesImagesWhenEnvelopeOff(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{
		"knowledge-1": {reindexTextChunk("text-1", "内容", true), reindexImageChunk("image-1")},
	}}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: false},
	}
	svc := newReindexService(t, kb, engine, chunkRepo, &reindexKnowledgeRepo{})

	require.NoError(t, svc.reindexKnowledgeVectors(reindexContext(), kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1}))

	require.Equal(t, []string{"text-1"}, engine.deletedIDs, "text still moves out of the chat space")
	require.Equal(t, map[string]bool{"image-1": false}, engine.enabledUpdates,
		"a visible image vector in the wrong space costs a result slot on every query")
}

// Guards the batching: more chunks than one batch must not stop early.
func TestReindexKnowledgeVectorsCoversEveryChunk(t *testing.T) {
	const total = 250
	engine := &reindexEngine{}
	chunks := make([]*types.Chunk, 0, total)
	for i := 0; i < total; i++ {
		chunks = append(chunks, reindexTextChunk(fmt.Sprintf("chunk-%d", i), "内容", true))
	}
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{"knowledge-1": chunks}}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	svc := newReindexService(t, kb, engine, chunkRepo, &reindexKnowledgeRepo{})

	require.NoError(t, svc.reindexKnowledgeVectors(reindexContext(), kb,
		&types.Knowledge{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1}))

	require.Len(t, engine.deletedIDs, total)
	require.Len(t, engine.indexed, total)
}

// Second line of defence: reindexKnowledgeVectors already separates image
// chunks out, but other callers pass whatever chunks they happen to hold.
func TestUpdateChunkVectorSkipsImageVectors(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	svc := newReindexService(t, kb, engine, chunkRepo, &reindexKnowledgeRepo{})

	require.NoError(t, svc.updateChunkVector(reindexContext(), reindexKBID, []*types.Chunk{
		reindexImageChunk("image-1"),
		reindexTextChunk("text-1", "一段关于红色消防车的说明", true),
	}))

	require.Equal(t, []string{"text-1"}, engine.deletedIDs,
		"an image vector must never be deleted here: the image has not changed")
	require.Len(t, engine.indexed, 1)
	require.Contains(t, engine.indexed[0].Content, "红色消防车")
}

func reindexTask(t *testing.T, payload types.KBReindexVectorsPayload) *asynq.Task {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKBReindexVectors, raw)
}

// A document still being parsed is indexed when its pipeline finishes;
// re-embedding it here would race that pipeline.
func TestProcessKBReindexVectorsSkipsIncompleteKnowledge(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{chunks: map[string][]*types.Chunk{
		"knowledge-done": {reindexTextChunk("text-1", "内容", true)},
	}}
	knowledgeRepo := &reindexKnowledgeRepo{byKB: []*types.Knowledge{
		{ID: "knowledge-done", KnowledgeBaseID: reindexKBID, TenantID: 1, ParseStatus: types.ParseStatusCompleted},
		{ID: "knowledge-parsing", KnowledgeBaseID: reindexKBID, TenantID: 1, ParseStatus: types.ParseStatusProcessing},
		{ID: "knowledge-failed", KnowledgeBaseID: reindexKBID, TenantID: 1, ParseStatus: types.ParseStatusFailed},
	}}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	svc := newReindexService(t, kb, engine, chunkRepo, knowledgeRepo)

	require.NoError(t, svc.ProcessKBReindexVectors(context.Background(), reindexTask(t,
		types.KBReindexVectorsPayload{TenantID: 1, KnowledgeBaseID: reindexKBID})))

	require.Equal(t, reindexKBID, knowledgeRepo.listedKB)
	require.Equal(t, []string{"knowledge-done"}, chunkRepo.listedIDs,
		"only completed documents are safe to re-embed")
}

// The handler must not return an error: a retry re-runs the whole knowledge
// base and re-bills every other document on the way.
func TestProcessKBReindexVectorsDoesNotRetryOneBadDocument(t *testing.T) {
	engine := &reindexEngine{}
	chunkRepo := &reindexChunkRepo{}
	chunkRepo.chunks = map[string][]*types.Chunk{
		"knowledge-1": {reindexTextChunk("text-1", "内容", true)},
		"knowledge-3": {reindexTextChunk("text-3", "内容", true)},
	}
	knowledgeRepo := &reindexKnowledgeRepo{}
	knowledgeRepo.byKB = []*types.Knowledge{
		{ID: "knowledge-1", KnowledgeBaseID: reindexKBID, TenantID: 1, ParseStatus: types.ParseStatusCompleted},
		{ID: "knowledge-2", KnowledgeBaseID: "other-kb", TenantID: 1, ParseStatus: types.ParseStatusCompleted},
		{ID: "knowledge-3", KnowledgeBaseID: reindexKBID, TenantID: 1, ParseStatus: types.ParseStatusCompleted},
	}
	kb := &types.KnowledgeBase{
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, ImageVectorEnabled: true},
	}
	svc := newReindexService(t, kb, engine, chunkRepo, knowledgeRepo)

	// knowledge-2 lives in another knowledge base: updateChunkVector skips
	// chunks whose KnowledgeBaseID does not match, so it is a no-op rather than
	// a failure. The point is that the remaining documents still get processed.
	require.NoError(t, svc.ProcessKBReindexVectors(context.Background(), reindexTask(t,
		types.KBReindexVectorsPayload{TenantID: 1, KnowledgeBaseID: reindexKBID})))

	require.Equal(t, []string{"knowledge-1", "knowledge-2", "knowledge-3"}, chunkRepo.listedIDs)
	require.Equal(t, []string{"text-1", "text-3"}, engine.deletedIDs)
}

// An unparseable payload can never succeed on retry, so asynq must not
// redeliver it.
func TestProcessKBReindexVectorsRejectsBrokenPayload(t *testing.T) {
	svc := &knowledgeService{}
	broken := asynq.NewTask(types.TypeKBReindexVectors, []byte("not json"))

	err := svc.ProcessKBReindexVectors(context.Background(), broken)

	require.Error(t, err)
	require.False(t, errors.Is(err, asynq.SkipRetry), "a malformed payload should be retried, not skipped")
}
