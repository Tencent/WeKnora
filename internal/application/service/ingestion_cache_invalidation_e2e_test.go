package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type ingestionArtifactFixture struct {
	db   *gorm.DB
	repo interfaces.DerivedArtifactRepository
}

func newIngestionArtifactFixture(t *testing.T) *ingestionArtifactFixture {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "artifact-fixture.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	return &ingestionArtifactFixture{db: db, repo: repository.NewDerivedArtifactRepository(db)}
}

func (f *ingestionArtifactFixture) embedding(t *testing.T, model *modelcount.CountingEmbedder) *artifactCachedEmbedder {
	t.Helper()
	cached, err := newArtifactCachedEmbedder(model, f.repo, embedding.Config{ModelID: "embedding-model", ModelName: "embedding-model", Provider: "test", Dimensions: 3})
	require.NoError(t, err)
	return cached.(*artifactCachedEmbedder)
}

func TestIngestionArtifactReuse_ThreeIdenticalRebuildsDoNotIncreaseProviderCalls(t *testing.T) {
	f := newIngestionArtifactFixture(t)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	embedProvider := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "embedding-model", ModelName: "embedding-model", Dimensions: 3})
	embedder := f.embedding(t, embedProvider)
	graphProvider := &graphObservationChat{response: `[{"entity":"Node","entity_attributes":["kind"]}]`}
	graphService := &ChunkExtractService{artifactRepo: f.repo}
	contents := []string{"chunk A", "chunk B", "chunk C"}

	for run := 1; run <= 3; run++ {
		vectors, err := embedder.BatchEmbed(ctx, contents)
		require.NoError(t, err)
		require.Len(t, vectors, 3)
		for _, content := range contents {
			graph, observation, err := graphService.extractGraphCached(context.Background(), 1, graphProvider, graphExtractCacheTemplate(), content)
			require.NoError(t, err)
			require.Len(t, graph.Node, 1)
			if run == 1 {
				require.Equal(t, string(types.IngestionCacheStatusMiss), observation["cache_status"])
			} else {
				require.Equal(t, string(types.IngestionCacheStatusHit), observation["cache_status"])
				require.EqualValues(t, 0, observation["request_count"])
			}
		}
		if run == 1 {
			require.Equal(t, 3, embedProvider.Snapshot().TotalInputItems)
			requests, _ := graphProvider.Snapshot()
			require.Equal(t, 3, requests)
		} else {
			require.Equal(t, 3, embedProvider.Snapshot().TotalInputItems, "identical rebuild must not add embedding inputs")
			requests, _ := graphProvider.Snapshot()
			require.Equal(t, 3, requests, "identical rebuild must not add graph requests")
		}
	}
}

func TestIngestionArtifactReuse_OneChunkChanged(t *testing.T) {
	f := newIngestionArtifactFixture(t)
	ctx := embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk)
	embedProvider := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "embedding-model", ModelName: "embedding-model", Dimensions: 3})
	embedder := f.embedding(t, embedProvider)
	graphProvider := &graphObservationChat{response: `[{"entity":"Node","entity_attributes":["kind"]}]`}
	graphService := &ChunkExtractService{artifactRepo: f.repo}
	first := []string{"chunk A", "chunk B", "chunk C"}
	second := []string{"chunk A", "chunk B changed", "chunk C"}

	_, err := embedder.BatchEmbed(ctx, first)
	require.NoError(t, err)
	for _, content := range first {
		_, _, err := graphService.extractGraphCached(context.Background(), 1, graphProvider, graphExtractCacheTemplate(), content)
		require.NoError(t, err)
	}
	beforeEmbed := embedProvider.Snapshot().TotalInputItems
	beforeGraph, _ := graphProvider.Snapshot()

	_, err = embedder.BatchEmbed(ctx, second)
	require.NoError(t, err)
	statuses := make([]string, 0, 3)
	for _, content := range second {
		_, observation, err := graphService.extractGraphCached(context.Background(), 1, graphProvider, graphExtractCacheTemplate(), content)
		require.NoError(t, err)
		statuses = append(statuses, fmt.Sprint(observation["cache_status"]))
	}
	require.Equal(t, beforeEmbed+1, embedProvider.Snapshot().TotalInputItems, "only changed chunk B may be embedded")
	afterGraph, _ := graphProvider.Snapshot()
	require.Equal(t, beforeGraph+1, afterGraph, "only changed chunk B may be extracted")
	require.Equal(t, []string{"hit", "miss", "hit"}, statuses)
}

func TestIngestionArtifactInvalidation_TenantIsolation(t *testing.T) {
	f := newIngestionArtifactFixture(t)
	image := []byte("tenant-isolated-image")
	ocrSpec := multimodalCacheSpec{Kind: "multimodal.ocr", Operation: types.IngestionOperationMultimodalOCR, Prompt: "ocr", Normalize: sanitizeOCRText}
	captionSpec := multimodalCacheSpec{Kind: "multimodal.caption", Operation: types.IngestionOperationMultimodalCaption, Prompt: "caption", Normalize: canonicalCaption}
	vlmProvider := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm", OCRResponse: "ocr", CaptionResponse: "caption"})
	vlmService := &ImageMultimodalService{artifactRepo: f.repo}
	embedProvider := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "embedding-model", ModelName: "embedding-model", Dimensions: 3})
	embedder := f.embedding(t, embedProvider)
	graphProvider := &graphObservationChat{response: `[{"entity":"Node"}]`}
	graphService := &ChunkExtractService{artifactRepo: f.repo}
	wikiChunks := &wikiMapTestChunkRepo{chunks: []*types.Chunk{{ID: "row", TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "same wiki text", ChunkType: types.ChunkTypeText, IsEnabled: true, StableIdentity: "stable", IdentityVersion: contentkey.ChunkIdentityVersion}}}
	wikiService := &wikiIngestService{artifactRepo: f.repo, chunkRepo: wikiChunks, wikiService: &wikiMapTestWikiService{pages: make(map[string]*types.WikiPage)}, knowledgeSvc: &wikiMapTestKnowledgeService{knowledge: &types.Knowledge{ID: "doc", Title: "Document", ParseStatus: types.ParseStatusCompleted}}}
	wikiProvider := &wikiMapCitationChat{}

	for _, tenantID := range []uint64{1, 2} {
		_, hit, err := vlmService.cachedMultimodalPredict(context.Background(), tenantID, vlmProvider, "vlm", image, ocrSpec)
		require.NoError(t, err)
		require.False(t, hit)
		_, hit, err = vlmService.cachedMultimodalPredict(context.Background(), tenantID, vlmProvider, "vlm", image, captionSpec)
		require.NoError(t, err)
		require.False(t, hit)
		_, err = embedder.BatchEmbed(embeddingArtifactTestContext(tenantID, types.IngestionOperationEmbeddingChunk), []string{"same text"})
		require.NoError(t, err)
		_, observation, err := graphService.extractGraphCached(context.Background(), tenantID, graphProvider, graphExtractCacheTemplate(), "same text")
		require.NoError(t, err)
		require.Equal(t, string(types.IngestionCacheStatusMiss), observation["cache_status"])
		wikiChunks.mu.Lock()
		wikiChunks.chunks[0].TenantID = tenantID
		wikiChunks.mu.Unlock()
		result, _, err := wikiService.mapOneDocument(context.Background(), wikiProvider, WikiIngestPayload{TenantID: tenantID, KnowledgeBaseID: "kb"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc", Language: "en"}, wikiMapIntegrationBatchContext())
		require.NoError(t, err)
		require.Equal(t, types.IngestionCacheStatusMiss, result.MapStats["cache_status"])
	}
	require.Equal(t, 4, vlmProvider.Snapshot().PredictRequestCount)
	require.Equal(t, 2, embedProvider.Snapshot().TotalInputItems)
	graphRequests, _ := graphProvider.Snapshot()
	require.Equal(t, 2, graphRequests)
	var byTenant []struct {
		TenantID uint64
		Count    int64
	}
	require.NoError(t, f.db.Model(&types.DerivedArtifact{}).Select("tenant_id, count(*) AS count").Group("tenant_id").Order("tenant_id").Scan(&byTenant).Error)
	require.Equal(t, []struct {
		TenantID uint64
		Count    int64
	}{{1, 5}, {2, 5}}, byTenant)
}

func TestIngestionArtifactPayloads_ContainNoSecretsOrRowIDs(t *testing.T) {
	f := newIngestionArtifactFixture(t)
	const rowID = "OLD_CHUNK_ROW_ID"
	markers := []string{"SECRET_API_KEY_MARKER", "SECRET_HEADER_MARKER", "ACCESS_TOKEN_MARKER", rowID, "ATTEMPT_ID_MARKER", "TRACE_ID_MARKER", "OPERATION_ID_MARKER"}
	image := []byte("safe image bytes")
	vlmProvider := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm", OCRResponse: "canonical OCR", CaptionResponse: "canonical caption"})
	vlmService := &ImageMultimodalService{artifactRepo: f.repo}
	_, _, err := vlmService.cachedMultimodalPredict(context.Background(), 1, vlmProvider, "vlm", image, multimodalCacheSpec{Kind: "multimodal.ocr", Operation: types.IngestionOperationMultimodalOCR, Prompt: "safe OCR prompt", Normalize: sanitizeOCRText})
	require.NoError(t, err)
	_, _, err = vlmService.cachedMultimodalPredict(context.Background(), 1, vlmProvider, "vlm", image, multimodalCacheSpec{Kind: "multimodal.caption", Operation: types.IngestionOperationMultimodalCaption, Prompt: "safe caption prompt", Normalize: canonicalCaption})
	require.NoError(t, err)
	embedProvider := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "embedding-model", ModelName: "embedding-model", Dimensions: 3})
	_, err = f.embedding(t, embedProvider).BatchEmbed(embeddingArtifactTestContext(1, types.IngestionOperationEmbeddingChunk), []string{"safe embedding text"})
	require.NoError(t, err)
	_, _, err = (&ChunkExtractService{artifactRepo: f.repo}).extractGraphCached(context.Background(), 1, &graphObservationChat{response: `[{"entity":"Safe"}]`}, graphExtractCacheTemplate(), "safe graph text")
	require.NoError(t, err)

	chunks := &wikiMapTestChunkRepo{chunks: []*types.Chunk{{ID: rowID, TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb", Content: "safe wiki source", ChunkType: types.ChunkTypeText, IsEnabled: true, StableIdentity: "stable-safe", IdentityVersion: contentkey.ChunkIdentityVersion}}}
	wikiSvc := &wikiIngestService{artifactRepo: f.repo, chunkRepo: chunks, wikiService: &wikiMapTestWikiService{pages: make(map[string]*types.WikiPage)}, knowledgeSvc: &wikiMapTestKnowledgeService{knowledge: &types.Knowledge{ID: "doc", Title: "Safe", ParseStatus: types.ParseStatusCompleted}}}
	_, _, err = wikiSvc.mapOneDocument(context.Background(), &wikiMapCitationChat{}, WikiIngestPayload{TenantID: 1, KnowledgeBaseID: "kb"}, WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "doc", Language: "en"}, wikiMapIntegrationBatchContext())
	require.NoError(t, err)

	var artifacts []types.DerivedArtifact
	require.NoError(t, f.db.Order("artifact_kind").Find(&artifacts).Error)
	require.Len(t, artifacts, 5)
	for _, artifact := range artifacts {
		require.Equal(t, artifactkey.DigestBytes(artifact.Payload), artifact.PayloadDigest, "payload digest for %s", artifact.ArtifactKind)
		payload := string(artifact.Payload)
		for _, marker := range markers {
			require.False(t, strings.Contains(payload, marker), "artifact %s contains forbidden marker name %s", artifact.ArtifactKind, marker)
		}
		if artifact.ArtifactKind == embeddingArtifactKind {
			require.Equal(t, embeddingArtifactEncoding, artifact.PayloadEncoding)
		} else {
			require.Equal(t, "json", artifact.PayloadEncoding)
		}
	}
}
