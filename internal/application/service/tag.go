package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// knowledgeTagService implements KnowledgeTagService.
type knowledgeTagService struct {
	kbService      interfaces.KnowledgeBaseService
	repo           interfaces.KnowledgeTagRepository
	chunkRepo      interfaces.ChunkRepository
	retrieveEngine interfaces.RetrieveEngineRegistry
	modelService   interfaces.ModelService
	task           *asynq.Client
}

// NewKnowledgeTagService creates a new tag service.
func NewKnowledgeTagService(
	kbService interfaces.KnowledgeBaseService,
	repo interfaces.KnowledgeTagRepository,
	chunkRepo interfaces.ChunkRepository,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	modelService interfaces.ModelService,
	task *asynq.Client,
) (interfaces.KnowledgeTagService, error) {
	return &knowledgeTagService{
		kbService:      kbService,
		repo:           repo,
		chunkRepo:      chunkRepo,
		retrieveEngine: retrieveEngine,
		modelService:   modelService,
		task:           task,
	}, nil
}

// ListTags lists all tags for a knowledge base with usage stats.
func (s *knowledgeTagService) ListTags(
	ctx context.Context,
	kbID string,
	page *types.Pagination,
	keyword string,
) (*types.PageResult, error) {
	if kbID == "" {
		return nil, werrors.NewBadRequestError("知识库ID不能为空")
	}
	if page == nil {
		page = &types.Pagination{}
	}
	keyword = strings.TrimSpace(keyword)
	// Ensure KB exists and belongs to current tenant
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	tenantID := kb.TenantID

	tags, total, err := s.repo.ListByKB(ctx, tenantID, kbID, page, keyword)
	if err != nil {
		return nil, err
	}

	results := make([]*types.KnowledgeTagWithStats, 0, len(tags)+1)

	// Add untagged pseudo-tag at the beginning (only on first page and when no keyword filter)
	if page.Page <= 1 && keyword == "" {
		untaggedKCount, untaggedCCount, err := s.repo.CountUntaggedReferences(ctx, tenantID, kbID)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"kb_id": kbID,
			})
			return nil, err
		}
		results = append(results, &types.KnowledgeTagWithStats{
			KnowledgeTag: types.KnowledgeTag{
				ID:              types.UntaggedTagID,
				TenantID:        tenantID,
				KnowledgeBaseID: kbID,
				Name:            "未分类",
			},
			KnowledgeCount: untaggedKCount,
			ChunkCount:     untaggedCCount,
		})
	}

	for _, tag := range tags {
		if tag == nil {
			continue
		}
		kCount, cCount, err := s.repo.CountReferences(ctx, tenantID, kbID, tag.ID)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"kb_id":  kbID,
				"tag_id": tag.ID,
			})
			return nil, err
		}
		results = append(results, &types.KnowledgeTagWithStats{
			KnowledgeTag:   *tag,
			KnowledgeCount: kCount,
			ChunkCount:     cCount,
		})
	}

	// Adjust total to include the untagged pseudo-tag
	if keyword == "" {
		total++
	}

	return types.NewPageResult(total, page, results), nil
}

// CreateTag creates a new tag under a KB.
func (s *knowledgeTagService) CreateTag(
	ctx context.Context,
	kbID string,
	name string,
	color string,
	sortOrder int,
) (*types.KnowledgeTag, error) {
	name = strings.TrimSpace(name)
	if kbID == "" || name == "" {
		return nil, werrors.NewBadRequestError("知识库ID和标签名称不能为空")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Check if tag with same name already exists
	existingTag, err := s.repo.GetByName(ctx, kb.TenantID, kbID, name)
	if err == nil && existingTag != nil {
		return nil, werrors.NewConflictError("标签名称已存在")
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now()
	tag := &types.KnowledgeTag{
		ID:              uuid.New().String(),
		TenantID:        kb.TenantID,
		KnowledgeBaseID: kb.ID,
		Name:            name,
		Color:           strings.TrimSpace(color),
		SortOrder:       sortOrder,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.repo.Create(ctx, tag); err != nil {
		return nil, err
	}
	return tag, nil
}

// UpdateTag updates tag basic information.
func (s *knowledgeTagService) UpdateTag(
	ctx context.Context,
	id string,
	name *string,
	color *string,
	sortOrder *int,
) (*types.KnowledgeTag, error) {
	if id == "" {
		return nil, werrors.NewBadRequestError("标签ID不能为空")
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	tag, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}

	if name != nil {
		newName := strings.TrimSpace(*name)
		if newName == "" {
			return nil, werrors.NewBadRequestError("标签名称不能为空")
		}
		tag.Name = newName
	}
	if color != nil {
		tag.Color = strings.TrimSpace(*color)
	}
	if sortOrder != nil {
		tag.SortOrder = *sortOrder
	}
	tag.UpdatedAt = time.Now()
	if err := s.repo.Update(ctx, tag); err != nil {
		return nil, err
	}
	return tag, nil
}

// DeleteTag deletes a tag. When force=true, also deletes all chunks under this tag.
// When contentOnly=true, only deletes the content under the tag but keeps the tag itself.
func (s *knowledgeTagService) DeleteTag(ctx context.Context, id string, force bool, contentOnly bool, excludeIDs []string) error {
	if id == "" {
		return werrors.NewBadRequestError("标签ID不能为空")
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	tag, err := s.repo.GetByID(ctx, tenantID, id)
	if err != nil {
		return err
	}

	// Get KB info for embedding model
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, tag.KnowledgeBaseID)
	if err != nil {
		return err
	}

	kCount, cCount, err := s.repo.CountReferences(ctx, tenantID, tag.KnowledgeBaseID, tag.ID)
	if err != nil {
		return err
	}

	// Get tenant info for effective engines
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)

	// Helper function to delete chunks and enqueue index deletion task
	deleteChunksAndEnqueueIndexDelete := func() error {
		// Delete chunks and get their IDs
		deletedIDs, err := s.chunkRepo.DeleteChunksByTagID(ctx, tenantID, tag.KnowledgeBaseID, tag.ID, excludeIDs)
		if err != nil {
			logger.Errorf(ctx, "Failed to delete chunks by tag ID %s: %v", tag.ID, err)
			return werrors.NewInternalServerError("删除标签下的数据失败")
		}

		// Enqueue async index deletion task for the deleted chunks
		if len(deletedIDs) > 0 {
			s.enqueueIndexDeleteTask(ctx, tenantID, kb.ID, kb.EmbeddingModelID, string(kb.Type), deletedIDs, tenantInfo.GetEffectiveEngines())
		}

		logger.Infof(ctx, "Deleted %d chunks under tag %s", len(deletedIDs), tag.ID)
		return nil
	}

	// contentOnly mode: only delete content, keep the tag
	if contentOnly {
		if cCount > 0 {
			if err := deleteChunksAndEnqueueIndexDelete(); err != nil {
				return err
			}
		}
		return nil
	}

	if !force && (kCount > 0 || cCount > 0) {
		return werrors.NewBadRequestError("标签仍有知识或FAQ条目引用，无法删除")
	}
	// When force=true, delete all chunks under this tag first
	if force && cCount > 0 {
		if err := deleteChunksAndEnqueueIndexDelete(); err != nil {
			return err
		}
	}
	// If there are excludeIDs, we cannot delete the tag itself as it still has content
	if len(excludeIDs) > 0 {
		return nil
	}
	return s.repo.Delete(ctx, tenantID, id)
}

// enqueueIndexDeleteTask enqueues an async task for index deletion (low priority)
func (s *knowledgeTagService) enqueueIndexDeleteTask(ctx context.Context,
	tenantID uint64, kbID, embeddingModelID, kbType string, chunkIDs []string, effectiveEngines []types.RetrieverEngineParams,
) {
	payload := types.IndexDeletePayload{
		TenantID:         tenantID,
		KnowledgeBaseID:  kbID,
		EmbeddingModelID: embeddingModelID,
		KBType:           kbType,
		ChunkIDs:         chunkIDs,
		EffectiveEngines: effectiveEngines,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		logger.Errorf(ctx, "Failed to marshal index delete payload: %v", err)
		return
	}

	task := asynq.NewTask(types.TypeIndexDelete, payloadBytes, asynq.Queue("low"), asynq.MaxRetry(10))
	info, err := s.task.Enqueue(task)
	if err != nil {
		logger.Errorf(ctx, "Failed to enqueue index delete task: %v", err)
		return
	}
	logger.Infof(ctx, "Enqueued index delete task: %s for %d chunks", info.ID, len(chunkIDs))
}

// ProcessIndexDelete handles async index deletion task
func (s *knowledgeTagService) ProcessIndexDelete(ctx context.Context, t *asynq.Task) error {
	var payload types.IndexDeletePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal index delete payload: %v", err)
		return err
	}

	// Set tenant context for downstream services
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	logger.Infof(ctx, "Processing index delete task for %d chunks in KB %s", len(payload.ChunkIDs), payload.KnowledgeBaseID)

	// Create retrieve engine
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, payload.EffectiveEngines)
	if err != nil {
		logger.Warnf(ctx, "Failed to create retrieve engine for index cleanup: %v", err)
		return err
	}

	// Get embedding model dimensions
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, payload.EmbeddingModelID)
	if err != nil {
		logger.Warnf(ctx, "Failed to get embedding model for index cleanup: %v", err)
		return err
	}

	// Delete indices in batches to avoid overwhelming the backend
	const batchSize = 100
	chunkIDs := payload.ChunkIDs
	dimension := embeddingModel.GetDimensions()

	for i := 0; i < len(chunkIDs); i += batchSize {
		end := i + batchSize
		if end > len(chunkIDs) {
			end = len(chunkIDs)
		}
		batch := chunkIDs[i:end]

		if err := retrieveEngine.DeleteByChunkIDList(ctx, batch, dimension, payload.KBType); err != nil {
			logger.Warnf(ctx, "Failed to delete indices for chunks batch [%d-%d]: %v", i, end, err)
			return err
		}
		logger.Debugf(ctx, "Deleted indices batch [%d-%d] of %d chunks", i, end, len(chunkIDs))
	}

	logger.Infof(ctx, "Successfully deleted indices for %d chunks", len(payload.ChunkIDs))
	return nil
}

// FindOrCreateTagByName finds a tag by name or creates it if not exists.
func (s *knowledgeTagService) FindOrCreateTagByName(ctx context.Context, kbID string, name string) (*types.KnowledgeTag, error) {
	name = strings.TrimSpace(name)
	if kbID == "" || name == "" {
		return nil, werrors.NewBadRequestError("知识库ID和标签名称不能为空")
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}

	tenantID := kb.TenantID

	// 先尝试查找现有标签
	tag, err := s.repo.GetByName(ctx, tenantID, kbID, name)
	if err == nil {
		return tag, nil
	}

	// 如果不是 not found 错误，直接返回
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// 创建新标签
	return s.CreateTag(ctx, kbID, name, "", 0)
}
