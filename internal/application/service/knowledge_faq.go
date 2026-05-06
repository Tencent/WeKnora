package service

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

// ListFAQEntries lists FAQ entries under a FAQ knowledge base.
func (s *knowledgeService) ListFAQEntries(ctx context.Context,
	kbID string, page *types.Pagination, tagSeqID int64, keyword string, searchField string, sortOrder string,
) (*types.PageResult, error) {
	if page == nil {
		page = &types.Pagination{}
	}
	keyword = strings.TrimSpace(keyword)
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Check if this is a shared knowledge base access
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	effectiveTenantID := tenantID

	// If the kb belongs to a different tenant, check for shared access
	if kb.TenantID != tenantID {
		// Get user ID from context
		userIDVal := ctx.Value(types.UserIDContextKey)
		if userIDVal == nil {
			return nil, werrors.NewForbiddenError("无权访问该知识库")
		}
		userID := userIDVal.(string)

		// Check if user has at least viewer permission through organization sharing
		hasPermission, err := s.kbShareService.HasKBPermission(ctx, kbID, userID, types.OrgRoleViewer)
		if err != nil || !hasPermission {
			return nil, werrors.NewForbiddenError("无权访问该知识库")
		}

		// Use the source tenant ID for data access
		sourceTenantID, err := s.kbShareService.GetKBSourceTenant(ctx, kbID)
		if err != nil {
			return nil, werrors.NewForbiddenError("无权访问该知识库")
		}
		effectiveTenantID = sourceTenantID
	}

	faqKnowledge, err := s.findFAQKnowledge(ctx, effectiveTenantID, kb.ID)
	if err != nil {
		return nil, err
	}
	if faqKnowledge == nil {
		return types.NewPageResult(0, page, []*types.FAQEntry{}), nil
	}

	// Convert tagSeqID to tagID (UUID)
	var tagID string
	if tagSeqID > 0 {
		tag, err := s.tagRepo.GetBySeqID(ctx, effectiveTenantID, tagSeqID)
		if err != nil {
			return nil, werrors.NewNotFoundError("标签不存在")
		}
		tagID = tag.ID
	}

	chunkType := []types.ChunkType{types.ChunkTypeFAQ}
	chunks, total, err := s.chunkRepo.ListPagedChunksByKnowledgeID(
		ctx, effectiveTenantID, faqKnowledge.ID, page, chunkType, tagID, keyword, searchField, sortOrder, types.KnowledgeTypeFAQ,
	)
	if err != nil {
		return nil, err
	}

	// Build tag ID to name and seq_id mapping for all unique tag IDs (batch query)
	tagNameMap := make(map[string]string)
	tagSeqIDMap := make(map[string]int64)
	tagIDs := make([]string, 0)
	tagIDSet := make(map[string]struct{})
	for _, chunk := range chunks {
		if chunk.TagID != "" {
			if _, exists := tagIDSet[chunk.TagID]; !exists {
				tagIDSet[chunk.TagID] = struct{}{}
				tagIDs = append(tagIDs, chunk.TagID)
			}
		}
	}
	if len(tagIDs) > 0 {
		tags, err := s.tagRepo.GetByIDs(ctx, effectiveTenantID, tagIDs)
		if err == nil {
			for _, tag := range tags {
				tagNameMap[tag.ID] = tag.Name
				tagSeqIDMap[tag.ID] = tag.SeqID
			}
		}
	}

	kb.EnsureDefaults()
	entries := make([]*types.FAQEntry, 0, len(chunks))
	for _, chunk := range chunks {
		entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
		if err != nil {
			return nil, err
		}
		// Set tag name from mapping
		if chunk.TagID != "" {
			entry.TagName = tagNameMap[chunk.TagID]
		}
		entries = append(entries, entry)
	}
	return types.NewPageResult(total, page, entries), nil
}

// CreateFAQEntry creates a single FAQ entry synchronously.
func (s *knowledgeService) CreateFAQEntry(ctx context.Context,
	kbID string, payload *types.FAQEntryPayload,
) (*types.FAQEntry, error) {
	if payload == nil {
		return nil, werrors.NewBadRequestError("请求体不能为空")
	}

	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 验证并清理输入
	meta, err := sanitizeFAQEntryPayload(payload)
	if err != nil {
		return nil, err
	}

	// 解析 TagID
	tagID, err := s.resolveTagID(ctx, kbID, payload)
	if err != nil {
		return nil, err
	}

	// 检查标准问和相似问是否与其他条目重复
	if err := s.checkFAQQuestionDuplicate(ctx, tenantID, kb.ID, "", meta); err != nil {
		return nil, err
	}

	// 确保FAQ Knowledge存在
	faqKnowledge, err := s.ensureFAQKnowledge(ctx, tenantID, kb)
	if err != nil {
		return nil, fmt.Errorf("failed to ensure FAQ knowledge: %w", err)
	}

	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}

	// 获取embedding模型
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, fmt.Errorf("failed to get embedding model: %w", err)
	}

	// 创建chunk
	isEnabled := true
	if payload.IsEnabled != nil {
		isEnabled = *payload.IsEnabled
	}
	// 默认可推荐
	flags := types.ChunkFlagRecommended
	if payload.IsRecommended != nil && !*payload.IsRecommended {
		flags = 0
	}

	chunk := &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        tenantID,
		KnowledgeID:     faqKnowledge.ID,
		KnowledgeBaseID: kb.ID,
		Content:         buildFAQChunkContent(meta, indexMode),
		IsEnabled:       isEnabled,
		Flags:           flags,
		ChunkType:       types.ChunkTypeFAQ,
		TagID:           tagID, // 使用解析后的 TagID
		Status:          int(types.ChunkStatusStored),
	}
	// 如果指定了 ID（用于数据迁移），设置 SeqID
	if payload.ID != nil && *payload.ID > 0 {
		chunk.SeqID = *payload.ID
	}

	if err := chunk.SetFAQMetadata(meta); err != nil {
		return nil, fmt.Errorf("failed to set FAQ metadata: %w", err)
	}

	// 保存chunk
	if err := s.chunkService.CreateChunks(ctx, []*types.Chunk{chunk}); err != nil {
		return nil, fmt.Errorf("failed to create chunk: %w", err)
	}

	// 索引chunk
	if err := s.indexFAQChunks(ctx, kb, faqKnowledge, []*types.Chunk{chunk}, embeddingModel, true, false); err != nil {
		// 如果索引失败，删除已创建的chunk
		_ = s.chunkService.DeleteChunk(ctx, chunk.ID)
		return nil, fmt.Errorf("failed to index chunk: %w", err)
	}

	// 更新chunk状态为已索引
	chunk.Status = int(types.ChunkStatusIndexed)
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return nil, fmt.Errorf("failed to update chunk status: %w", err)
	}

	// Build tag seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	// 转换为FAQEntry返回
	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	// 查询TagName
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// GetFAQEntry retrieves a single FAQ entry by seq_id.
func (s *knowledgeService) GetFAQEntry(ctx context.Context,
	kbID string, entrySeqID int64,
) (*types.FAQEntry, error) {
	if entrySeqID <= 0 {
		return nil, werrors.NewBadRequestError("条目ID不能为空")
	}

	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 获取chunk by seq_id
	chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, entrySeqID)
	if err != nil {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}

	// 验证chunk属于当前知识库
	if chunk.KnowledgeBaseID != kb.ID || chunk.TenantID != tenantID {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}

	// 验证是FAQ类型
	if chunk.ChunkType != types.ChunkTypeFAQ {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}

	// Build tag seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	// 转换为FAQEntry返回
	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	// 查询TagName
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// UpdateFAQEntry updates a single FAQ entry.
func (s *knowledgeService) UpdateFAQEntry(ctx context.Context,
	kbID string, entrySeqID int64, payload *types.FAQEntryPayload,
) (*types.FAQEntry, error) {
	if payload == nil {
		return nil, werrors.NewBadRequestError("请求体不能为空")
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, entrySeqID)
	if err != nil {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}
	if chunk.KnowledgeBaseID != kb.ID {
		return nil, werrors.NewForbiddenError("无权操作该 FAQ 条目")
	}
	if chunk.ChunkType != types.ChunkTypeFAQ {
		return nil, werrors.NewBadRequestError("仅支持更新 FAQ 条目")
	}
	meta, err := sanitizeFAQEntryPayload(payload)
	if err != nil {
		return nil, err
	}

	// 检查标准问和相似问是否与其他条目重复
	if err := s.checkFAQQuestionDuplicate(ctx, tenantID, kb.ID, chunk.ID, meta); err != nil {
		return nil, err
	}

	// 获取旧的相似问列表，用于增量更新
	var oldSimilarQuestions []string
	var oldStandardQuestion string
	var oldAnswers []string
	questionIndexMode := types.FAQQuestionIndexModeCombined
	if kb.FAQConfig != nil && kb.FAQConfig.QuestionIndexMode != "" {
		questionIndexMode = kb.FAQConfig.QuestionIndexMode
	}
	if existing, err := chunk.FAQMetadata(); err == nil && existing != nil {
		meta.Version = existing.Version + 1
		// 保存旧的内容用于增量比较
		if questionIndexMode == types.FAQQuestionIndexModeSeparate {
			oldSimilarQuestions = existing.SimilarQuestions
			oldStandardQuestion = existing.StandardQuestion
			oldAnswers = existing.Answers
		}
	}
	if err := chunk.SetFAQMetadata(meta); err != nil {
		return nil, err
	}
	// 获取索引模式
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}
	chunk.Content = buildFAQChunkContent(meta, indexMode)

	// Convert tag seq_id to UUID
	if payload.TagID > 0 {
		tag, tagErr := s.tagRepo.GetBySeqID(ctx, tenantID, payload.TagID)
		if tagErr != nil {
			return nil, werrors.NewNotFoundError("标签不存在")
		}
		chunk.TagID = tag.ID
	} else {
		chunk.TagID = ""
	}

	if payload.IsEnabled != nil {
		chunk.IsEnabled = *payload.IsEnabled
	}
	// 处理推荐状态
	if payload.IsRecommended != nil {
		if *payload.IsRecommended {
			chunk.Flags = chunk.Flags.SetFlag(types.ChunkFlagRecommended)
		} else {
			chunk.Flags = chunk.Flags.ClearFlag(types.ChunkFlagRecommended)
		}
	}
	chunk.UpdatedAt = time.Now()
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}

	// Note: We don't need to call BatchUpdateChunkEnabledStatus here because
	// indexFAQChunks will delete old vectors and re-insert with the latest chunk data
	// (including the updated is_enabled status). Calling both would cause version conflicts.

	faqKnowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, err
	}

	// 增量索引优化：只对变化的内容进行索引操作
	if questionIndexMode == types.FAQQuestionIndexModeSeparate && len(oldSimilarQuestions) > 0 {
		// 分别索引模式下的增量更新
		if err := s.incrementalIndexFAQEntry(ctx, kb, faqKnowledge, chunk, embeddingModel,
			oldStandardQuestion, oldSimilarQuestions, oldAnswers, meta); err != nil {
			return nil, err
		}
	} else {
		// Combined 模式或首次创建，使用全量索引
		// 增量删除：只删除被移除的相似问索引
		oldSimilarQuestionCount := len(oldSimilarQuestions)
		newSimilarQuestionCount := len(meta.SimilarQuestions)
		if questionIndexMode == types.FAQQuestionIndexModeSeparate && oldSimilarQuestionCount > newSimilarQuestionCount {
			tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
			retrieveEngine, engineErr := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
			if engineErr == nil {
				sourceIDsToDelete := make([]string, 0, oldSimilarQuestionCount-newSimilarQuestionCount)
				for i := newSimilarQuestionCount; i < oldSimilarQuestionCount; i++ {
					sourceIDsToDelete = append(sourceIDsToDelete, fmt.Sprintf("%s-%d", chunk.ID, i))
				}
				if len(sourceIDsToDelete) > 0 {
					logger.Debugf(ctx, "UpdateFAQEntry: incremental delete %d obsolete source IDs", len(sourceIDsToDelete))
					if delErr := retrieveEngine.DeleteBySourceIDList(ctx, sourceIDsToDelete, embeddingModel.GetDimensions(), types.KnowledgeTypeFAQ); delErr != nil {
						logger.Warnf(ctx, "UpdateFAQEntry: failed to delete obsolete source IDs: %v", delErr)
					}
				}
			}
		}

		// 使用 needDelete=false，因为 EFPutDocument 会自动覆盖相同 SourceID 的文档
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, []*types.Chunk{chunk}, embeddingModel, false, false); err != nil {
			return nil, err
		}
	}

	// Build tag seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	// 转换为FAQEntry返回
	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	// 查询TagName
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// AddSimilarQuestions adds similar questions to a FAQ entry.
// This will append the new questions to the existing similar questions list.
func (s *knowledgeService) AddSimilarQuestions(ctx context.Context,
	kbID string, entrySeqID int64, questions []string,
) (*types.FAQEntry, error) {
	if len(questions) == 0 {
		return nil, werrors.NewBadRequestError("相似问列表不能为空")
	}

	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Get existing FAQ entry
	chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, entrySeqID)
	if err != nil {
		return nil, werrors.NewNotFoundError("FAQ条目不存在")
	}
	if chunk.KnowledgeBaseID != kb.ID {
		return nil, werrors.NewForbiddenError("无权操作该 FAQ 条目")
	}
	if chunk.ChunkType != types.ChunkTypeFAQ {
		return nil, werrors.NewBadRequestError("仅支持更新 FAQ 条目")
	}

	// Get existing metadata
	meta, err := chunk.FAQMetadata()
	if err != nil || meta == nil {
		return nil, werrors.NewBadRequestError("获取 FAQ 元数据失败")
	}

	// Deduplicate and sanitize new questions
	existingSet := make(map[string]struct{})
	for _, q := range meta.SimilarQuestions {
		existingSet[q] = struct{}{}
	}
	// Also add standard question to prevent duplicates
	existingSet[meta.StandardQuestion] = struct{}{}

	newQuestions := make([]string, 0, len(questions))
	for _, q := range questions {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if _, exists := existingSet[q]; exists {
			continue
		}
		existingSet[q] = struct{}{}
		newQuestions = append(newQuestions, q)
	}

	if len(newQuestions) == 0 {
		// No new questions to add, return current entry
		tagSeqIDMap := make(map[string]int64)
		if chunk.TagID != "" {
			tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
			if tagErr == nil && tag != nil {
				tagSeqIDMap[tag.ID] = tag.SeqID
			}
		}
		return s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	}

	// Check for duplicates with other entries
	tempMeta := &types.FAQChunkMetadata{
		StandardQuestion: meta.StandardQuestion,
		SimilarQuestions: append(meta.SimilarQuestions, newQuestions...),
	}
	if err := s.checkFAQQuestionDuplicate(ctx, tenantID, kb.ID, chunk.ID, tempMeta); err != nil {
		return nil, err
	}

	// Update metadata
	oldSimilarQuestions := meta.SimilarQuestions
	meta.SimilarQuestions = append(meta.SimilarQuestions, newQuestions...)
	meta.Version++

	if err := chunk.SetFAQMetadata(meta); err != nil {
		return nil, err
	}

	// Update chunk content
	indexMode := types.FAQIndexModeQuestionOnly
	if kb.FAQConfig != nil && kb.FAQConfig.IndexMode != "" {
		indexMode = kb.FAQConfig.IndexMode
	}
	chunk.Content = buildFAQChunkContent(meta, indexMode)
	chunk.UpdatedAt = time.Now()

	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}

	// Index new similar questions
	faqKnowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return nil, err
	}

	questionIndexMode := types.FAQQuestionIndexModeCombined
	if kb.FAQConfig != nil && kb.FAQConfig.QuestionIndexMode != "" {
		questionIndexMode = kb.FAQConfig.QuestionIndexMode
	}

	if questionIndexMode == types.FAQQuestionIndexModeSeparate {
		// Only index the new similar questions
		if err := s.incrementalIndexFAQEntry(ctx, kb, faqKnowledge, chunk, embeddingModel,
			meta.StandardQuestion, oldSimilarQuestions, meta.Answers, meta); err != nil {
			return nil, err
		}
	} else {
		// Combined mode, re-index the whole entry
		if err := s.indexFAQChunks(ctx, kb, faqKnowledge, []*types.Chunk{chunk}, embeddingModel, false, false); err != nil {
			return nil, err
		}
	}

	// Build response
	tagSeqIDMap := make(map[string]int64)
	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			tagSeqIDMap[tag.ID] = tag.SeqID
		}
	}

	entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
	if err != nil {
		return nil, err
	}

	if chunk.TagID != "" {
		tag, tagErr := s.tagRepo.GetByID(ctx, tenantID, chunk.TagID)
		if tagErr == nil && tag != nil {
			entry.TagName = tag.Name
		}
	}

	return entry, nil
}

// UpdateFAQEntryStatus updates enable status for a FAQ entry.
func (s *knowledgeService) UpdateFAQEntryStatus(ctx context.Context,
	kbID string, entryID string, isEnabled bool,
) error {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, entryID)
	if err != nil {
		return err
	}
	if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
		return werrors.NewBadRequestError("仅支持更新 FAQ 条目")
	}
	if chunk.IsEnabled == isEnabled {
		return nil
	}
	chunk.IsEnabled = isEnabled
	chunk.UpdatedAt = time.Now()
	if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
		return err
	}

	// Sync update to retriever engines
	chunkStatusMap := map[string]bool{chunk.ID: isEnabled}
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
	if err != nil {
		return err
	}
	if err := retrieveEngine.BatchUpdateChunkEnabledStatus(ctx, chunkStatusMap); err != nil {
		return err
	}

	return nil
}

// UpdateFAQEntryFieldsBatch updates multiple fields for FAQ entries in batch.
// This is the unified API for batch updating FAQ entry fields.
// Supports two modes:
// 1. By entry seq_id: use ByID field
// 2. By Tag seq_id: use ByTag field to apply the same update to all entries under a tag
func (s *knowledgeService) UpdateFAQEntryFieldsBatch(ctx context.Context,
	kbID string, req *types.FAQEntryFieldsBatchUpdate,
) error {
	if req == nil || (len(req.ByID) == 0 && len(req.ByTag) == 0) {
		return nil
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	enabledUpdates := make(map[string]bool)
	tagUpdates := make(map[string]string)

	// Convert exclude seq_ids to UUIDs
	excludeUUIDs := make([]string, 0, len(req.ExcludeIDs))
	if len(req.ExcludeIDs) > 0 {
		excludeChunks, err := s.chunkRepo.ListChunksBySeqID(ctx, tenantID, req.ExcludeIDs)
		if err == nil {
			for _, c := range excludeChunks {
				excludeUUIDs = append(excludeUUIDs, c.ID)
			}
		}
	}

	// Handle ByTag updates first (by tag seq_id)
	if len(req.ByTag) > 0 {
		for tagSeqID, update := range req.ByTag {
			// Convert tag seq_id to UUID
			tag, err := s.tagRepo.GetBySeqID(ctx, tenantID, tagSeqID)
			if err != nil {
				return werrors.NewNotFoundError(fmt.Sprintf("标签 %d 不存在", tagSeqID))
			}

			var setFlags, clearFlags types.ChunkFlags

			// Handle IsRecommended
			if update.IsRecommended != nil {
				if *update.IsRecommended {
					setFlags = types.ChunkFlagRecommended
				} else {
					clearFlags = types.ChunkFlagRecommended
				}
			}

			// Convert new tag seq_id to UUID if provided
			var newTagUUID *string
			if update.TagID != nil {
				if *update.TagID > 0 {
					newTag, err := s.tagRepo.GetBySeqID(ctx, tenantID, *update.TagID)
					if err != nil {
						return werrors.NewNotFoundError(fmt.Sprintf("标签 %d 不存在", *update.TagID))
					}
					newTagUUID = &newTag.ID
				} else {
					emptyStr := ""
					newTagUUID = &emptyStr
				}
			}

			// Update all chunks with this tag
			affectedIDs, err := s.chunkRepo.UpdateChunkFieldsByTagID(
				ctx, tenantID, kb.ID, tag.ID,
				update.IsEnabled, setFlags, clearFlags, newTagUUID, excludeUUIDs,
			)
			if err != nil {
				return err
			}

			// Collect affected IDs for retriever sync
			if len(affectedIDs) > 0 {
				if update.IsEnabled != nil {
					for _, id := range affectedIDs {
						enabledUpdates[id] = *update.IsEnabled
					}
				}
				if newTagUUID != nil {
					for _, id := range affectedIDs {
						tagUpdates[id] = *newTagUUID
					}
				}
			}
		}
	}

	// Handle ByID updates (by entry seq_id)
	if len(req.ByID) > 0 {
		entrySeqIDs := make([]int64, 0, len(req.ByID))
		for entrySeqID := range req.ByID {
			entrySeqIDs = append(entrySeqIDs, entrySeqID)
		}
		chunks, err := s.chunkRepo.ListChunksBySeqID(ctx, tenantID, entrySeqIDs)
		if err != nil {
			return err
		}

		// Build chunk seq_id to chunk map
		chunkBySeqID := make(map[int64]*types.Chunk)
		for _, chunk := range chunks {
			chunkBySeqID[chunk.SeqID] = chunk
		}

		setFlags := make(map[string]types.ChunkFlags)
		clearFlags := make(map[string]types.ChunkFlags)
		chunksToUpdate := make([]*types.Chunk, 0)

		for entrySeqID, update := range req.ByID {
			chunk, exists := chunkBySeqID[entrySeqID]
			if !exists {
				continue
			}
			if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
				continue
			}

			needUpdate := false

			// Handle IsEnabled
			if update.IsEnabled != nil && chunk.IsEnabled != *update.IsEnabled {
				chunk.IsEnabled = *update.IsEnabled
				enabledUpdates[chunk.ID] = *update.IsEnabled
				needUpdate = true
			}

			// Handle IsRecommended (via Flags)
			if update.IsRecommended != nil {
				currentRecommended := chunk.Flags.HasFlag(types.ChunkFlagRecommended)
				if currentRecommended != *update.IsRecommended {
					if *update.IsRecommended {
						setFlags[chunk.ID] = types.ChunkFlagRecommended
					} else {
						clearFlags[chunk.ID] = types.ChunkFlagRecommended
					}
				}
			}

			// Handle TagID (convert seq_id to UUID)
			if update.TagID != nil {
				var newTagID string
				if *update.TagID > 0 {
					newTag, err := s.tagRepo.GetBySeqID(ctx, tenantID, *update.TagID)
					if err != nil {
						return werrors.NewNotFoundError(fmt.Sprintf("标签 %d 不存在", *update.TagID))
					}
					newTagID = newTag.ID
				}
				if chunk.TagID != newTagID {
					chunk.TagID = newTagID
					tagUpdates[chunk.ID] = newTagID
					needUpdate = true
				}
			}

			if needUpdate {
				chunk.UpdatedAt = time.Now()
				chunksToUpdate = append(chunksToUpdate, chunk)
			}
		}

		// Batch update chunks (for IsEnabled and TagID)
		if len(chunksToUpdate) > 0 {
			if err := s.chunkRepo.UpdateChunks(ctx, chunksToUpdate); err != nil {
				return err
			}
		}

		// Batch update flags (for IsRecommended)
		if len(setFlags) > 0 || len(clearFlags) > 0 {
			if err := s.chunkRepo.UpdateChunkFlagsBatch(ctx, tenantID, kb.ID, setFlags, clearFlags); err != nil {
				return err
			}
		}
	}

	// Sync to retriever engines
	if len(enabledUpdates) > 0 || len(tagUpdates) > 0 {
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			return err
		}
		if len(enabledUpdates) > 0 {
			if err := retrieveEngine.BatchUpdateChunkEnabledStatus(ctx, enabledUpdates); err != nil {
				return err
			}
		}
		if len(tagUpdates) > 0 {
			if err := retrieveEngine.BatchUpdateChunkTagID(ctx, tagUpdates); err != nil {
				return err
			}
		}
	}

	return nil
}

// UpdateFAQEntryTag updates the tag assigned to an FAQ entry.
func (s *knowledgeService) UpdateFAQEntryTag(ctx context.Context, kbID string, entryID string, tagID *string) error {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, entryID)
	if err != nil {
		return err
	}
	if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
		return werrors.NewBadRequestError("仅支持更新 FAQ 条目标签")
	}

	var resolvedTagID string
	if tagID != nil && *tagID != "" {
		tag, err := s.tagRepo.GetByID(ctx, tenantID, *tagID)
		if err != nil {
			return err
		}
		if tag.KnowledgeBaseID != kb.ID {
			return werrors.NewBadRequestError("标签不属于当前知识库")
		}
		resolvedTagID = tag.ID
	}

	// Check if tag actually changed
	if chunk.TagID == resolvedTagID {
		return nil
	}

	chunk.TagID = resolvedTagID
	chunk.UpdatedAt = time.Now()
	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		return err
	}

	// Sync tag update to retriever engines
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
		s.retrieveEngine,
		tenantInfo.GetEffectiveEngines(),
	)
	if err != nil {
		return err
	}
	return retrieveEngine.BatchUpdateChunkTagID(ctx, map[string]string{chunk.ID: resolvedTagID})
}

// UpdateFAQEntryTagBatch updates tags for FAQ entries in batch.
// Key: entry seq_id, Value: tag seq_id (nil to remove tag)
func (s *knowledgeService) UpdateFAQEntryTagBatch(ctx context.Context, kbID string, updates map[int64]*int64) error {
	if len(updates) == 0 {
		return nil
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Get all chunks in batch by seq_id
	entrySeqIDs := make([]int64, 0, len(updates))
	for entrySeqID := range updates {
		entrySeqIDs = append(entrySeqIDs, entrySeqID)
	}
	chunks, err := s.chunkRepo.ListChunksBySeqID(ctx, tenantID, entrySeqIDs)
	if err != nil {
		return err
	}

	// Build chunk seq_id to chunk map
	chunkBySeqID := make(map[int64]*types.Chunk)
	for _, chunk := range chunks {
		chunkBySeqID[chunk.SeqID] = chunk
	}

	// Build tag seq_id set for validation
	tagSeqIDSet := make(map[int64]bool)
	for _, tagSeqID := range updates {
		if tagSeqID != nil && *tagSeqID > 0 {
			tagSeqIDSet[*tagSeqID] = true
		}
	}

	// Validate all tags in batch by seq_id
	tagMap := make(map[int64]*types.KnowledgeTag)
	if len(tagSeqIDSet) > 0 {
		tagSeqIDs := make([]int64, 0, len(tagSeqIDSet))
		for tagSeqID := range tagSeqIDSet {
			tagSeqIDs = append(tagSeqIDs, tagSeqID)
		}
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, tagSeqIDs)
		if err != nil {
			return err
		}
		for _, tag := range tags {
			if tag.KnowledgeBaseID != kb.ID {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %d 不属于当前知识库", tag.SeqID))
			}
			tagMap[tag.SeqID] = tag
		}
	}

	// Update chunks
	chunksToUpdate := make([]*types.Chunk, 0)
	for entrySeqID, tagSeqID := range updates {
		chunk, exists := chunkBySeqID[entrySeqID]
		if !exists {
			continue
		}
		if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
			continue
		}

		var resolvedTagID string
		if tagSeqID != nil && *tagSeqID > 0 {
			tag, ok := tagMap[*tagSeqID]
			if !ok {
				return werrors.NewBadRequestError(fmt.Sprintf("标签 %d 不存在", *tagSeqID))
			}
			resolvedTagID = tag.ID
		}

		chunk.TagID = resolvedTagID
		chunk.UpdatedAt = time.Now()
		chunksToUpdate = append(chunksToUpdate, chunk)
	}

	if len(chunksToUpdate) > 0 {
		if err := s.chunkRepo.UpdateChunks(ctx, chunksToUpdate); err != nil {
			return err
		}

		// Sync tag updates to retriever engines
		tagUpdates := make(map[string]string)
		for _, chunk := range chunksToUpdate {
			tagUpdates[chunk.ID] = chunk.TagID
		}
		tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(
			s.retrieveEngine,
			tenantInfo.GetEffectiveEngines(),
		)
		if err != nil {
			return err
		}
		if err := retrieveEngine.BatchUpdateChunkTagID(ctx, tagUpdates); err != nil {
			return err
		}
	}

	return nil
}

// SearchFAQEntries searches FAQ entries using hybrid search.
func (s *knowledgeService) SearchFAQEntries(ctx context.Context,
	kbID string, req *types.FAQSearchRequest,
) ([]*types.FAQEntry, error) {
	// Validate FAQ knowledge base
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}

	// Set default values
	if req.VectorThreshold <= 0 {
		req.VectorThreshold = 0.7
	}
	if req.MatchCount <= 0 {
		req.MatchCount = 10
	}
	if req.MatchCount > 50 {
		req.MatchCount = 50
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// Convert tag seq_ids to UUIDs
	var firstPriorityTagUUIDs, secondPriorityTagUUIDs []string
	firstPrioritySeqIDSet := make(map[int64]struct{})
	secondPrioritySeqIDSet := make(map[int64]struct{})

	if len(req.FirstPriorityTagIDs) > 0 {
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, req.FirstPriorityTagIDs)
		if err == nil {
			firstPriorityTagUUIDs = make([]string, 0, len(tags))
			for _, tag := range tags {
				firstPriorityTagUUIDs = append(firstPriorityTagUUIDs, tag.ID)
				firstPrioritySeqIDSet[tag.SeqID] = struct{}{}
			}
		}
	}
	if len(req.SecondPriorityTagIDs) > 0 {
		tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, req.SecondPriorityTagIDs)
		if err == nil {
			secondPriorityTagUUIDs = make([]string, 0, len(tags))
			for _, tag := range tags {
				secondPriorityTagUUIDs = append(secondPriorityTagUUIDs, tag.ID)
				secondPrioritySeqIDSet[tag.SeqID] = struct{}{}
			}
		}
	}

	// Build priority tag sets for sorting (using UUID)
	hasFirstPriority := len(firstPriorityTagUUIDs) > 0
	hasSecondPriority := len(secondPriorityTagUUIDs) > 0
	hasPriorityFilter := hasFirstPriority || hasSecondPriority

	firstPrioritySet := make(map[string]struct{}, len(firstPriorityTagUUIDs))
	for _, tagID := range firstPriorityTagUUIDs {
		firstPrioritySet[tagID] = struct{}{}
	}
	secondPrioritySet := make(map[string]struct{}, len(secondPriorityTagUUIDs))
	for _, tagID := range secondPriorityTagUUIDs {
		secondPrioritySet[tagID] = struct{}{}
	}

	// Perform separate searches for each priority level to ensure FirstPriority results
	// are not crowded out by higher-scoring SecondPriority results in TopK truncation
	var searchResults []*types.SearchResult

	if hasPriorityFilter {
		// Use goroutines to search both priority levels concurrently
		var (
			firstResults  []*types.SearchResult
			secondResults []*types.SearchResult
			firstErr      error
			secondErr     error
			wg            sync.WaitGroup
		)

		if hasFirstPriority {
			wg.Add(1)
			go func() {
				defer wg.Done()
				firstParams := types.SearchParams{
					QueryText:            secutils.SanitizeForLog(req.QueryText),
					VectorThreshold:      req.VectorThreshold,
					MatchCount:           req.MatchCount,
					DisableKeywordsMatch: true,
					TagIDs:               firstPriorityTagUUIDs,
					OnlyRecommended:      req.OnlyRecommended,
				}
				firstResults, firstErr = s.kbService.HybridSearch(ctx, kbID, firstParams)
			}()
		}

		if hasSecondPriority {
			wg.Add(1)
			go func() {
				defer wg.Done()
				secondParams := types.SearchParams{
					QueryText:            secutils.SanitizeForLog(req.QueryText),
					VectorThreshold:      req.VectorThreshold,
					MatchCount:           req.MatchCount,
					DisableKeywordsMatch: true,
					TagIDs:               secondPriorityTagUUIDs,
					OnlyRecommended:      req.OnlyRecommended,
				}
				secondResults, secondErr = s.kbService.HybridSearch(ctx, kbID, secondParams)
			}()
		}

		wg.Wait()

		// Check errors
		if firstErr != nil {
			return nil, firstErr
		}
		if secondErr != nil {
			return nil, secondErr
		}

		// Merge results: FirstPriority first, then SecondPriority (deduplicated)
		seenChunkIDs := make(map[string]struct{})
		for _, result := range firstResults {
			if _, exists := seenChunkIDs[result.ID]; !exists {
				seenChunkIDs[result.ID] = struct{}{}
				searchResults = append(searchResults, result)
			}
		}
		for _, result := range secondResults {
			if _, exists := seenChunkIDs[result.ID]; !exists {
				seenChunkIDs[result.ID] = struct{}{}
				searchResults = append(searchResults, result)
			}
		}
	} else {
		// No priority filter, search all
		searchParams := types.SearchParams{
			QueryText:            secutils.SanitizeForLog(req.QueryText),
			VectorThreshold:      req.VectorThreshold,
			MatchCount:           req.MatchCount,
			DisableKeywordsMatch: true,
		}
		var err error
		searchResults, err = s.kbService.HybridSearch(ctx, kbID, searchParams)
		if err != nil {
			return nil, err
		}
	}

	if len(searchResults) == 0 {
		return []*types.FAQEntry{}, nil
	}

	// Extract chunk IDs and build score/match type/matched content maps
	chunkIDs := make([]string, 0, len(searchResults))
	chunkScores := make(map[string]float64)
	chunkMatchTypes := make(map[string]types.MatchType)
	chunkMatchedContents := make(map[string]string)
	for _, result := range searchResults {
		// SearchResult.ID is the chunk ID
		chunkID := result.ID
		chunkIDs = append(chunkIDs, chunkID)
		chunkScores[chunkID] = result.Score
		chunkMatchTypes[chunkID] = result.MatchType
		chunkMatchedContents[chunkID] = result.MatchedContent
	}

	// Batch fetch chunks
	chunks, err := s.chunkRepo.ListChunksByID(ctx, tenantID, chunkIDs)
	if err != nil {
		return nil, err
	}

	// Build tag UUID to seq_id map for conversion
	tagSeqIDMap := make(map[string]int64)
	tagIDs := make([]string, 0)
	tagIDSet := make(map[string]struct{})
	for _, chunk := range chunks {
		if chunk.TagID != "" {
			if _, exists := tagIDSet[chunk.TagID]; !exists {
				tagIDSet[chunk.TagID] = struct{}{}
				tagIDs = append(tagIDs, chunk.TagID)
			}
		}
	}
	if len(tagIDs) > 0 {
		tags, err := s.tagRepo.GetByIDs(ctx, tenantID, tagIDs)
		if err == nil {
			for _, tag := range tags {
				tagSeqIDMap[tag.ID] = tag.SeqID
			}
		}
	}

	// Filter FAQ chunks and convert to FAQEntry
	kb.EnsureDefaults()
	entries := make([]*types.FAQEntry, 0, len(chunks))
	for _, chunk := range chunks {
		// Only process FAQ type chunks
		if chunk.ChunkType != types.ChunkTypeFAQ {
			continue
		}
		if !chunk.IsEnabled {
			continue
		}

		entry, err := s.chunkToFAQEntry(chunk, kb, tagSeqIDMap)
		if err != nil {
			logger.Warnf(ctx, "Failed to convert chunk to FAQ entry: %v", err)
			continue
		}

		// Preserve score and match type from search results
		// Note: Negative question filtering is now handled in HybridSearch
		if score, ok := chunkScores[chunk.ID]; ok {
			entry.Score = score
		}
		if matchType, ok := chunkMatchTypes[chunk.ID]; ok {
			entry.MatchType = matchType
		}

		// Set MatchedQuestion from search result's matched content
		if matchedContent, ok := chunkMatchedContents[chunk.ID]; ok && matchedContent != "" {
			entry.MatchedQuestion = matchedContent
		}

		entries = append(entries, entry)
	}

	// Sort entries with two-level priority tag support
	if hasPriorityFilter {
		// getPriorityLevel returns: 0 = first priority, 1 = second priority, 2 = no priority
		// Use chunk.TagID (UUID) for comparison
		getPriorityLevel := func(chunk *types.Chunk) int {
			if _, ok := firstPrioritySet[chunk.TagID]; ok {
				return 0
			}
			if _, ok := secondPrioritySet[chunk.TagID]; ok {
				return 1
			}
			return 2
		}

		// Build chunk map for priority lookup
		chunkMap := make(map[int64]*types.Chunk)
		for _, chunk := range chunks {
			chunkMap[chunk.SeqID] = chunk
		}

		slices.SortFunc(entries, func(a, b *types.FAQEntry) int {
			aChunk := chunkMap[a.ID]
			bChunk := chunkMap[b.ID]
			var aPriority, bPriority int
			if aChunk != nil {
				aPriority = getPriorityLevel(aChunk)
			} else {
				aPriority = 2
			}
			if bChunk != nil {
				bPriority = getPriorityLevel(bChunk)
			} else {
				bPriority = 2
			}

			// Compare by priority level first
			if aPriority != bPriority {
				return aPriority - bPriority // Lower level = higher priority
			}

			// Same priority level, sort by score descending
			if b.Score > a.Score {
				return 1
			} else if b.Score < a.Score {
				return -1
			}
			return 0
		})
	} else {
		// No priority tags, sort by score only
		slices.SortFunc(entries, func(a, b *types.FAQEntry) int {
			if b.Score > a.Score {
				return 1
			} else if b.Score < a.Score {
				return -1
			}
			return 0
		})
	}

	// Limit results to requested match count
	if len(entries) > req.MatchCount {
		entries = entries[:req.MatchCount]
	}

	// 批量查询TagName并补充到结果中
	if len(entries) > 0 {
		// 收集所有需要查询的TagID (seq_id)
		tagSeqIDs := make([]int64, 0)
		tagSeqIDSet := make(map[int64]struct{})
		for _, entry := range entries {
			if entry.TagID != 0 {
				if _, exists := tagSeqIDSet[entry.TagID]; !exists {
					tagSeqIDs = append(tagSeqIDs, entry.TagID)
					tagSeqIDSet[entry.TagID] = struct{}{}
				}
			}
		}

		// 批量查询标签
		if len(tagSeqIDs) > 0 {
			tags, err := s.tagRepo.GetBySeqIDs(ctx, tenantID, tagSeqIDs)
			if err != nil {
				logger.Warnf(ctx, "Failed to batch query tags: %v", err)
			} else {
				// 构建TagSeqID到TagName的映射
				tagNameMap := make(map[int64]string)
				for _, tag := range tags {
					tagNameMap[tag.SeqID] = tag.Name
				}

				// 补充TagName
				for _, entry := range entries {
					if entry.TagID != 0 {
						if tagName, exists := tagNameMap[entry.TagID]; exists {
							entry.TagName = tagName
						}
					}
				}
			}
		}
	}

	return entries, nil
}

// DeleteFAQEntries deletes FAQ entries in batch by seq_id.
func (s *knowledgeService) DeleteFAQEntries(ctx context.Context,
	kbID string, entrySeqIDs []int64,
) error {
	if len(entrySeqIDs) == 0 {
		return werrors.NewBadRequestError("请选择需要删除的 FAQ 条目")
	}
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	var faqKnowledge *types.Knowledge
	chunksToRemove := make([]*types.Chunk, 0, len(entrySeqIDs))
	for _, seqID := range entrySeqIDs {
		if seqID <= 0 {
			continue
		}
		chunk, err := s.chunkRepo.GetChunkBySeqID(ctx, tenantID, seqID)
		if err != nil {
			return werrors.NewNotFoundError("FAQ条目不存在")
		}
		if chunk.KnowledgeBaseID != kb.ID || chunk.ChunkType != types.ChunkTypeFAQ {
			return werrors.NewBadRequestError("包含无效的 FAQ 条目")
		}
		if err := s.chunkService.DeleteChunk(ctx, chunk.ID); err != nil {
			return err
		}
		if faqKnowledge == nil {
			faqKnowledge, err = s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
			if err != nil {
				return err
			}
		}
		chunksToRemove = append(chunksToRemove, chunk)
	}
	if len(chunksToRemove) > 0 && faqKnowledge != nil {
		if err := s.deleteFAQChunkVectors(ctx, kb, faqKnowledge, chunksToRemove); err != nil {
			return err
		}
	}
	return nil
}

// ExportFAQEntries exports all FAQ entries for a knowledge base as CSV data.
// The CSV format matches the import example format with 8 columns:
// 分类(必填), 问题(必填), 相似问题(选填-多个用##分隔), 反例问题(选填-多个用##分隔),
// 机器人回答(必填-多个用##分隔), 是否全部回复(选填-默认FALSE), 是否停用(选填-默认FALSE),
// 是否禁止被推荐(选填-默认False 可被推荐)
func (s *knowledgeService) ExportFAQEntries(ctx context.Context, kbID string) ([]byte, error) {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, err
	}

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	faqKnowledge, err := s.findFAQKnowledge(ctx, tenantID, kb.ID)
	if err != nil {
		return nil, err
	}
	if faqKnowledge == nil {
		// Return empty CSV with headers only
		return s.buildFAQCSV(nil, nil), nil
	}

	// Get all FAQ chunks
	chunks, err := s.chunkRepo.ListAllFAQChunksForExport(ctx, tenantID, faqKnowledge.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to list FAQ chunks: %w", err)
	}

	// Build tag map for tag_id -> tag_name conversion
	tagMap, err := s.buildTagMap(ctx, tenantID, kbID)
	if err != nil {
		return nil, fmt.Errorf("failed to build tag map: %w", err)
	}

	return s.buildFAQCSV(chunks, tagMap), nil
}

// buildTagMap builds a map from tag_id to tag_name for the given knowledge base.
func (s *knowledgeService) buildTagMap(ctx context.Context, tenantID uint64, kbID string) (map[string]string, error) {
	const pageSize = 1000
	tagMap := make(map[string]string)

	for pageNum := 1; ; pageNum++ {
		page := &types.Pagination{Page: pageNum, PageSize: pageSize}
		tags, _, err := s.tagRepo.ListByKB(ctx, tenantID, kbID, page, "")
		if err != nil {
			return nil, err
		}
		for _, tag := range tags {
			if tag != nil {
				tagMap[tag.ID] = tag.Name
			}
		}
		if len(tags) < pageSize {
			break
		}
	}
	return tagMap, nil
}

// buildFAQCSV builds CSV content from FAQ chunks.
func (s *knowledgeService) buildFAQCSV(chunks []*types.Chunk, tagMap map[string]string) []byte {
	var buf strings.Builder

	// Write CSV header (matching import example format)
	headers := []string{
		"分类(必填)",
		"问题(必填)",
		"相似问题(选填-多个用##分隔)",
		"反例问题(选填-多个用##分隔)",
		"机器人回答(必填-多个用##分隔)",
		"是否全部回复(选填-默认FALSE)",
		"是否停用(选填-默认FALSE)",
		"是否禁止被推荐(选填-默认False 可被推荐)",
	}
	buf.WriteString(strings.Join(headers, ","))
	buf.WriteString("\n")

	// Write data rows
	for _, chunk := range chunks {
		meta, err := chunk.FAQMetadata()
		if err != nil || meta == nil {
			continue
		}

		// Get tag name
		tagName := ""
		if chunk.TagID != "" && tagMap != nil {
			if name, ok := tagMap[chunk.TagID]; ok {
				tagName = name
			}
		}

		// Build row
		row := []string{
			escapeCSVField(tagName),
			escapeCSVField(meta.StandardQuestion),
			escapeCSVField(strings.Join(meta.SimilarQuestions, "##")),
			escapeCSVField(strings.Join(meta.NegativeQuestions, "##")),
			escapeCSVField(strings.Join(meta.Answers, "##")),
			boolToCSV(meta.AnswerStrategy == types.AnswerStrategyAll),
			boolToCSV(!chunk.IsEnabled),                                 // 是否停用：取反
			boolToCSV(!chunk.Flags.HasFlag(types.ChunkFlagRecommended)), // 是否禁止被推荐：取反
		}
		buf.WriteString(strings.Join(row, ","))
		buf.WriteString("\n")
	}

	return []byte(buf.String())
}

// escapeCSVField escapes a field for CSV format.
func escapeCSVField(field string) string {
	// If field contains comma, newline, or quote, wrap in quotes and escape internal quotes
	if strings.ContainsAny(field, ",\"\n\r") {
		return "\"" + strings.ReplaceAll(field, "\"", "\"\"") + "\""
	}
	return field
}

// boolToCSV converts a boolean to CSV TRUE/FALSE string.
func boolToCSV(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func (s *knowledgeService) validateFAQKnowledgeBase(ctx context.Context, kbID string) (*types.KnowledgeBase, error) {
	if kbID == "" {
		return nil, werrors.NewBadRequestError("知识库 ID 不能为空")
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, err
	}
	kb.EnsureDefaults()
	if kb.Type != types.KnowledgeBaseTypeFAQ {
		return nil, werrors.NewBadRequestError("仅 FAQ 知识库支持该操作")
	}
	return kb, nil
}

func (s *knowledgeService) findFAQKnowledge(
	ctx context.Context,
	tenantID uint64,
	kbID string,
) (*types.Knowledge, error) {
	knowledges, err := s.repo.ListKnowledgeByKnowledgeBaseID(ctx, tenantID, kbID)
	if err != nil {
		return nil, err
	}
	for _, knowledge := range knowledges {
		if knowledge.Type == types.KnowledgeTypeFAQ {
			return knowledge, nil
		}
	}
	return nil, nil
}

func (s *knowledgeService) ensureFAQKnowledge(
	ctx context.Context,
	tenantID uint64,
	kb *types.KnowledgeBase,
) (*types.Knowledge, error) {
	existing, err := s.findFAQKnowledge(ctx, tenantID, kb.ID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	knowledge := &types.Knowledge{
		TenantID:         tenantID,
		KnowledgeBaseID:  kb.ID,
		Type:             types.KnowledgeTypeFAQ,
		Channel:          types.ChannelWeb,
		Title:            fmt.Sprintf("%s - FAQ", kb.Name),
		Description:      "FAQ 条目容器",
		Source:           types.KnowledgeTypeFAQ,
		ParseStatus:      "completed",
		EnableStatus:     "enabled",
		EmbeddingModelID: kb.EmbeddingModelID,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}
	if err := s.repo.CreateKnowledge(ctx, knowledge); err != nil {
		return nil, err
	}
	return knowledge, nil
}

func (s *knowledgeService) chunkToFAQEntry(chunk *types.Chunk, kb *types.KnowledgeBase, tagSeqIDMap map[string]int64) (*types.FAQEntry, error) {
	meta, err := chunk.FAQMetadata()
	if err != nil {
		return nil, err
	}
	if meta == nil {
		meta = &types.FAQChunkMetadata{StandardQuestion: chunk.Content}
	}
	// 默认使用 all 策略
	answerStrategy := meta.AnswerStrategy
	if answerStrategy == "" {
		answerStrategy = types.AnswerStrategyAll
	}

	// Get tag seq_id from map
	var tagSeqID int64
	if chunk.TagID != "" && tagSeqIDMap != nil {
		tagSeqID = tagSeqIDMap[chunk.TagID]
	}

	entry := &types.FAQEntry{
		ID:                chunk.SeqID,
		ChunkID:           chunk.ID,
		KnowledgeID:       chunk.KnowledgeID,
		KnowledgeBaseID:   chunk.KnowledgeBaseID,
		TagID:             tagSeqID,
		IsEnabled:         chunk.IsEnabled,
		IsRecommended:     chunk.Flags.HasFlag(types.ChunkFlagRecommended),
		StandardQuestion:  meta.StandardQuestion,
		SimilarQuestions:  meta.SimilarQuestions,
		NegativeQuestions: meta.NegativeQuestions,
		Answers:           meta.Answers,
		AnswerStrategy:    answerStrategy,
		IndexMode:         kb.FAQConfig.IndexMode,
		UpdatedAt:         chunk.UpdatedAt,
		CreatedAt:         chunk.CreatedAt,
		ChunkType:         chunk.ChunkType,
	}
	return entry, nil
}

func buildFAQChunkContent(meta *types.FAQChunkMetadata, mode types.FAQIndexMode) string {
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Q: %s\n", meta.StandardQuestion))
	if len(meta.SimilarQuestions) > 0 {
		builder.WriteString("Similar Questions:\n")
		for _, q := range meta.SimilarQuestions {
			builder.WriteString(fmt.Sprintf("- %s\n", q))
		}
	}
	// 负例不应该包含在 Content 中，因为它们不应该被索引
	// 答案根据索引模式决定是否包含
	if mode == types.FAQIndexModeQuestionAnswer && len(meta.Answers) > 0 {
		builder.WriteString("Answers:\n")
		for _, ans := range meta.Answers {
			builder.WriteString(fmt.Sprintf("- %s\n", ans))
		}
	}
	return builder.String()
}

// checkFAQQuestionDuplicate 检查标准问和相似问是否与知识库中其他条目重复
// excludeChunkID 用于排除当前正在编辑的条目（更新时使用）
// 按照批量导入时的检查方式：先构建已存在问题集合，再统一检查
func (s *knowledgeService) checkFAQQuestionDuplicate(
	ctx context.Context,
	tenantID uint64,
	kbID string,
	excludeChunkID string,
	meta *types.FAQChunkMetadata,
) error {
	// 1. 首先检查当前条目自身的相似问是否与标准问重复
	for _, q := range meta.SimilarQuestions {
		if q == meta.StandardQuestion {
			return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」不能与标准问相同", q))
		}
	}

	// 2. 检查当前条目自身的相似问之间是否有重复
	seen := make(map[string]struct{})
	for _, q := range meta.SimilarQuestions {
		if _, exists := seen[q]; exists {
			return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」重复", q))
		}
		seen[q] = struct{}{}
	}

	// 3. 检查反例问题是否与标准问或相似问重复（反例不能和正例相同）
	positiveQuestions := make(map[string]struct{})
	positiveQuestions[meta.StandardQuestion] = struct{}{}
	for _, q := range meta.SimilarQuestions {
		positiveQuestions[q] = struct{}{}
	}
	negativeQuestionsSeen := make(map[string]struct{})
	for _, q := range meta.NegativeQuestions {
		if q == "" {
			continue
		}
		// 检查反例是否与标准问重复
		if q == meta.StandardQuestion {
			return werrors.NewBadRequestError(fmt.Sprintf("反例问题「%s」不能与标准问相同", q))
		}
		// 检查反例是否与相似问重复
		if _, exists := positiveQuestions[q]; exists {
			return werrors.NewBadRequestError(fmt.Sprintf("反例问题「%s」不能与相似问相同", q))
		}
		// 检查反例之间是否重复
		if _, exists := negativeQuestionsSeen[q]; exists {
			return werrors.NewBadRequestError(fmt.Sprintf("反例问题「%s」重复", q))
		}
		negativeQuestionsSeen[q] = struct{}{}
	}

	// 4. 将标准问和所有相似问合并，用一条 DB 查询检查是否与其他条目冲突（替代全量扫描）
	allQuestions := make([]string, 0, 1+len(meta.SimilarQuestions))
	allQuestions = append(allQuestions, meta.StandardQuestion)
	allQuestions = append(allQuestions, meta.SimilarQuestions...)

	dupChunk, err := s.chunkRepo.FindFAQChunkWithDuplicateQuestion(ctx, tenantID, kbID, excludeChunkID, allQuestions)
	if err != nil {
		return fmt.Errorf("failed to check FAQ question duplicate: %w", err)
	}
	if dupChunk == nil {
		return nil
	}

	existingMeta, err := dupChunk.FAQMetadata()
	if err != nil || existingMeta == nil {
		return werrors.NewBadRequestError("标准问或相似问与已有条目重复")
	}

	// 5–7. 与原先全量扫描一致的报错语义：先检查标准问，再逐条检查相似问
	existingSimilarSet := make(map[string]struct{}, len(existingMeta.SimilarQuestions))
	for _, q := range existingMeta.SimilarQuestions {
		if q != "" {
			existingSimilarSet[q] = struct{}{}
		}
	}

	if meta.StandardQuestion != "" {
		if meta.StandardQuestion == existingMeta.StandardQuestion {
			return werrors.NewBadRequestError(fmt.Sprintf("标准问「%s」已存在", meta.StandardQuestion))
		}
		if _, ok := existingSimilarSet[meta.StandardQuestion]; ok {
			return werrors.NewBadRequestError(fmt.Sprintf("标准问「%s」已存在", meta.StandardQuestion))
		}
	}

	for _, q := range meta.SimilarQuestions {
		if q == "" {
			continue
		}
		if q == existingMeta.StandardQuestion {
			return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」已存在", q))
		}
		if _, ok := existingSimilarSet[q]; ok {
			return werrors.NewBadRequestError(fmt.Sprintf("相似问「%s」已存在", q))
		}
	}

	return werrors.NewBadRequestError("标准问或相似问与已有条目重复")
}

// resolveTagID resolves tag ID (UUID) from payload, prioritizing tag_id (seq_id) over tag_name
// If no tag is specified, creates or finds the "未分类" tag
// Returns the internal UUID of the tag
func (s *knowledgeService) resolveTagID(ctx context.Context, kbID string, payload *types.FAQEntryPayload) (string, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

	// 如果提供了 tag_id (seq_id)，优先使用 tag_id
	if payload.TagID != 0 {
		tag, err := s.tagRepo.GetBySeqID(ctx, tenantID, payload.TagID)
		if err != nil {
			return "", fmt.Errorf("failed to find tag by seq_id %d: %w", payload.TagID, err)
		}
		return tag.ID, nil
	}

	// 如果提供了 tag_name，查找或创建标签
	if payload.TagName != "" {
		tag, err := s.tagService.FindOrCreateTagByName(ctx, kbID, payload.TagName)
		if err != nil {
			return "", fmt.Errorf("failed to resolve tag by name '%s': %w", payload.TagName, err)
		}
		return tag.ID, nil
	}

	// 都没有提供，使用"未分类"标签
	tag, err := s.tagService.FindOrCreateTagByName(ctx, kbID, types.UntaggedTagName)
	if err != nil {
		return "", fmt.Errorf("failed to get or create default untagged tag: %w", err)
	}
	return tag.ID, nil
}

func sanitizeFAQEntryPayload(payload *types.FAQEntryPayload) (*types.FAQChunkMetadata, error) {
	// 处理 AnswerStrategy，默认为 all
	answerStrategy := types.AnswerStrategyAll
	if payload.AnswerStrategy != nil && *payload.AnswerStrategy != "" {
		switch *payload.AnswerStrategy {
		case types.AnswerStrategyAll, types.AnswerStrategyRandom:
			answerStrategy = *payload.AnswerStrategy
		default:
			return nil, werrors.NewBadRequestError("answer_strategy 必须是 'all' 或 'random'")
		}
	}
	meta := &types.FAQChunkMetadata{
		StandardQuestion:  strings.TrimSpace(payload.StandardQuestion),
		SimilarQuestions:  payload.SimilarQuestions,
		NegativeQuestions: payload.NegativeQuestions,
		Answers:           payload.Answers,
		AnswerStrategy:    answerStrategy,
		Version:           1,
		Source:            "faq",
	}
	meta.Normalize()
	if meta.StandardQuestion == "" {
		return nil, werrors.NewBadRequestError("标准问不能为空")
	}
	if len(meta.Answers) == 0 {
		return nil, werrors.NewBadRequestError("至少提供一个答案")
	}
	return meta, nil
}

func buildFAQIndexContent(meta *types.FAQChunkMetadata, mode types.FAQIndexMode) string {
	var builder strings.Builder
	builder.WriteString(meta.StandardQuestion)
	for _, q := range meta.SimilarQuestions {
		builder.WriteString("\n")
		builder.WriteString(q)
	}
	if mode == types.FAQIndexModeQuestionAnswer {
		for _, ans := range meta.Answers {
			builder.WriteString("\n")
			builder.WriteString(ans)
		}
	}
	return builder.String()
}

// buildFAQIndexInfoList 构建FAQ索引信息列表，支持分别索引模式
func (s *knowledgeService) buildFAQIndexInfoList(
	ctx context.Context,
	kb *types.KnowledgeBase,
	chunk *types.Chunk,
) ([]*types.IndexInfo, error) {
	indexMode := types.FAQIndexModeQuestionAnswer
	questionIndexMode := types.FAQQuestionIndexModeCombined
	if kb.FAQConfig != nil {
		if kb.FAQConfig.IndexMode != "" {
			indexMode = kb.FAQConfig.IndexMode
		}
		if kb.FAQConfig.QuestionIndexMode != "" {
			questionIndexMode = kb.FAQConfig.QuestionIndexMode
		}
	}

	meta, err := chunk.FAQMetadata()
	if err != nil {
		return nil, err
	}
	if meta == nil {
		meta = &types.FAQChunkMetadata{StandardQuestion: chunk.Content}
	}

	// 如果是一起索引模式，使用原有逻辑
	if questionIndexMode == types.FAQQuestionIndexModeCombined {
		content := buildFAQIndexContent(meta, indexMode)
		return []*types.IndexInfo{
			{
				Content:         content,
				SourceID:        chunk.ID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     chunk.KnowledgeID,
				KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeType:   types.KnowledgeTypeFAQ,
				TagID:           chunk.TagID,
				IsEnabled:       chunk.IsEnabled,
				IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
			},
		}, nil
	}

	// 分别索引模式：为每个问题创建独立的索引项
	indexInfoList := make([]*types.IndexInfo, 0)

	// 标准问索引项
	standardContent := meta.StandardQuestion
	if indexMode == types.FAQIndexModeQuestionAnswer && len(meta.Answers) > 0 {
		var builder strings.Builder
		builder.WriteString(meta.StandardQuestion)
		for _, ans := range meta.Answers {
			builder.WriteString("\n")
			builder.WriteString(ans)
		}
		standardContent = builder.String()
	}
	indexInfoList = append(indexInfoList, &types.IndexInfo{
		Content:         standardContent,
		SourceID:        chunk.ID,
		SourceType:      types.ChunkSourceType,
		ChunkID:         chunk.ID,
		KnowledgeID:     chunk.KnowledgeID,
		KnowledgeBaseID: chunk.KnowledgeBaseID,
		KnowledgeType:   types.KnowledgeTypeFAQ,
		TagID:           chunk.TagID,
		IsEnabled:       chunk.IsEnabled,
		IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
	})

	// 每个相似问创建一个索引项
	for i, similarQ := range meta.SimilarQuestions {
		similarContent := similarQ
		if indexMode == types.FAQIndexModeQuestionAnswer && len(meta.Answers) > 0 {
			var builder strings.Builder
			builder.WriteString(similarQ)
			for _, ans := range meta.Answers {
				builder.WriteString("\n")
				builder.WriteString(ans)
			}
			similarContent = builder.String()
		}
		sourceID := fmt.Sprintf("%s-%d", chunk.ID, i)
		indexInfoList = append(indexInfoList, &types.IndexInfo{
			Content:         similarContent,
			SourceID:        sourceID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeType:   types.KnowledgeTypeFAQ,
			TagID:           chunk.TagID,
			IsEnabled:       chunk.IsEnabled,
			IsRecommended:   chunk.Flags.HasFlag(types.ChunkFlagRecommended),
		})
	}

	return indexInfoList, nil
}
