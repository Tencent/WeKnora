package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// ImageVectorService schedules and executes image encoding independently of
// document parsing and VLM enrichment. Both Redis and Lite dispatch this handler.
type ImageVectorService struct {
	jobs      *repository.ImageVectorRepository
	kb        interfaces.KnowledgeBaseService
	models    interfaces.ModelService
	chunks    interfaces.ChunkRepository
	tenants   interfaces.TenantRepository
	engines   interfaces.RetrieveEngineRegistry
	ownership retriever.TenantStoreOwnership
	queue     interfaces.TaskEnqueuer
}

// NewImageVectorService wires image indexing without a VLM dependency.
func NewImageVectorService(jobs *repository.ImageVectorRepository, kb interfaces.KnowledgeBaseService,
	models interfaces.ModelService, chunks interfaces.ChunkRepository, tenants interfaces.TenantRepository,
	engines interfaces.RetrieveEngineRegistry, ownership retriever.TenantStoreOwnership,
	queue interfaces.TaskEnqueuer,
) *ImageVectorService {
	return &ImageVectorService{
		jobs: jobs, kb: kb, models: models, chunks: chunks, tenants: tenants,
		engines: engines, ownership: ownership, queue: queue,
	}
}

// imageEncodingFingerprint changes with the vector space or image preparation
// contract, but not API credentials. The hash is never a usable secret.
func imageEncodingFingerprint(m *types.Model, e embedding.Embedder) string {
	imageModel, _ := embedding.AsImageEmbedder(e)
	var limits imageprep.Limits
	if imageModel != nil {
		limits = imageModel.ImageLimits()
	}
	data, _ := json.Marshal([]any{
		"image-vector/v1", m.ID, m.Name, m.Source, m.Parameters.Provider,
		m.Parameters.BaseURL, m.Parameters.InterfaceType, m.Parameters.EmbeddingParameters, m.Parameters.Spec,
		e.GetDimensions(), limits,
	})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (s *ImageVectorService) configuration(
	ctx context.Context, kbID string,
) (*types.KnowledgeBase, embedding.Embedder, string, error) {
	kb, err := s.kb.GetKnowledgeBaseByIDOnly(ctx, kbID)
	if err != nil {
		return nil, nil, "", err
	}
	if kb == nil || kb.TenantID != types.MustTenantIDFromContext(ctx) {
		return nil, nil, "", apperrors.NewForbiddenError("knowledge base is outside the execution workspace")
	}
	if kb.EmbeddingModelID == "" {
		return kb, nil, "", nil
	}
	model, err := s.models.GetModelByID(ctx, kb.EmbeddingModelID)
	if err != nil {
		return kb, nil, "", err
	}
	embedder, err := s.models.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return kb, nil, "", err
	}
	return kb, embedder, imageEncodingFingerprint(model, embedder), nil
}

// Coverage reports the current model configuration and indexing progress.
func (s *ImageVectorService) Coverage(ctx context.Context, kbID string) (*types.ImageVectorCoverage, error) {
	kb, model, fingerprint, err := s.configuration(ctx, kbID)
	if err != nil {
		return nil, err
	}
	out, err := s.jobs.Coverage(ctx, kb.TenantID, kbID, fingerprint)
	if err != nil {
		return nil, err
	}
	out.Enabled = kb.IsImageVectorEnabled()
	_, out.Supported = embedding.AsImageEmbedder(model)
	return out, nil
}

// Schedule only queues a bounded scanning task. It never encodes images on the
// HTTP or document parsing goroutine. The worker scans all pages, not just the UI page.
func (s *ImageVectorService) Schedule(ctx context.Context, kbID, knowledgeID, sourceType string) error {
	kb, model, _, err := s.configuration(ctx, kbID)
	if err != nil {
		return err
	}
	if !kb.IsImageVectorEnabled() {
		return apperrors.NewBadRequestError("image vector indexing is disabled")
	}
	if _, ok := embedding.AsImageEmbedder(model); !ok {
		return apperrors.NewBadRequestError("embedding model does not support images")
	}
	payload := imageVectorScan{
		TenantID: kb.TenantID, KnowledgeBaseID: kbID, KnowledgeID: knowledgeID, SourceType: sourceType,
	}
	data, _ := json.Marshal(payload)
	_, err = s.queue.Enqueue(asynq.NewTask(types.TypeImageVectorBackfill, data),
		asynq.Queue(types.QueueMultimodal), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
	return err
}

type imageVectorScan struct {
	TenantID        uint64 `json:"tenant_id"`
	KnowledgeBaseID string `json:"knowledge_base_id"`
	KnowledgeID     string `json:"knowledge_id,omitempty"`
	SourceType      string `json:"source_type,omitempty"`
}

// Handle dispatches image jobs and paged backfill scans.
func (s *ImageVectorService) Handle(ctx context.Context, task *asynq.Task) error {
	if task.Type() == types.TypeImageVectorBackfill {
		return s.backfill(ctx, task.Payload())
	}
	var payload types.ImageVectorPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("%w: malformed image job", asynq.SkipRetry)
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	job, err := s.jobs.Get(ctx, payload.TenantID, payload.JobID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if job.KnowledgeBaseID != payload.KnowledgeBaseID || job.KnowledgeID != payload.KnowledgeID {
		return fmt.Errorf("%w: image job scope mismatch", asynq.SkipRetry)
	}
	token := uuid.NewString()
	claimed, err := s.jobs.Claim(ctx, job, token)
	if err != nil {
		return err
	}
	if !claimed {
		if job.Status == "completed" || job.Status == "skipped" {
			return nil
		}
		return errors.New("image vector job is already leased; retry later")
	}
	workCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	status, reason, workErr := s.run(workCtx, job)
	finishCtx, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer finishCancel()
	if err := s.jobs.Finish(finishCtx, job, token, status, reason); err != nil {
		return errors.Join(workErr, err)
	}
	return workErr
}

func (s *ImageVectorService) backfill(ctx context.Context, data []byte) error {
	var payload imageVectorScan
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("%w: malformed image scan", asynq.SkipRetry)
	}
	ctx = types.WithExecutionTenant(ctx, payload.TenantID)
	kb, model, fingerprint, err := s.configuration(ctx, payload.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if !kb.IsImageVectorEnabled() {
		return nil
	}
	if _, ok := embedding.AsImageEmbedder(model); !ok {
		return nil
	}
	for offset := 0; ; offset += 100 {
		rows, err := s.jobs.Sources(ctx, kb.TenantID, kb.ID, payload.KnowledgeID, offset, 100)
		if err != nil {
			return err
		}
		for _, row := range rows {
			identity := fmt.Sprintf("%d/%s/%s/%s", kb.TenantID, kb.ID, row.KnowledgeID, row.ImageURL)
			id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
			job := &types.ImageVectorJob{
				ID: id, TenantID: kb.TenantID, KnowledgeBaseID: kb.ID, KnowledgeID: row.KnowledgeID,
				SourceChunkID: row.ChunkID, ImageURL: row.ImageURL, Fingerprint: fingerprint,
				ChunkID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("image-vector/"+id)).String(), Status: "pending",
			}
			if payload.SourceType == "scanned_pdf" {
				job.Status, job.Reason = "skipped", "scanned_page"
			}
			queue, err := s.jobs.Ensure(ctx, job)
			if err != nil {
				return err
			}
			if !queue {
				continue
			}
			body, _ := json.Marshal(types.ImageVectorPayload{
				TenantID: job.TenantID, KnowledgeBaseID: kb.ID, KnowledgeID: job.KnowledgeID, JobID: id,
			})
			_, err = s.queue.Enqueue(asynq.NewTask(types.TypeImageVector, body),
				asynq.Queue(types.QueueMultimodal), asynq.MaxRetry(3), asynq.Timeout(9*time.Minute))
			if err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}

func (s *ImageVectorService) run(ctx context.Context, job *types.ImageVectorJob) (string, string, error) {
	fail := func(err error) (string, string, error) { return "failed", "indexing_failed", err }
	kb, model, fingerprint, err := s.configuration(ctx, job.KnowledgeBaseID)
	if err != nil {
		return fail(err)
	}
	if !kb.IsImageVectorEnabled() || fingerprint != job.Fingerprint {
		return "skipped", "configuration_changed", nil
	}
	imageModel, ok := embedding.AsImageEmbedder(model)
	if !ok {
		return "skipped", "unsupported_model", nil
	}
	source, err := s.jobs.Source(ctx, job)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "skipped", "source_removed", nil
	}
	if err != nil {
		return fail(err)
	}
	original, err := s.chunks.GetChunkByID(ctx, job.TenantID, job.SourceChunkID)
	if err != nil {
		return fail(err)
	}
	var originals []types.ImageInfo
	if original != nil {
		_ = json.Unmarshal([]byte(original.ImageInfo), &originals)
	}
	for _, image := range originals {
		if (image.URL == job.ImageURL || image.OriginalURL == job.ImageURL) && image.SourceType == "scanned_pdf" {
			return "skipped", "scanned_page", nil
		}
	}
	info, _ := json.Marshal([]types.ImageInfo{{
		URL: job.ImageURL, OriginalURL: job.ImageURL, Caption: source.Caption, OCRText: source.OCRText,
	}})
	data, err := s.kb.ReadChunkImage(ctx, &types.SearchResult{
		ID: source.ChunkID, KnowledgeBaseID: kb.ID, ImageInfo: string(info),
	})
	if err != nil {
		return fail(err)
	}
	prepared, err := imageprep.Prepare(data, imageModel.ImageLimits())
	if err != nil {
		return fail(err)
	}
	vectors, err := imageModel.BatchEmbedImages(ctx, []embedding.Image{prepared})
	if err != nil {
		return fail(err)
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 ||
		(model.GetDimensions() > 0 && len(vectors[0]) != model.GetDimensions()) {
		return fail(errors.New("image embedding returned an invalid vector shape"))
	}
	for _, value := range vectors[0] {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fail(errors.New("image embedding returned non-finite values"))
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	tenant, err := s.tenants.GetTenantByID(ctx, kb.TenantID)
	if err != nil {
		return fail(err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	engine, err := retriever.CreateRetrieveEngineForKB(ctx, s.engines, s.ownership, kb.TenantID, kb.VectorStoreID)
	if err != nil {
		return fail(err)
	}
	// No fabricated caption: image-only evidence remains meaningful to visual rerank/chat.
	content := strings.TrimSpace(source.Caption)
	if content == "" {
		content = strings.TrimSpace(source.OCRText)
	}
	if content == "" {
		content = "![image](" + job.ImageURL + ")"
	}
	current, _, currentFingerprint, err := s.configuration(ctx, kb.ID)
	if err != nil {
		return fail(err)
	}
	if !current.IsImageVectorEnabled() || currentFingerprint != fingerprint {
		return "skipped", "configuration_changed", nil
	}
	chunk := &types.Chunk{
		ID: job.ChunkID, TenantID: job.TenantID, KnowledgeID: job.KnowledgeID, KnowledgeBaseID: kb.ID,
		ChunkType: types.ChunkTypeImageVector, ParentChunkID: source.ChunkID, Content: content, ImageInfo: string(info),
		IsEnabled: false, Flags: types.ChunkFlagRecommended, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if original != nil {
		chunk.SourceLocators = original.SourceLocators
		chunk.TagID = original.TagID
	}
	if err := s.jobs.SaveVectorChunk(ctx, job, chunk); err != nil {
		return fail(err)
	}
	err = engine.BatchIndexVectors(ctx, model, []*types.IndexInfo{{
		Content: content, SourceID: chunk.ID,
		SourceType: types.ImageSourceType, ChunkID: chunk.ID, KnowledgeID: job.KnowledgeID, KnowledgeBaseID: kb.ID,
		IsEnabled: true, IsRecommended: true,
	}}, vectors)
	if err != nil {
		return fail(err)
	}
	// Reparse/deletion can race a vendor request. Never resurrect its removed source.
	_, err = s.jobs.Source(ctx, job)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanup := engine.DeleteByChunkIDList(cleanupCtx, []string{chunk.ID}, model.GetDimensions(), "")
		cleanup = errors.Join(cleanup, s.chunks.DeleteChunk(cleanupCtx, job.TenantID, chunk.ID))
		if cleanup != nil {
			return fail(errors.Join(err, cleanup))
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "skipped", "source_removed", nil
		}
		return fail(err)
	}
	fields := map[string]any{"status": int(types.ChunkStatusIndexed), "is_enabled": true}
	if err := s.chunks.UpdateChunkFieldsByIDs(ctx, job.TenantID, []string{chunk.ID}, fields); err != nil {
		return fail(err)
	}
	return "completed", "", nil
}
