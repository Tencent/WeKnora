package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// multimodalObservationModelService exposes the counting VLM through the same
// ModelService interface used by ImageMultimodalService in production.
type multimodalObservationModelService struct {
	interfaces.ModelService

	model vlm.VLM
	err   error
}

func (s *multimodalObservationModelService) GetVLMModel(
	_ context.Context,
	_ string,
) (vlm.VLM, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.model, nil
}

// multimodalObservationKBService returns a fixed knowledge base configuration
// for a multimodal handler test.
type multimodalObservationKBService struct {
	interfaces.KnowledgeBaseService

	knowledgeBase *types.KnowledgeBase
	err           error
}

func (s *multimodalObservationKBService) GetKnowledgeBaseByIDOnly(
	_ context.Context,
	_ string,
) (*types.KnowledgeBase, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.knowledgeBase, nil
}

// multimodalObservationKnowledgeRepo returns one live knowledge row so the
// orphan/supersede guard allows the task to reach the VLM calls.
type multimodalObservationKnowledgeRepo struct {
	interfaces.KnowledgeRepository

	knowledge *types.Knowledge
	err       error
}

func (r *multimodalObservationKnowledgeRepo) GetKnowledgeByIDOnly(
	_ context.Context,
	_ string,
) (*types.Knowledge, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.knowledge, nil
}

// multimodalObservationChunkService stores generated OCR/caption chunks in
// memory. The production handler re-reads the chunks while marking them as
// indexed, so the fake implements CreateChunks, GetChunkByIDOnly, and
// UpdateChunk.
type multimodalObservationChunkService struct {
	interfaces.ChunkService

	mu        sync.Mutex
	chunks    map[string]*types.Chunk
	createErr error
}

func newMultimodalObservationChunkService() *multimodalObservationChunkService {
	return &multimodalObservationChunkService{
		chunks: make(map[string]*types.Chunk),
	}
}

func (s *multimodalObservationChunkService) CreateChunks(
	_ context.Context,
	chunks []*types.Chunk,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return s.createErr
	}

	for _, chunk := range chunks {
		if chunk == nil {
			continue
		}

		copied := *chunk
		s.chunks[chunk.ID] = &copied
	}

	return nil
}

func TestIngestionArtifactRecovery_OCRAndCaptionSucceededDownstreamFailed(t *testing.T) {
	imagePath := writeMultimodalObservationImage(t, []byte("durable multimodal image"))
	model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm-test", ModelName: "counting-vlm", OCRResponse: "recognized text", CaptionResponse: "image caption"})
	chunks := newMultimodalObservationChunkService()
	chunks.createErr = errors.New("multimodal chunk materialization failed")
	service := newImageMultimodalObservationService(model, chunks)
	artifactService, db := newMultimodalArtifactTestService(t)
	service.artifactRepo = artifactService.artifactRepo
	task := newImageMultimodalObservationTask(t, imagePath, "knowledge-test", "chunk-test")

	require.ErrorContains(t, service.Handle(context.Background(), task), "multimodal chunk materialization failed")
	require.Equal(t, 2, model.Snapshot().PredictRequestCount)
	var succeeded int64
	require.NoError(t, db.Model(&types.DerivedArtifact{}).Where("status = ?", types.DerivedArtifactSucceeded).Count(&succeeded).Error)
	require.EqualValues(t, 2, succeeded)

	chunks.mu.Lock()
	chunks.createErr = nil
	chunks.mu.Unlock()
	require.NoError(t, service.Handle(context.Background(), task))
	require.Equal(t, 2, model.Snapshot().PredictRequestCount, "retry must hit both OCR and caption artifacts")
	chunks.mu.Lock()
	require.Len(t, chunks.chunks, 2)
	chunks.mu.Unlock()
}

func (s *multimodalObservationChunkService) GetChunkByIDOnly(
	_ context.Context,
	id string,
) (*types.Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunk, ok := s.chunks[id]
	if !ok {
		return nil, nil
	}

	copied := *chunk
	return &copied, nil
}

func (s *multimodalObservationChunkService) UpdateChunk(
	_ context.Context,
	chunk *types.Chunk,
) error {
	if chunk == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	copied := *chunk
	s.chunks[chunk.ID] = &copied

	return nil
}

func (s *multimodalObservationChunkService) Snapshot() []*types.Chunk {
	s.mu.Lock()
	defer s.mu.Unlock()

	chunks := make([]*types.Chunk, 0, len(s.chunks))
	for _, chunk := range s.chunks {
		copied := *chunk
		chunks = append(chunks, &copied)
	}

	return chunks
}

func TestImageMultimodalObservation_RecordsOCRAndCaptionRequests(
	t *testing.T,
) {
	imageBytes := []byte("fixed-image-bytes")
	imagePath := writeMultimodalObservationImage(t, imageBytes)

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "recognized text",
			CaptionResponse: "an image caption",
		},
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)

	task := newImageMultimodalObservationTask(
		t,
		imagePath,
		"knowledge-test",
		"chunk-test",
	)

	err := service.Handle(context.Background(), task)
	require.NoError(t, err)

	snapshot := countingVLM.Snapshot()

	require.Equal(t, 2, snapshot.PredictRequestCount)
	require.Equal(t, 1, snapshot.OCRRequestCount)
	require.Equal(t, 1, snapshot.CaptionRequestCount)
	require.Equal(t, 2, snapshot.InputImageCount)
	require.Equal(t, int64(len(imageBytes)*2), snapshot.InputBytes)
	require.Len(t, snapshot.Calls, 2)

	require.Equal(
		t,
		types.IngestionOperationMultimodalOCR,
		snapshot.Calls[0].Operation,
	)
	require.Equal(
		t,
		types.IngestionOperationMultimodalCaption,
		snapshot.Calls[1].Operation,
	)

	chunks := chunkService.Snapshot()
	require.Len(t, chunks, 2)
	requireChunkTypeContent(
		t,
		chunks,
		types.ChunkTypeImageOCR,
		"recognized text",
	)
	requireChunkTypeContent(
		t,
		chunks,
		types.ChunkTypeImageCaption,
		"an image caption",
	)
}

func TestImageMultimodalArtifactCache_RebuildReusesOCRAndCaption(
	t *testing.T,
) {
	imageBytes := []byte("fixed-image-bytes")
	imagePath := writeMultimodalObservationImage(t, imageBytes)

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "recognized text",
			CaptionResponse: "an image caption",
		},
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	artifactService, _ := newMultimodalArtifactTestService(t)
	service.artifactRepo = artifactService.artifactRepo

	firstTask := newImageMultimodalObservationTask(
		t,
		imagePath,
		"knowledge-test",
		"chunk-test",
	)
	require.NoError(
		t,
		service.Handle(context.Background(), firstTask),
	)

	firstSnapshot := countingVLM.Snapshot()
	require.Equal(t, 2, firstSnapshot.PredictRequestCount)
	require.Equal(t, 1, firstSnapshot.OCRRequestCount)
	require.Equal(t, 1, firstSnapshot.CaptionRequestCount)
	require.Equal(t, 2, firstSnapshot.InputImageCount)

	secondTask := newImageMultimodalObservationTask(
		t,
		imagePath,
		"knowledge-test",
		"chunk-test",
	)
	require.NoError(
		t,
		service.Handle(context.Background(), secondTask),
	)

	secondSnapshot := countingVLM.Snapshot()

	require.Equal(t, 2, secondSnapshot.PredictRequestCount)
	require.Equal(t, 1, secondSnapshot.OCRRequestCount)
	require.Equal(t, 1, secondSnapshot.CaptionRequestCount)
	require.Equal(t, 2, secondSnapshot.InputImageCount)
	require.Equal(t, int64(len(imageBytes)*2), secondSnapshot.InputBytes)

	require.Equal(
		t,
		0,
		secondSnapshot.OCRRequestCount-firstSnapshot.OCRRequestCount,
	)
	require.Equal(
		t,
		0,
		secondSnapshot.CaptionRequestCount-firstSnapshot.CaptionRequestCount,
	)
}

func TestImageMultimodalArtifactCache_HitObservationHasZeroRequests(t *testing.T) {
	ctx := context.Background()
	tracker, db := setupSpanTrackerTest(t)
	imagePath := writeMultimodalObservationImage(t, []byte("observation-cache-image"))
	model := modelcount.NewCountingVLM(modelcount.CountingVLMOptions{ModelID: "vlm-test", OCRResponse: "ocr", CaptionResponse: "caption"})
	service := newImageMultimodalObservationService(model, newMultimodalObservationChunkService())
	artifactService, _ := newMultimodalArtifactTestService(t)
	service.artifactRepo = artifactService.artifactRepo
	service.spanTracker = tracker

	run := func() types.KnowledgeProcessingSpan {
		_, attempt, err := tracker.OpenAttempt(ctx, "knowledge-test", "")
		require.NoError(t, err)
		require.NotNil(t, tracker.BeginStage(ctx, "knowledge-test", attempt, types.StageMultimodal, nil))
		require.NoError(t, service.Handle(ctx, newImageMultimodalObservationTaskWithOptions(t, imagePath, attempt, true, true)))
		return loadMultimodalObservationSpan(t, db, attempt)
	}
	miss := run()
	hit := run()
	require.Equal(t, string(types.IngestionCacheStatusMiss), miss.Output["ocr_cache_status"])
	require.Equal(t, string(types.ArtifactCacheComputed), miss.Output["ocr_artifact_cache_event"])
	require.EqualValues(t, 1, miss.Output["ocr_request_count"])
	require.EqualValues(t, 1, miss.Output["ocr_computed_items"])
	require.EqualValues(t, 0, miss.Output["ocr_reused_items"])
	require.Equal(t, string(types.IngestionCacheStatusHit), hit.Output["ocr_cache_status"])
	require.Equal(t, string(types.IngestionCacheStatusHit), hit.Output["caption_cache_status"])
	require.Equal(t, string(types.ArtifactCacheHit), hit.Output["ocr_artifact_cache_event"])
	require.Equal(t, string(types.ArtifactCacheHit), hit.Output["caption_artifact_cache_event"])
	require.EqualValues(t, 0, hit.Output["ocr_request_count"])
	require.EqualValues(t, 0, hit.Output["caption_request_count"])
	require.EqualValues(t, 1, hit.Output["ocr_reused_items"])
	require.EqualValues(t, 1, hit.Output["caption_reused_items"])
	require.EqualValues(t, 0, hit.Output["ocr_computed_items"])
	require.EqualValues(t, 0, hit.Output["caption_computed_items"])
	require.Equal(t, 2, model.Snapshot().PredictRequestCount)
}

func newImageMultimodalObservationService(
	model vlm.VLM,
	chunkService interfaces.ChunkService,
) *ImageMultimodalService {
	knowledgeBase := &types.KnowledgeBase{
		ID:       "kb-test",
		TenantID: 1,
		VLMConfig: types.VLMConfig{
			Enabled: true,
			ModelID: "vlm-test",
		},
	}

	return &ImageMultimodalService{
		chunkService: chunkService,
		modelService: &multimodalObservationModelService{
			model: model,
		},
		kbService: &multimodalObservationKBService{
			knowledgeBase: knowledgeBase,
		},
		knowledgeRepo: &multimodalObservationKnowledgeRepo{
			knowledge: &types.Knowledge{
				ID:              "knowledge-test",
				TenantID:        1,
				KnowledgeBaseID: "kb-test",
				ParseStatus:     types.ParseStatusProcessing,
			},
		},
	}
}

func newImageMultimodalObservationTask(
	t *testing.T,
	imagePath string,
	knowledgeID string,
	chunkID string,
) *asynq.Task {
	t.Helper()

	payload, err := json.Marshal(types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     knowledgeID,
		KnowledgeBaseID: "kb-test",
		ChunkID:         chunkID,
		ImageLocalPath:  imagePath,
		EnableOCR:       true,
		EnableCaption:   true,
		Language:        "en-US",
		ImageIndex:      0,
	})
	require.NoError(t, err)

	return asynq.NewTask(types.TypeImageMultimodal, payload)
}

func writeMultimodalObservationImage(
	t *testing.T,
	imageBytes []byte,
) string {
	t.Helper()

	imagePath := t.TempDir() + string(os.PathSeparator) + "image.png"
	require.NoError(
		t,
		os.WriteFile(imagePath, imageBytes, 0o600),
	)

	return imagePath
}

func requireChunkTypeContent(
	t *testing.T,
	chunks []*types.Chunk,
	chunkType types.ChunkType,
	content string,
) {
	t.Helper()

	for _, chunk := range chunks {
		if chunk == nil || chunk.ChunkType != chunkType {
			continue
		}

		require.Equal(t, content, chunk.Content)
		return
	}

	t.Fatalf(
		"chunk type %s with content %q was not created",
		chunkType,
		content,
	)
}

func TestImageMultimodalObservation_SpanMatchesCountingVLM(
	t *testing.T,
) {
	ctx := context.Background()

	tracker, db := setupSpanTrackerTest(t)

	_, attempt, err := tracker.OpenAttempt(
		ctx,
		"knowledge-test",
		"",
	)
	require.NoError(t, err)
	require.Positive(t, attempt)

	multimodalSpan := tracker.BeginStage(
		ctx,
		"knowledge-test",
		attempt,
		types.StageMultimodal,
		nil,
	)
	require.NotNil(t, multimodalSpan)

	imageBytes := []byte("fixed-image-bytes")
	imagePath := writeMultimodalObservationImage(
		t,
		imageBytes,
	)

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "recognized text",
			CaptionResponse: "an image caption",
		},
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	service.spanTracker = tracker
	artifactService, _ := newMultimodalArtifactTestService(t)
	service.artifactRepo = artifactService.artifactRepo

	payload, err := json.Marshal(types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "knowledge-test",
		KnowledgeBaseID: "kb-test",
		ChunkID:         "chunk-test",
		ImageLocalPath:  imagePath,
		EnableOCR:       true,
		EnableCaption:   true,
		Language:        "en-US",
		Attempt:         attempt,
		ImageIndex:      0,
	})
	require.NoError(t, err)

	task := asynq.NewTask(
		types.TypeImageMultimodal,
		payload,
	)

	require.NoError(
		t,
		service.Handle(ctx, task),
	)

	snapshot := countingVLM.Snapshot()
	require.Equal(t, 2, snapshot.PredictRequestCount)
	require.Equal(t, 1, snapshot.OCRRequestCount)
	require.Equal(t, 1, snapshot.CaptionRequestCount)
	require.Equal(t, 2, snapshot.InputImageCount)

	var imageSpan types.KnowledgeProcessingSpan
	require.NoError(
		t,
		db.Where(
			"knowledge_id = ? AND attempt = ? AND name = ?",
			"knowledge-test",
			attempt,
			"multimodal.image[0]",
		).Take(&imageSpan).Error,
	)

	require.Equal(
		t,
		types.SpanStatusDone,
		imageSpan.Status,
	)
	require.NotNil(t, imageSpan.Output)

	require.Equal(
		t,
		string(types.IngestionOperationMultimodalOCR),
		imageSpan.Output["ocr_operation"],
	)
	require.Equal(
		t,
		string(types.IngestionOperationMultimodalCaption),
		imageSpan.Output["caption_operation"],
	)
	require.NotContains(t, imageSpan.Output, "cache_status")
	require.Equal(t, string(types.IngestionCacheStatusMiss), imageSpan.Output["ocr_cache_status"])
	require.Equal(t, string(types.IngestionCacheStatusMiss), imageSpan.Output["caption_cache_status"])
	require.EqualValues(t, 1, imageSpan.Output["ocr_computed_items"])
	require.EqualValues(t, 1, imageSpan.Output["caption_computed_items"])
	require.EqualValues(t, 0, imageSpan.Output["ocr_reused_items"])
	require.EqualValues(t, 0, imageSpan.Output["caption_reused_items"])
	require.Equal(t, string(types.ArtifactCacheComputed), imageSpan.Output["ocr_artifact_cache_event"])
	require.Equal(t, string(types.ArtifactCacheComputed), imageSpan.Output["caption_artifact_cache_event"])

	require.EqualValues(
		t,
		snapshot.OCRRequestCount,
		imageSpan.Output["ocr_request_count"],
	)
	require.EqualValues(
		t,
		snapshot.CaptionRequestCount,
		imageSpan.Output["caption_request_count"],
	)
	require.EqualValues(
		t,
		snapshot.InputImageCount/2,
		imageSpan.Output["ocr_input_images"],
	)
	require.EqualValues(
		t,
		snapshot.InputImageCount/2,
		imageSpan.Output["caption_input_images"],
	)

	require.EqualValues(
		t,
		len(imageBytes),
		imageSpan.Output["image_bytes"],
	)
	require.EqualValues(
		t,
		2,
		imageSpan.Output["chunks_created"],
	)

	require.Equal(
		t,
		true,
		imageSpan.Output["ocr_enabled"],
	)
	require.Equal(
		t,
		true,
		imageSpan.Output["caption_enabled"],
	)
}

func TestImageMultimodalObservation_OCRFailureStillCountsRequest(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db, attempt := beginMultimodalObservationSpan(
		t,
		ctx,
	)

	expectedError := errors.New("OCR provider failed")
	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRError:        expectedError,
			CaptionResponse: "an image caption",
		},
	)

	imagePath := writeMultimodalObservationImage(
		t,
		[]byte("fixed-image-bytes"),
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	service.spanTracker = tracker

	task := newImageMultimodalObservationTaskWithOptions(
		t,
		imagePath,
		attempt,
		true,
		true,
	)

	require.NoError(
		t,
		service.Handle(ctx, task),
	)

	snapshot := countingVLM.Snapshot()

	// The OCR provider returned an error, but Predict was still entered.
	require.Equal(t, 2, snapshot.PredictRequestCount)
	require.Equal(t, 1, snapshot.OCRRequestCount)
	require.Equal(t, 1, snapshot.CaptionRequestCount)

	imageSpan := loadMultimodalObservationSpan(
		t,
		db,
		attempt,
	)

	require.Equal(
		t,
		types.SpanStatusDone,
		imageSpan.Status,
	)
	require.EqualValues(
		t,
		1,
		imageSpan.Output["ocr_request_count"],
	)
	require.EqualValues(
		t,
		1,
		imageSpan.Output["ocr_input_images"],
	)
	require.Equal(
		t,
		expectedError.Error(),
		imageSpan.Output["ocr_error"],
	)
	require.Equal(t, string(types.IngestionCacheStatusError), imageSpan.Output["ocr_cache_status"])
	require.Equal(t, string(types.ArtifactCacheFailed), imageSpan.Output["ocr_artifact_cache_event"])
	require.Equal(t, false, imageSpan.Output["ocr_success"])
	require.EqualValues(t, 1, imageSpan.Output["ocr_computed_items"])
	require.EqualValues(t, 0, imageSpan.Output["ocr_reused_items"])
	require.EqualValues(
		t,
		1,
		imageSpan.Output["caption_request_count"],
	)
	require.EqualValues(
		t,
		1,
		imageSpan.Output["caption_input_images"],
	)

	chunks := chunkService.Snapshot()

	// OCR failed, so only the successful caption result is persisted.
	require.Len(t, chunks, 1)
	requireChunkTypeContent(
		t,
		chunks,
		types.ChunkTypeImageCaption,
		"an image caption",
	)
}

func TestImageMultimodalObservation_EmptyOCRStillCountsRequest(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db, attempt := beginMultimodalObservationSpan(
		t,
		ctx,
	)

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "No text content.",
			CaptionResponse: "an image caption",
		},
	)

	imagePath := writeMultimodalObservationImage(
		t,
		[]byte("fixed-image-bytes"),
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	service.spanTracker = tracker

	task := newImageMultimodalObservationTaskWithOptions(
		t,
		imagePath,
		attempt,
		true,
		true,
	)

	require.NoError(
		t,
		service.Handle(ctx, task),
	)

	snapshot := countingVLM.Snapshot()

	require.Equal(t, 2, snapshot.PredictRequestCount)
	require.Equal(t, 1, snapshot.OCRRequestCount)
	require.Equal(t, 1, snapshot.CaptionRequestCount)

	imageSpan := loadMultimodalObservationSpan(
		t,
		db,
		attempt,
	)

	// The OCR result was empty after sanitization, but the model request still
	// occurred and must remain visible in the observation.
	require.EqualValues(
		t,
		1,
		imageSpan.Output["ocr_request_count"],
	)
	require.EqualValues(
		t,
		1,
		imageSpan.Output["ocr_input_images"],
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["ocr_chars"],
	)
	require.Equal(
		t,
		"empty_or_invalid",
		imageSpan.Output["ocr_skipped"],
	)

	chunks := chunkService.Snapshot()

	// No OCR chunk is created for an empty/invalid response. Caption remains.
	require.Len(t, chunks, 1)
	requireChunkTypeContent(
		t,
		chunks,
		types.ChunkTypeImageCaption,
		"an image caption",
	)
}

func TestImageMultimodalObservation_ImageReadFailureDoesNotCountRequests(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db, attempt := beginMultimodalObservationSpan(
		t,
		ctx,
	)

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "recognized text",
			CaptionResponse: "an image caption",
		},
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	service.spanTracker = tracker

	// The local path does not exist. ImageURL is also empty, so the fallback
	// downloader fails before either VLM operation can be invoked.
	missingImagePath := t.TempDir() +
		string(os.PathSeparator) +
		"missing-image.png"

	task := newImageMultimodalObservationTaskWithOptions(
		t,
		missingImagePath,
		attempt,
		true,
		true,
	)

	// A single unreadable image is skipped by the existing handler rather than
	// failing the whole knowledge task.
	require.NoError(
		t,
		service.Handle(ctx, task),
	)

	snapshot := countingVLM.Snapshot()

	require.Equal(t, 0, snapshot.PredictRequestCount)
	require.Equal(t, 0, snapshot.OCRRequestCount)
	require.Equal(t, 0, snapshot.CaptionRequestCount)
	require.Equal(t, 0, snapshot.InputImageCount)

	imageSpan := loadMultimodalObservationSpan(
		t,
		db,
		attempt,
	)

	require.Equal(
		t,
		types.SpanStatusDone,
		imageSpan.Status,
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["ocr_request_count"],
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["ocr_input_images"],
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["caption_request_count"],
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["caption_input_images"],
	)
	require.Equal(
		t,
		"unreadable_image",
		imageSpan.Output["skipped"],
	)

	require.Empty(
		t,
		chunkService.Snapshot(),
	)
}

func TestImageMultimodalObservation_OCRDisabledDoesNotCountOCRRequest(
	t *testing.T,
) {
	ctx := context.Background()
	tracker, db, attempt := beginMultimodalObservationSpan(
		t,
		ctx,
	)

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "recognized text",
			CaptionResponse: "an image caption",
		},
	)

	imagePath := writeMultimodalObservationImage(
		t,
		[]byte("fixed-image-bytes"),
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	service.spanTracker = tracker

	task := newImageMultimodalObservationTaskWithOptions(
		t,
		imagePath,
		attempt,
		false,
		true,
	)

	require.NoError(
		t,
		service.Handle(ctx, task),
	)

	snapshot := countingVLM.Snapshot()

	require.Equal(t, 1, snapshot.PredictRequestCount)
	require.Equal(t, 0, snapshot.OCRRequestCount)
	require.Equal(t, 1, snapshot.CaptionRequestCount)
	require.Equal(t, 1, snapshot.InputImageCount)

	imageSpan := loadMultimodalObservationSpan(
		t,
		db,
		attempt,
	)

	require.Equal(
		t,
		false,
		imageSpan.Output["ocr_enabled"],
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["ocr_request_count"],
	)
	require.EqualValues(
		t,
		0,
		imageSpan.Output["ocr_input_images"],
	)
	require.EqualValues(
		t,
		1,
		imageSpan.Output["caption_request_count"],
	)
	require.EqualValues(
		t,
		1,
		imageSpan.Output["caption_input_images"],
	)

	chunks := chunkService.Snapshot()
	require.Len(t, chunks, 1)
	requireChunkTypeContent(
		t,
		chunks,
		types.ChunkTypeImageCaption,
		"an image caption",
	)
}

func TestImageMultimodalObservation_VLMResolveFailureDoesNotCountRequests(
	t *testing.T,
) {
	ctx := context.Background()
	expectedError := errors.New("VLM model unavailable")

	countingVLM := modelcount.NewCountingVLM(
		modelcount.CountingVLMOptions{
			ModelID:         "vlm-test",
			ModelName:       "counting-vlm",
			OCRResponse:     "recognized text",
			CaptionResponse: "an image caption",
		},
	)

	imagePath := writeMultimodalObservationImage(
		t,
		[]byte("fixed-image-bytes"),
	)

	chunkService := newMultimodalObservationChunkService()
	service := newImageMultimodalObservationService(
		countingVLM,
		chunkService,
	)
	service.modelService = &multimodalObservationModelService{
		err: expectedError,
	}

	task := newImageMultimodalObservationTaskWithOptions(
		t,
		imagePath,
		0,
		true,
		true,
	)

	err := service.Handle(ctx, task)
	require.Error(t, err)
	require.ErrorIs(t, err, expectedError)

	snapshot := countingVLM.Snapshot()

	// Model resolution failed before Predict was entered.
	require.Equal(t, 0, snapshot.PredictRequestCount)
	require.Equal(t, 0, snapshot.OCRRequestCount)
	require.Equal(t, 0, snapshot.CaptionRequestCount)
	require.Equal(t, 0, snapshot.InputImageCount)

	require.Empty(
		t,
		chunkService.Snapshot(),
	)
}

func beginMultimodalObservationSpan(
	t *testing.T,
	ctx context.Context,
) (SpanTracker, *gorm.DB, int) {
	t.Helper()

	tracker, db := setupSpanTrackerTest(t)

	_, attempt, err := tracker.OpenAttempt(
		ctx,
		"knowledge-test",
		"",
	)
	require.NoError(t, err)
	require.Positive(t, attempt)

	multimodalSpan := tracker.BeginStage(
		ctx,
		"knowledge-test",
		attempt,
		types.StageMultimodal,
		nil,
	)
	require.NotNil(t, multimodalSpan)

	return tracker, db, attempt
}

func loadMultimodalObservationSpan(
	t *testing.T,
	db *gorm.DB,
	attempt int,
) types.KnowledgeProcessingSpan {
	t.Helper()

	var imageSpan types.KnowledgeProcessingSpan
	require.NoError(
		t,
		db.Where(
			"knowledge_id = ? AND attempt = ? AND name = ?",
			"knowledge-test",
			attempt,
			"multimodal.image[0]",
		).Take(&imageSpan).Error,
	)

	require.NotNil(t, imageSpan.Output)

	return imageSpan
}

func newImageMultimodalObservationTaskWithOptions(
	t *testing.T,
	imagePath string,
	attempt int,
	enableOCR bool,
	enableCaption bool,
) *asynq.Task {
	t.Helper()

	payload, err := json.Marshal(types.ImageMultimodalPayload{
		TenantID:        1,
		KnowledgeID:     "knowledge-test",
		KnowledgeBaseID: "kb-test",
		ChunkID:         "chunk-test",
		ImageLocalPath:  imagePath,
		EnableOCR:       enableOCR,
		EnableCaption:   enableCaption,
		Language:        "en-US",
		Attempt:         attempt,
		ImageIndex:      0,
	})
	require.NoError(t, err)

	return asynq.NewTask(
		types.TypeImageMultimodal,
		payload,
	)
}
