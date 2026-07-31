package service

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/contentkey"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestIngestionCachesEndToEndAcrossRestartInvalidationAndCrashRecovery is the
// PR9 acceptance test for the independently introduced artifact caches. It
// deliberately gives every cache the same durable repository, as production
// does, and verifies that their keys and recovery state cannot interfere.
func TestIngestionCachesEndToEndAcrossRestartInvalidationAndCrashRecovery(t *testing.T) {
	ctx := context.Background()
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL", filepath.ToSlash(filepath.Join(t.TempDir(), "ingestion-cache-e2e.db")))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.DerivedArtifact{}))
	artifactRepo := repository.NewDerivedArtifactRepository(db)

	chunks := &wikiMapTestChunkRepo{chunks: []*types.Chunk{{
		ID: "chunk-row-before-rebuild", TenantID: 7, KnowledgeID: "document-1", KnowledgeBaseID: "kb-1",
		Content: "Alice knows Bob in the canonical source document.", ChunkIndex: 0, StartAt: 0,
		ChunkType: types.ChunkTypeText, IsEnabled: true, StableIdentity: "stable-source-chunk",
		IdentityVersion: contentkey.ChunkIdentityVersion,
	}}}
	newWikiService := func() *wikiIngestService {
		return &wikiIngestService{
			artifactRepo: artifactRepo,
			chunkRepo:    chunks,
			wikiService:  &wikiMapTestWikiService{pages: make(map[string]*types.WikiPage)},
			knowledgeSvc: &wikiMapTestKnowledgeService{knowledge: &types.Knowledge{
				ID: "document-1", Title: "Document", ParseStatus: types.ParseStatusCompleted,
			}},
		}
	}
	newEmbedding := func(model *modelcount.CountingEmbedder) *artifactCachedEmbedder {
		cached, cacheErr := newArtifactCachedEmbedder(model, artifactRepo, embedding.Config{
			ModelID: "embedding-model", ModelName: "embedding-model", Provider: "test", Dimensions: 3,
		})
		require.NoError(t, cacheErr)
		return cached.(*artifactCachedEmbedder)
	}

	image := []byte("canonical-image-bytes")
	ocrSpec := multimodalCacheSpec{Kind: "multimodal.ocr", Operation: types.IngestionOperationMultimodalOCR, Prompt: "read text", Normalize: sanitizeOCRText}
	captionSpec := multimodalCacheSpec{Kind: "multimodal.caption", Operation: types.IngestionOperationMultimodalCaption, Prompt: "describe image", Normalize: canonicalCaption}
	graphPrompt := graphExtractCacheTemplate()
	wikiPayload := WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb-1"}
	wikiOp := WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "document-1", Language: "en"}
	embedCtx := embeddingArtifactTestContext(7, types.IngestionOperationEmbeddingChunk)

	// First process: every layer computes and persists its own artifact kind.
	vlmFirst := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm-model", OCRResponse: " Alice and Bob ", CaptionResponse: " Alice and Bob in an image "})
	ocrFirst, hit, err := (&ImageMultimodalService{artifactRepo: artifactRepo}).cachedMultimodalPredict(ctx, 7, vlmFirst, "vlm-model", image, ocrSpec)
	require.NoError(t, err)
	require.False(t, hit)
	captionFirst, hit, err := (&ImageMultimodalService{artifactRepo: artifactRepo}).cachedMultimodalPredict(ctx, 7, vlmFirst, "vlm-model", image, captionSpec)
	require.NoError(t, err)
	require.False(t, hit)
	embedFirstModel := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "embedding-model", ModelName: "embedding-model", Dimensions: 3})
	embedFirst, err := newEmbedding(embedFirstModel).BatchEmbed(embedCtx, []string{"Alice", "Bob"})
	require.NoError(t, err)
	graphFirstModel := &graphObservationChat{response: `[{"entity":"Alice","entity_attributes":["person"]}]`}
	graphFirst, graphObservation, err := (&ChunkExtractService{artifactRepo: artifactRepo}).extractGraphCached(ctx, 7, graphFirstModel, graphPrompt, "Alice knows Bob")
	require.NoError(t, err)
	require.Equal(t, string(types.IngestionCacheStatusMiss), graphObservation["cache_status"])
	var originalGraphArtifact types.DerivedArtifact
	require.NoError(t, db.Where("tenant_id = ? AND artifact_kind = ?", 7, graphExtractArtifactKind).Take(&originalGraphArtifact).Error)
	wikiFirstModel := &wikiMapCitationChat{}
	_, wikiFirstUpdates, err := newWikiService().mapOneDocument(ctx, wikiFirstModel, wikiPayload, wikiOp, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Equal(t, []string{"chunk-row-before-rebuild"}, sourceChunksForSlug(wikiFirstUpdates, "entity/acme"))

	var kinds []string
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Distinct().Order("artifact_kind").Pluck("artifact_kind", &kinds).Error)
	require.Equal(t, []string{"embedding.vector", "graph.chunk-extraction", "multimodal.caption", "multimodal.ocr", "wiki.document-map"}, kinds)

	// Simulate a server restart and chunk-row reconciliation. Providers with the
	// same identities must not be called, and Wiki references must bind to the
	// rebuilt database row rather than leaking a cached row ID.
	chunks.mu.Lock()
	chunks.chunks[0].ID = "chunk-row-after-rebuild"
	chunks.mu.Unlock()
	vlmRestart := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm-model", OCRResponse: "must not be used", CaptionResponse: "must not be used"})
	ocrRestart, hit, err := (&ImageMultimodalService{artifactRepo: repository.NewDerivedArtifactRepository(db)}).cachedMultimodalPredict(ctx, 7, vlmRestart, "vlm-model", image, ocrSpec)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, ocrFirst, ocrRestart)
	captionRestart, hit, err := (&ImageMultimodalService{artifactRepo: repository.NewDerivedArtifactRepository(db)}).cachedMultimodalPredict(ctx, 7, vlmRestart, "vlm-model", image, captionSpec)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, captionFirst, captionRestart)
	require.Zero(t, vlmRestart.Snapshot().PredictRequestCount)
	embedRestartModel := modelcount.NewCountingEmbedder(modelcount.CountingEmbedderOptions{ModelID: "embedding-model", ModelName: "embedding-model", Dimensions: 3})
	embedRestart, err := newEmbedding(embedRestartModel).BatchEmbed(embedCtx, []string{"Alice", "Bob"})
	require.NoError(t, err)
	require.Equal(t, embedFirst, embedRestart)
	require.Zero(t, embedRestartModel.Snapshot().RequestCount)
	graphRestartModel := &graphObservationChat{response: `[{"entity":"must-not-run"}]`}
	graphRestart, graphObservation, err := (&ChunkExtractService{artifactRepo: repository.NewDerivedArtifactRepository(db)}).extractGraphCached(ctx, 7, graphRestartModel, graphPrompt, "Alice knows Bob")
	require.NoError(t, err)
	require.Equal(t, string(types.IngestionCacheStatusHit), graphObservation["cache_status"])
	require.Equal(t, graphFirst, graphRestart)
	graphRestartRequests, _ := graphRestartModel.Snapshot()
	require.Zero(t, graphRestartRequests)
	wikiRestartModel := &wikiMapCitationChat{}
	_, wikiRestartUpdates, err := newWikiService().mapOneDocument(ctx, wikiRestartModel, wikiPayload, wikiOp, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Zero(t, wikiRestartModel.calls.Load())
	require.Equal(t, []string{"chunk-row-after-rebuild"}, sourceChunksForSlug(wikiRestartUpdates, "entity/acme"))

	// Exact invalidation: a graph prompt change invalidates only graph extraction.
	changedGraphPrompt := graphExtractCacheTemplate()
	changedGraphPrompt.Tags = []string{"PERSON"}
	graphChangedModel := &graphObservationChat{response: `[{"entity":"Alice","entity_attributes":["person"]}]`}
	_, graphObservation, err = (&ChunkExtractService{artifactRepo: artifactRepo}).extractGraphCached(ctx, 7, graphChangedModel, changedGraphPrompt, "Alice knows Bob")
	require.NoError(t, err)
	require.Equal(t, string(types.IngestionCacheStatusMiss), graphObservation["cache_status"])
	graphChangedRequests, _ := graphChangedModel.Snapshot()
	require.Equal(t, 1, graphChangedRequests)
	_, hit, err = (&ImageMultimodalService{artifactRepo: artifactRepo}).cachedMultimodalPredict(ctx, 7, vlmRestart, "vlm-model", image, ocrSpec)
	require.NoError(t, err)
	require.True(t, hit)
	_, hit, err = (&ImageMultimodalService{artifactRepo: artifactRepo}).cachedMultimodalPredict(ctx, 7, vlmRestart, "vlm-model", image, captionSpec)
	require.NoError(t, err)
	require.True(t, hit)
	_, err = newEmbedding(embedRestartModel).BatchEmbed(embedCtx, []string{"Alice", "Bob"})
	require.NoError(t, err)
	_, _, err = newWikiService().mapOneDocument(ctx, wikiRestartModel, wikiPayload, wikiOp, wikiMapIntegrationBatchContext())
	require.NoError(t, err)
	require.Zero(t, vlmRestart.Snapshot().PredictRequestCount)
	require.Zero(t, embedRestartModel.Snapshot().RequestCount)
	require.Zero(t, wikiRestartModel.calls.Load())

	// Crash recovery: turn the original graph artifact into the durable state a
	// dead worker leaves behind. A new instance must take it over after expiry;
	// the stale owner cannot strand or overwrite the key.
	expired := time.Now().UTC().Add(-time.Minute)
	require.NoError(t, db.Model(&types.DerivedArtifact{}).
		Where("id = ?", originalGraphArtifact.ID).
		Updates(map[string]any{"status": types.DerivedArtifactComputing, "owner_token": "crashed-worker", "lease_expires_at": expired, "payload": nil, "payload_digest": "", "completed_at": nil}).Error)

	var crashed types.DerivedArtifact
	require.NoError(t, db.Where("id = ?", originalGraphArtifact.ID).Take(&crashed).Error)
	recoveryModel := &graphObservationChat{response: `[{"entity":"Recovered","entity_attributes":["person"]}]`}
	recovered, recoveryObservation, err := (&ChunkExtractService{artifactRepo: repository.NewDerivedArtifactRepository(db)}).extractGraphCached(ctx, 7, recoveryModel, graphPrompt, "Alice knows Bob")
	require.NoError(t, err)
	require.Len(t, recovered.Node, 1)
	require.Equal(t, "Recovered", recovered.Node[0].Name)
	require.Equal(t, string(types.IngestionCacheStatusMiss), recoveryObservation["cache_status"])
	require.NoError(t, db.Where("id = ?", crashed.ID).Take(&crashed).Error)
	require.Equal(t, types.DerivedArtifactSucceeded, crashed.Status)
	require.Equal(t, 2, crashed.AttemptCount)
}
