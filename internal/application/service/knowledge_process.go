package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func (s *knowledgeService) cloneKnowledge(
	ctx context.Context,
	src *types.Knowledge,
	targetKB *types.KnowledgeBase,
) (err error) {
	if src.ParseStatus != "completed" {
		logger.GetLogger(ctx).WithField("knowledge_id", src.ID).Errorf("MoveKnowledge parse status is not completed")
		return nil
	}
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	dst := &types.Knowledge{
		ID:               uuid.New().String(),
		TenantID:         targetKB.TenantID,
		KnowledgeBaseID:  targetKB.ID,
		Type:             src.Type,
		Channel:          src.Channel,
		Title:            src.Title,
		Description:      src.Description,
		Source:           src.Source,
		ParseStatus:      "processing",
		EnableStatus:     "disabled",
		EmbeddingModelID: targetKB.EmbeddingModelID,
		FileName:         src.FileName,
		FolderPath:       src.FolderPath,
		FileType:         src.FileType,
		FileSize:         src.FileSize,
		FileHash:         src.FileHash,
		FilePath:         src.FilePath,
		StorageSize:      src.StorageSize,
		Metadata:         src.Metadata,
	}

	// Deep-copy the source document file into an object owned by the destination
	// knowledge. Without this the clone only shares the source's storage path, so
	// deleting the source knowledge would destroy the clone's file too. The new
	// object is tracked for cleanup if the clone fails downstream.
	var copiedFilePaths []string
	if src.FilePath != "" {
		srcKB, kbErr := s.kbService.GetKnowledgeBaseByID(ctx, src.KnowledgeBaseID)
		if kbErr != nil {
			return fmt.Errorf("clone knowledge: failed to load source knowledge base: %w", kbErr)
		}
		srcSvc := s.resolveFileServiceForPath(ctx, srcKB, src.FilePath)
		dstSvc := s.resolveFileService(ctx, targetKB)
		newPath, copyErr := copyOwnedObject(ctx, srcSvc, dstSvc, src.FilePath, targetKB.TenantID, dst.ID)
		if copyErr != nil {
			return fmt.Errorf("clone knowledge file copy failed: %w", copyErr)
		}
		dst.FilePath = newPath
		copiedFilePaths = append(copiedFilePaths, newPath)
	}

	defer func() {
		if err != nil {
			if len(copiedFilePaths) > 0 {
				cleanupCopiedObjects(ctx, s.resolveFileService(ctx, targetKB), copiedFilePaths)
			}
			dst.ParseStatus = "failed"
			dst.ErrorMessage = err.Error()
			_ = s.repo.UpdateKnowledge(ctx, dst)
			logger.GetLogger(ctx).WithField("error", err).Errorf("MoveKnowledge failed to move knowledge")
		} else {
			dst.ParseStatus = "completed"
			dst.EnableStatus = "enabled"
			_ = s.repo.UpdateKnowledge(ctx, dst)
			logger.GetLogger(ctx).WithField("knowledge_id", dst.ID).Infof("MoveKnowledge move knowledge successfully")
		}
	}()

	if err = s.repo.CreateKnowledge(ctx, dst); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("MoveKnowledge create knowledge failed")
		return
	}
	tenantInfo.StorageUsed += dst.StorageSize
	if err = s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, dst.StorageSize); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("MoveKnowledge update tenant storage used failed")
		return
	}
	if err = s.CloneChunk(ctx, src, dst); err != nil {
		logger.GetLogger(ctx).WithField("knowledge_id", dst.ID).
			WithField("error", err).Errorf("MoveKnowledge move chunks failed")
		return
	}
	return
}

// processDocumentFromPassage handles asynchronous processing of text passages
func (s *knowledgeService) processDocumentFromPassage(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, passage []string,
) {
	// Update status to processing
	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return
	}

	// Convert passages to chunks
	chunks := make([]types.ParsedChunk, 0, len(passage))
	start, end := 0, 0
	for i, p := range passage {
		if p == "" {
			continue
		}
		end += len([]rune(p))
		chunks = append(chunks, types.ParsedChunk{
			Content: p,
			Seq:     i,
			Start:   start,
			End:     end,
		})
		start = end
	}
	// Process and store chunks
	var opts ProcessChunksOptions
	if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
		opts.EnableQuestionGeneration = true
		opts.QuestionCount = kb.QuestionGenerationConfig.QuestionCount
		if opts.QuestionCount <= 0 {
			opts.QuestionCount = 3
		}
	}
	if err := s.processChunks(ctx, kb, knowledge, chunks, opts); err != nil {
		logger.Errorf(ctx, "Process passage chunks failed: %v", err)
	}
}

// ProcessChunksOptions contains options for processing chunks
type ProcessChunksOptions struct {
	EnableQuestionGeneration bool
	QuestionCount            int
	EnableMultimodel         bool
	StoredImages             []docparser.StoredImage
	// ParentChunks holds parent chunk data when parent-child chunking is enabled.
	// When set, the chunks passed to processChunks are child chunks, and each
	// child's ParentIndex references an entry in this slice.
	ParentChunks []types.ParsedParentChunk
	Metadata     map[string]string
}

// finalizeIndexedKnowledgeState makes a document retrievable as soon as chunks
// and indexes are persisted (enable_status=enabled), but it deliberately does
// NOT mark the row completed when enrichment is still expected. Whenever the
// document still has work to fan out — pending multimodal image tasks, or text
// chunks that feed summary/question/graph generation — parse_status stays
// "processing" so KnowledgePostProcess remains the single authority that drives
// processing → finalizing → completed. Marking the row completed here would make
// post-process hit its "non-processing status" guard and skip the summary
// fan-out, stranding summary_status on "pending" forever.
func finalizeIndexedKnowledgeState(
	knowledge *types.Knowledge,
	totalStorageSize int64,
	textChunkCount int,
	hasPendingMultimodal bool,
	now time.Time,
) {
	if hasPendingMultimodal || textChunkCount > 0 {
		knowledge.ParseStatus = types.ParseStatusProcessing
		knowledge.SummaryStatus = types.SummaryStatusNone
	} else {
		// No text chunks and no pending multimodal work: there is nothing for
		// post-process to enrich, so complete immediately. This is the only
		// route to 'completed' that bypasses FinalizeSubtask, so it has to
		// clear error_message itself — otherwise a successfully indexed row
		// keeps reporting a failure from an earlier attempt.
		knowledge.ParseStatus = types.ParseStatusCompleted
		knowledge.SummaryStatus = types.SummaryStatusNone
		knowledge.ErrorMessage = ""
	}

	knowledge.EnableStatus = "enabled"
	knowledge.StorageSize = totalStorageSize
	knowledge.ProcessedAt = &now
	knowledge.UpdatedAt = now
}

// markKnowledgeProcessing makes the top-level knowledge state describe the
// attempt that is starting now. Clearing error_message is part of that: the
// column is only meaningful for the attempt that produced it, so a row
// re-entering processing must not keep surfacing the previous attempt's
// failure while it is visibly running again.
func markKnowledgeProcessing(knowledge *types.Knowledge, now time.Time) {
	knowledge.ParseStatus = types.ParseStatusProcessing
	knowledge.ErrorMessage = ""
	knowledge.UpdatedAt = now
}

// buildSplitterConfig creates a SplitterConfig with fallbacks from a KnowledgeBase.
// Defaults mirror chunker.DefaultChunkSize / DefaultChunkOverlap so behavior is
// identical whether callers come through this path or invoke the chunker
// directly with a zero-value config.
func buildSplitterConfig(kb *types.KnowledgeBase) chunker.SplitterConfig {
	return buildSplitterConfigFromChunking(kb.ChunkingConfig)
}

func buildSplitterConfigFromChunking(cc types.ChunkingConfig) chunker.SplitterConfig {
	return chunker.NormalizeSplitterConfig(chunker.SplitterConfig{
		ChunkSize:    cc.ChunkSize,
		ChunkOverlap: cc.ChunkOverlap,
		Separators:   cc.Separators,
		Strategy:     cc.Strategy,
		TokenLimit:   cc.TokenLimit,
		Languages:    cc.Languages,
	})
}

// buildParentChildConfigs derives parent and child SplitterConfig from ChunkingConfig.
// The base config (already validated with defaults) is used for separators and
// the splitting strategy. Strategy must be propagated: an empty Strategy resolves
// to the legacy tier (see resolveChainWithProfile), which never runs the heading
// splitter, so parent-child chunks would silently lose heading alignment and
// ContextHeader breadcrumbs regardless of the configured strategy.
func buildParentChildConfigs(cc types.ChunkingConfig, base chunker.SplitterConfig) (parent, child chunker.SplitterConfig) {
	return chunker.DeriveParentChildConfigs(base, cc.ParentChunkSize, cc.ChildChunkSize)
}

// processChunks processes chunks and creates embeddings for knowledge content
func (s *knowledgeService) processChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []types.ParsedChunk,
	opts ...ProcessChunksOptions,
) error {
	// Get options
	var options ProcessChunksOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	// Check if knowledge is being deleted/cancelled before processing.
	// Both statuses short-circuit identically here — there's nothing to clean
	// up yet so the branch is purely "stop early".
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "Knowledge aborted (%s), skipping chunk processing: %s", status, knowledge.ID)
		return nil
	}

	// Get embedding model for vectorization — only needed when vector/keyword indexing is enabled
	var embeddingModel embedding.Embedder
	if kb.NeedsEmbeddingModel() {
		var err error
		embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			processingErr := fmt.Errorf("get embedding model %s: %w", kb.EmbeddingModelID, err)
			knowledge.ParseStatus = types.ParseStatusFailed
			knowledge.ErrorMessage = processingErr.Error()
			knowledge.UpdatedAt = time.Now()
			updateErr := s.repo.UpdateKnowledge(ctx, knowledge)
			s.tracker().FinalizeAttempt(
				ctx,
				knowledge.ID,
				attemptFromCtx(ctx),
				types.SpanStatusFailed,
				nil,
				werrors.ErrCodeEmbeddingProviderFail,
				processingErr.Error(),
			)
			logger.GetLogger(ctx).WithField("error", processingErr).Errorf("processChunks get embedding model failed")
			if updateErr != nil {
				return errors.Join(processingErr, fmt.Errorf("update failed knowledge state: %w", updateErr))
			}
			return processingErr
		}
	} else {
		logger.Infof(ctx, "Vector/keyword indexing disabled for KB %s, skipping embedding model", kb.ID)
	}

	// 幂等性处理：清理旧的chunks和索引数据，避免重复数据
	logger.Infof(ctx, "Cleaning up existing chunks and index data for knowledge: %s", knowledge.ID)

	// 删除旧的chunks
	if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
		logger.Warnf(ctx, "Failed to delete existing chunks (may not exist): %v", err)
		// 不返回错误，继续处理（可能没有旧数据）
	}

	// 删除旧的索引数据 — only when vector/keyword indexing is enabled
	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err == nil && embeddingModel != nil {
		if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), knowledge.Type); err != nil {
			logger.Warnf(ctx, "Failed to delete existing index data (may not exist): %v", err)
			// 不返回错误，继续处理（可能没有旧数据）
		} else {
			logger.Infof(ctx, "Successfully deleted existing index data for knowledge: %s", knowledge.ID)
		}
	}

	// 删除知识图谱数据（如果存在）
	namespace := types.NameSpace{KnowledgeBase: knowledge.KnowledgeBaseID, Knowledge: knowledge.ID}
	if err := s.graphEngine.DelGraph(ctx, []types.NameSpace{namespace}); err != nil {
		logger.Warnf(ctx, "Failed to delete existing graph data (may not exist): %v", err)
		// 不返回错误，继续处理
	}

	logger.Infof(ctx, "Cleanup completed, starting to process new chunks")

	// ========== DocReader 解析结果日志 ==========
	logger.Infof(ctx, "[DocReader] ========== 解析结果概览 ==========")
	logger.Infof(ctx, "[DocReader] 知识ID: %s, 知识库ID: %s", knowledge.ID, knowledge.KnowledgeBaseID)
	logger.Infof(ctx, "[DocReader] 总Chunk数量: %d", len(chunks))

	// 统计图片信息
	totalImages := 0
	chunksWithImages := 0
	for _, chunkData := range chunks {
		if len(chunkData.Images) > 0 {
			chunksWithImages++
			totalImages += len(chunkData.Images)
		}
	}
	logger.Infof(ctx, "[DocReader] 包含图片的Chunk数: %d, 总图片数: %d", chunksWithImages, totalImages)

	// 打印每个Chunk的详细信息
	for idx, chunkData := range chunks {
		contentPreview := chunkData.Content
		if len(contentPreview) > 200 {
			contentPreview = contentPreview[:200] + "..."
		}
		logger.Infof(ctx, "[DocReader] Chunk #%d (seq=%d): 内容长度=%d, 图片数=%d, 范围=[%d-%d]",
			idx, chunkData.Seq, len(chunkData.Content), len(chunkData.Images), chunkData.Start, chunkData.End)
		logger.Debugf(ctx, "[DocReader] Chunk #%d 内容预览: %s", idx, contentPreview)

		// 打印图片详细信息
		for imgIdx, img := range chunkData.Images {
			logger.Infof(ctx, "[DocReader]   图片 #%d: URL=%s", imgIdx, img.URL)
			logger.Infof(ctx, "[DocReader]   图片 #%d: OriginalURL=%s", imgIdx, img.OriginalURL)
			if img.Caption != "" {
				captionPreview := img.Caption
				if len(captionPreview) > 100 {
					captionPreview = captionPreview[:100] + "..."
				}
				logger.Infof(ctx, "[DocReader]   图片 #%d: Caption=%s", imgIdx, captionPreview)
			}
			if img.OCRText != "" {
				ocrPreview := img.OCRText
				if len(ocrPreview) > 100 {
					ocrPreview = ocrPreview[:100] + "..."
				}
				logger.Infof(ctx, "[DocReader]   图片 #%d: OCRText=%s", imgIdx, ocrPreview)
			}
			logger.Infof(ctx, "[DocReader]   图片 #%d: 位置=[%d-%d]", imgIdx, img.Start, img.End)
		}
	}
	logger.Infof(ctx, "[DocReader] ========== 解析结果概览结束 ==========")

	// Create chunk objects from proto chunks
	maxSeq := 0

	// 统计图片相关的子Chunk数量，用于扩展insertChunks的容量
	imageChunkCount := 0
	for _, chunkData := range chunks {
		if len(chunkData.Images) > 0 {
			// 为每个图片的OCR和Caption分别创建一个Chunk
			imageChunkCount += len(chunkData.Images) * 2
		}
		if int(chunkData.Seq) > maxSeq {
			maxSeq = int(chunkData.Seq)
		}
	}

	// === Parent-Child Chunking: create parent chunks first ===
	hasParentChild := len(options.ParentChunks) > 0
	var parentDBChunks []*types.Chunk // indexed by ParsedParentChunk position
	if hasParentChild {
		parentDBChunks = make([]*types.Chunk, len(options.ParentChunks))
		for i, pc := range options.ParentChunks {
			parentDBChunks[i] = &types.Chunk{
				ID:              uuid.New().String(),
				TenantID:        knowledge.TenantID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				Content:         pc.Content,
				ChunkIndex:      pc.Seq,
				IsEnabled:       true,
				CreatedAt:       time.Now(),
				UpdatedAt:       time.Now(),
				StartAt:         pc.Start,
				EndAt:           pc.End,
				ChunkType:       types.ChunkTypeParentText,
			}
		}
		// Set prev/next links for parent chunks
		for i := range parentDBChunks {
			if i > 0 {
				parentDBChunks[i-1].NextChunkID = parentDBChunks[i].ID
				parentDBChunks[i].PreChunkID = parentDBChunks[i-1].ID
			}
		}
		logger.Infof(ctx, "Created %d parent chunks for parent-child strategy", len(parentDBChunks))
	}

	// 重新分配容量，考虑图片相关的Chunk + parent chunks
	parentCount := len(options.ParentChunks)
	insertChunks := make([]*types.Chunk, 0, len(chunks)+imageChunkCount+parentCount)
	// Add parent chunks first (they go into DB but NOT into the vector index)
	if hasParentChild {
		insertChunks = append(insertChunks, parentDBChunks...)
	}

	for idx, chunkData := range chunks {
		if strings.TrimSpace(chunkData.Content) == "" {
			continue
		}

		// 创建主文本Chunk
		textChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         chunkData.Content,
			ContextHeader:   chunkData.ContextHeader,
			ChunkIndex:      int(chunkData.Seq),
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         int(chunkData.Start),
			EndAt:           int(chunkData.End),
			ChunkType:       types.ChunkTypeText,
		}

		// Wire up ParentChunkID for child chunks
		if hasParentChild && chunkData.ParentIndex >= 0 && chunkData.ParentIndex < len(parentDBChunks) {
			textChunk.ParentChunkID = parentDBChunks[chunkData.ParentIndex].ID
		}

		chunks[idx].ChunkID = textChunk.ID
		insertChunks = append(insertChunks, textChunk)
	}

	// Sort chunks by index for proper ordering
	sort.Slice(insertChunks, func(i, j int) bool {
		return insertChunks[i].ChunkIndex < insertChunks[j].ChunkIndex
	})

	// Collect retrievable text chunks only. ParentChunkID only controls parent expansion after retrieval.
	// When ParentChunkID is empty, retrieval keeps the standalone child content without loading a parent.
	textChunks := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range insertChunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}

	// 设置文本Chunk之间的前后关系 (skip if parent-child, children don't need prev/next links)
	if !hasParentChild {
		for i, chunk := range textChunks {
			if i > 0 {
				textChunks[i-1].NextChunkID = chunk.ID
			}
			if i < len(textChunks)-1 {
				textChunks[i+1].PreChunkID = chunk.ID
			}
		}
	}

	// Check if knowledge is being deleted/cancelled before writing chunks.
	// Nothing has been persisted yet, so both branches just bail.
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "Knowledge aborted (%s), skipping chunk write: %s", status, knowledge.ID)
		return nil
	}

	// Save chunks to database — ALWAYS, regardless of indexing strategy.
	// Chunks are needed for wiki generation, graph extraction, and summary generation
	// even when vector/keyword indexing is disabled.
	s.beginStage(ctx, knowledge.ID, types.StageChunking, types.JSONMap{
		"chunks_planned": len(insertChunks),
	})
	if err := s.chunkService.CreateChunks(ctx, insertChunks); err != nil {
		knowledge.ParseStatus = types.ParseStatusFailed
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageChunking,
			werrors.ErrCodeChunkingFailed, "create chunks failed", err)
		return fmt.Errorf("create chunks: %w", err)
	}
	totalChunkChars := 0
	for _, c := range insertChunks {
		totalChunkChars += len(c.Content)
	}
	s.endStage(ctx, knowledge.ID, types.StageChunking, types.JSONMap{
		"chunks_written":   len(insertChunks),
		"total_text_chars": totalChunkChars,
	})

	// Create index information and perform vector indexing — only when vector/keyword is enabled.
	// Chunks are ALWAYS saved to DB (above) because wiki and graph need them even without vector indexing.
	var totalStorageSize int64
	if kb.NeedsEmbeddingModel() && embeddingModel != nil {
		embedInput := types.JSONMap{
			"chunks_to_embed": len(textChunks),
			"model_id":        kb.EmbeddingModelID,
		}
		if dim := embeddingModel.GetDimensions(); dim > 0 {
			embedInput["dim"] = dim
		}
		s.beginStage(ctx, knowledge.ID, types.StageEmbedding, embedInput)
		// Create index information — only for child/flat chunks, NOT parent chunks.
		// Parent chunks are stored for context retrieval but do not need vector embeddings.
		// Prepend the document title to improve semantic alignment between
		// question-style queries and statement-style chunk content.
		indexInfoList := make([]*types.IndexInfo, 0, len(textChunks))
		for _, chunk := range textChunks {
			// chunk.EmbeddingContent prepends ContextHeader (heading breadcrumb)
			// when the chunker populated it during Tier-1 splitting; falls back
			// to plain Content otherwise. The document title sits outermost;
			// custom metadata remains document-scoped model context.
			indexContent := buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent())
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         indexContent,
				SourceID:        chunk.ID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				IsEnabled:       true,
			})
		}

		// Calculate storage size required for embeddings
		totalStorageSize = retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfoList)
		if tenantInfo.StorageQuota > 0 {
			// Re-fetch tenant storage information
			tenantInfo, err = s.tenantRepo.GetTenantByID(ctx, tenantInfo.ID)
			if err != nil {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
				return fmt.Errorf("reload tenant storage usage: %w", err)
			}
			// Check if there's enough storage quota available
			if tenantInfo.StorageUsed+totalStorageSize > tenantInfo.StorageQuota {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = "存储空间不足"
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
				return errors.New("tenant storage quota exceeded")
			}
		}

		// Check again before batch indexing (heavy operation).
		// deleting → row is going away anyway, drop the chunks we just wrote.
		// cancelled → user wants to keep what was already persisted, just stop.
		if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
			logger.Infof(ctx, "Knowledge aborted (%s) before indexing: %s", status, knowledge.ID)
			if status == types.ParseStatusDeleting {
				if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
					logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
				}
			}
			return nil
		}

		err = retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList)
		if err != nil {
			knowledge.ParseStatus = types.ParseStatusFailed
			knowledge.ErrorMessage = err.Error()
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)

			// delete failed chunks
			if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
				logger.Errorf(ctx, "Delete chunks failed: %v", err)
			}

			// delete index
			if err := retrieveEngine.DeleteByKnowledgeIDList(
				ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), kb.Type,
			); err != nil {
				logger.Errorf(ctx, "Delete index failed: %v", err)
			}
			// Map vector store / embedding rate-limit errors to a
			// stable code so the UI can offer "retry later" hints.
			code := werrors.ErrCodeVectorStoreWriteFailed
			if isLikelyRateLimitError(err) {
				code = werrors.ErrCodeEmbeddingRateLimit
			}
			s.failStage(ctx, knowledge.ID, types.StageEmbedding,
				code, "batch index failed", err)
			return fmt.Errorf("batch index: %w", err)
		}
		logger.GetLogger(ctx).Infof("processChunks batch index successfully, with %d index", len(indexInfoList))
		s.endStage(ctx, knowledge.ID, types.StageEmbedding, types.JSONMap{
			"vectors_written": len(indexInfoList),
			"storage_bytes":   totalStorageSize,
		})

		// Final check before marking as completed.
		// deleting → drop chunks+index we just wrote.
		// cancelled → keep persisted data; the row stays in cancelled status
		// and downstream stages skip via the entry guards.
		if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
			logger.Infof(ctx, "Knowledge aborted (%s) after indexing: %s", status, knowledge.ID)
			if status == types.ParseStatusDeleting {
				if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
					logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
				}
				if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), kb.Type); err != nil {
					logger.Warnf(ctx, "Failed to cleanup index after deletion detected: %v", err)
				}
			}
			return nil
		}
	} else {
		logger.Infof(ctx, "Vector/keyword indexing disabled for KB %s, skipping BatchIndex", kb.ID)
		s.skipStage(ctx, knowledge.ID, types.StageEmbedding, "skipped")
	}

	// Check if this document has extracted images that will be processed asynchronously
	isImage := IsImageType(knowledge.FileType)
	isVideo := IsVideoType(knowledge.FileType)
	pendingMultimodal := isImage && options.EnableMultimodel && len(options.StoredImages) > 0
	pendingPDFMultimodal := !isImage && !isVideo && options.EnableMultimodel && len(options.StoredImages) > 0

	now := time.Now()
	finalizeIndexedKnowledgeState(
		knowledge,
		totalStorageSize,
		len(textChunks),
		pendingMultimodal || pendingPDFMultimodal,
		now,
	)

	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update knowledge failed")
		return fmt.Errorf("update indexed knowledge state: %w", err)
	}

	// Enqueue multimodal tasks for images (async, non-blocking)
	if options.EnableMultimodel && len(options.StoredImages) > 0 {
		s.beginStage(ctx, knowledge.ID, types.StageMultimodal, types.JSONMap{
			"image_count":    len(options.StoredImages),
			"enable_ocr":     true,
			"enable_caption": true,
		})
		s.enqueueImageMultimodalTasks(ctx, knowledge, kb, options.StoredImages, chunks, options.Metadata)
	} else {
		s.skipStage(ctx, knowledge.ID, types.StageMultimodal, "skipped")
		// If there are no multimodal tasks, enqueue the post process task immediately
		lang := types.LanguageFromContextOrDefault(ctx)
		postProcessPayload := types.KnowledgePostProcessPayload{
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Language:        lang,
			Attempt:         attemptFromCtx(ctx),
		}
		langfuse.InjectTracing(ctx, &postProcessPayload)
		payloadBytes, err := json.Marshal(postProcessPayload)
		if err == nil {
			task := asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes,
				knowledgePostProcessTaskOptions()...)
			if _, err := s.task.Enqueue(task); err != nil {
				logger.Errorf(ctx, "Failed to enqueue knowledge post process task: %v", err)
			} else {
				logger.Infof(ctx, "Enqueued knowledge post process task for %s", knowledge.ID)
			}
		} else {
			logger.Errorf(ctx, "Failed to marshal knowledge post process payload: %v", err)
		}
	}

	// Update tenant's storage usage
	tenantInfo.StorageUsed += totalStorageSize
	if err := s.tenantRepo.AdjustStorageUsed(ctx, tenantInfo.ID, totalStorageSize); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update tenant storage used failed")
	}
	logger.GetLogger(ctx).Infof("processChunks successfully")
	return nil
}

// defaultMaxInputChars is the default maximum characters used as input for summary generation.
const defaultMaxInputChars = 1024 * 24

// imageDominatedTextThreshold is the rune count below which a document is
// considered "image-dominated" — i.e. the body text is so sparse that we
// should fall back to full image enrichment (caption + OCR) for the summary
// LLM call. Above this threshold the document has enough native text that
// caption-only enrichment is preferable (OCR text from incidental figures
// would otherwise add noise without contributing to the main topic).
const imageDominatedTextThreshold = 200

// errInsufficientSummaryContent signals that getSummary refused to call the
// LLM because the document had no usable text after image markup was stripped
// (typical for scanned PDFs where VLM OCR yielded nothing). Callers should
// mark the knowledge's summary as failed instead of falling back to the first
// chunk's raw content (which would just be a bare image reference).
var (
	errInsufficientSummaryContent = errors.New("insufficient text content for summary generation")
	errEmptySummaryOutput         = errors.New("summary model returned empty output")
)

const summaryFallbackMaxRunes = 500

// validateSummaryOutput rejects successful model responses that contain no
// user-visible text. Treating whitespace-only output as an error lets Asynq
// retry the summary task instead of persisting description="" as completed.
func validateSummaryOutput(response *types.ChatResponse) (string, error) {
	if response == nil {
		return "", errEmptySummaryOutput
	}
	content := strings.TrimSpace(response.Content)
	if content == "" {
		return "", errEmptySummaryOutput
	}
	return content, nil
}

// firstTextChunkSummaryFallback preserves the existing deterministic fallback:
// use the first already-ordered text chunk and cap it by runes so Chinese and
// emoji are never cut in the middle of a UTF-8 sequence.
func firstTextChunkSummaryFallback(textChunks []*types.Chunk) string {
	if len(textChunks) == 0 || textChunks[0] == nil {
		return ""
	}
	fallback := strings.TrimSpace(textChunks[0].Content)
	runes := []rune(fallback)
	if len(runes) > summaryFallbackMaxRunes {
		fallback = string(runes[:summaryFallbackMaxRunes])
	}
	return fallback
}

// applyRetryableSummaryFailureState keeps an existing description visible
// while another attempt is queued, then publishes the deterministic fallback
// and marks only the summary subtask failed after the retry budget is exhausted.
func applyRetryableSummaryFailureState(
	knowledge *types.Knowledge, textChunks []*types.Chunk, willRetry bool,
) string {
	knowledge.UpdatedAt = time.Now()
	if willRetry {
		knowledge.SummaryStatus = types.SummaryStatusPending
		return ""
	}
	fallback := firstTextChunkSummaryFallback(textChunks)
	knowledge.Description = fallback
	knowledge.SummaryStatus = types.SummaryStatusFailed
	return fallback
}

// summaryTaskWillRetry reports whether the current Asynq delivery has another
// configured attempt remaining. Calls outside an Asynq worker are terminal.
func summaryTaskWillRetry(ctx context.Context) bool {
	retried, retryOK := asynq.GetRetryCount(ctx)
	maxRetry, maxRetryOK := asynq.GetMaxRetry(ctx)
	if retryOK && maxRetryOK {
		return retried < maxRetry
	}
	retried, maxRetry, ok := types.TaskRetryMetadataFromContext(ctx)
	return ok && retried < maxRetry
}

// checkSufficientSummaryContent returns errInsufficientSummaryContent if the
// given content does not carry enough real text (after stripping image markup)
// for an LLM summary call, and logs a warning at the call site. Returns nil
// when the content passes the threshold.
//
// Extracted so the threshold gate can be unit-tested without standing up the
// full ProcessSummaryGeneration dependency graph.
func checkSufficientSummaryContent(ctx context.Context, knowledgeID, content string) error {
	realTextLen := realTextRuneCount(content)
	if realTextLen < minTextContentRunes {
		logger.GetLogger(ctx).Warnf(
			"summary content check: knowledge %s has insufficient text after stripping image markup (real_text_runes=%d, min=%d); skipping LLM call",
			knowledgeID, realTextLen, minTextContentRunes,
		)
		return errInsufficientSummaryContent
	}
	return nil
}

// sortChunksForSummary orders chunks for document reconstruction. Parser
// StartAt offsets stay authoritative until any chunk has been manually edited;
// after that ChunkIndex is the safer reading order.
func sortChunksForSummary(chunks []*types.Chunk) []*types.Chunk {
	sorted := make([]*types.Chunk, len(chunks))
	copy(sorted, chunks)
	edited := false
	for _, chunk := range sorted {
		if chunk.ContentRevision > 0 {
			edited = true
			break
		}
	}
	sort.Slice(sorted, func(i, j int) bool {
		if edited {
			if sorted[i].ChunkIndex != sorted[j].ChunkIndex {
				return sorted[i].ChunkIndex < sorted[j].ChunkIndex
			}
			return sorted[i].ID < sorted[j].ID
		}
		return sorted[i].StartAt < sorted[j].StartAt
	})
	return sorted
}

// getSummary generates a summary for knowledge content using an AI model
func (s *knowledgeService) getSummary(ctx context.Context,
	summaryModel chat.Chat, knowledge *types.Knowledge, chunks []*types.Chunk,
) (string, error) {
	// Get knowledge info from the first chunk
	if len(chunks) == 0 {
		return "", fmt.Errorf("no chunks provided for summary generation")
	}

	// Determine max input chars from config
	maxInputChars := defaultMaxInputChars
	if s.config.Conversation.Summary != nil && s.config.Conversation.Summary.MaxInputChars > 0 {
		maxInputChars = s.config.Conversation.Summary.MaxInputChars
	}

	// Sort chunks for reconstruction. Parser offsets remain authoritative until
	// any chunk has been manually edited; after that ChunkIndex is safer.
	sortedChunks := sortChunksForSummary(chunks)

	// Reconstruct the document before enriching it with image info. Enrichment
	// must happen AFTER reconstruction because StartAt is based on original
	// document offsets; enriched (longer) content would break the positioning.
	chunkContents := ""
	hasEditedChunk := false
	for _, chunk := range sortedChunks {
		if chunk.ContentRevision > 0 {
			hasEditedChunk = true
			break
		}
	}
	if hasEditedChunk {
		// Parser offsets describe the immutable source. Once a replacement has
		// changed length they can no longer be applied to the effective content;
		// concatenate current chunks instead of truncating at stale offsets.
		parts := make([]string, 0, len(sortedChunks))
		for _, chunk := range sortedChunks {
			if chunk.IsEnabled && strings.TrimSpace(chunk.Content) != "" {
				parts = append(parts, chunk.Content)
			}
		}
		chunkContents = strings.Join(parts, "\n\n")
	} else {
		// Synthetic table headers are not represented in StartAt/EndAt. Match
		// real text overlap instead of slicing by source offsets.
		chunkContents = searchutil.MergeTextChunks(sortedChunks, "")
	}

	// Collect image_info from image_ocr/image_caption children and enrich
	chunkIDs := make([]string, len(sortedChunks))
	for i, c := range sortedChunks {
		chunkIDs[i] = c.ID
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, knowledge.TenantID, chunkIDs)
	mergedImageInfo := searchutil.MergeImageInfoJSON(imageInfoMap)
	if mergedImageInfo != "" {
		// For image-dominated documents (e.g. a docx whose only payload is a
		// single embedded picture, or a screenshot-only file), captions alone
		// often carry too little signal — the real content lives in OCR text.
		// Detect that case by measuring the document's real (non-image-markup)
		// text BEFORE enrichment, and switch to full enrichment (caption + OCR)
		// when the body is essentially empty. Text-heavy documents stay on the
		// caption-only path to avoid OCR noise (page headers/footers/watermarks
		// from many figures diluting the main topic).
		if realTextRuneCount(chunkContents) < imageDominatedTextThreshold {
			// Caption + OCR (no URL/original wrappers — those are pure noise
			// for the summary LLM and have been observed to trigger the
			// "image reference with no extracted text" refusal heuristic).
			chunkContents = searchutil.EnrichContentCaptionAndOCR(chunkContents, mergedImageInfo)
		} else {
			chunkContents = searchutil.EnrichContentCaptionOnly(chunkContents, mergedImageInfo)
		}
	}

	// Apply length limit: sample long content to fit within maxInputChars
	chunkContents = sampleLongContent(chunkContents, maxInputChars)

	logger.GetLogger(ctx).Infof("getSummary: content length=%d chars (max=%d) for knowledge %s",
		len([]rune(chunkContents)), maxInputChars, knowledge.ID)

	// Bail out before the LLM call when there is not enough actual text to
	// summarise. We deliberately do not pass filename/file-type metadata to the
	// LLM: scanned PDFs frequently carry filenames like "MX5280.pdf" (the
	// scanner model), and feeding that to the model would invite it to
	// hallucinate a scanner manual instead of admitting the document had no
	// extractable text.
	if err := checkSufficientSummaryContent(ctx, knowledge.ID, chunkContents); err != nil {
		return "", err
	}

	// User-authored metadata is trusted document context. Internal ingestion
	// metadata remains excluded because it contains IDs and pipeline controls.
	contentWithMetadata := chunkContents
	if custom := knowledge.CustomMetadataText(); custom != "" {
		contentWithMetadata = "Document metadata:\n" + custom + "\n\nDocument content:\n" + chunkContents
	}
	contentWithMetadata = sampleLongContent(contentWithMetadata, maxInputChars)

	// Determine max output tokens from config
	maxTokens := 2048
	if s.config.Conversation.Summary != nil && s.config.Conversation.Summary.MaxCompletionTokens > 0 {
		maxTokens = s.config.Conversation.Summary.MaxCompletionTokens
	}

	// Generate summary using AI model
	summaryPrompt := types.RenderPromptPlaceholders(s.config.Conversation.GenerateSummaryPrompt, types.PlaceholderValues{
		"language": types.LanguageNameFromContext(ctx),
	})
	thinking := false
	modelCtx := types.WithLLMCallMetadata(ctx, "document_summary", "")
	summary, err := summaryModel.Chat(modelCtx, []chat.Message{
		{
			Role:    "system",
			Content: summaryPrompt,
		},
		{
			Role:    "user",
			Content: contentWithMetadata,
		},
	}, &chat.ChatOptions{
		Temperature: 0.3,
		MaxTokens:   maxTokens,
		Thinking:    &thinking,
	})
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("GetSummary failed")
		return "", err
	}
	content, err := validateSummaryOutput(summary)
	if err != nil {
		logger.GetLogger(ctx).WithField("error", err).Warnf("GetSummary returned no usable content")
		return "", err
	}
	logger.GetLogger(ctx).WithField("summary", content).Infof("GetSummary success")
	return content, nil
}

// sampleLongContent returns content that fits within maxChars.
// For short content (≤ maxChars), it is returned as-is.
// For long content, it samples: head (60%), tail (20%), and evenly-spaced middle (20%),
// joined by "[...content omitted...]" markers so the LLM knows content was skipped.
func sampleLongContent(content string, maxChars int) string {
	runes := []rune(content)
	if len(runes) <= maxChars {
		return content
	}

	const omitMarker = "\n\n[...content omitted...]\n\n"
	omitRunes := len([]rune(omitMarker))

	// Reserve space for two omit markers (head→middle, middle→tail)
	usable := maxChars - 2*omitRunes
	if usable < 100 {
		// Fallback: just truncate
		return string(runes[:maxChars])
	}

	headLen := usable * 60 / 100
	tailLen := usable * 20 / 100
	midLen := usable - headLen - tailLen

	head := string(runes[:headLen])
	tail := string(runes[len(runes)-tailLen:])

	// Sample middle portion: take a contiguous block from the center of the document
	midStart := len(runes)/2 - midLen/2
	if midStart < headLen {
		midStart = headLen
	}
	midEnd := midStart + midLen
	if midEnd > len(runes)-tailLen {
		midEnd = len(runes) - tailLen
		midStart = midEnd - midLen
		if midStart < headLen {
			midStart = headLen
		}
	}
	middle := string(runes[midStart:midEnd])

	return head + omitMarker + middle + omitMarker + tail
}

// ProcessSummaryGeneration handles async summary generation task
func (s *knowledgeService) ProcessSummaryGeneration(ctx context.Context, t *asynq.Task) (retErr error) {
	var payload types.SummaryGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal summary generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}
	logger.Infof(ctx, "Processing summary generation for knowledge: %s", payload.KnowledgeID)

	// Set tenant and language context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// A newer attempt (re-upload / edit / reparse) has superseded this one:
	// skip before opening the span or registering the FinalizeSubtask defer
	// so we neither read stale chunks nor decrement the new attempt's counter.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		logger.Infof(ctx, "summary: attempt %d superseded for %s, skipping stale enrichment",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}
	if payload.Refresh {
		var err error
		ctx, err = restoreSummaryRefreshTenantInfo(ctx, s.tenantRepo, payload.TenantID)
		if err == nil {
			_, err = s.RegenerateKnowledgeSummary(ctx, payload.KnowledgeID)
		}
		if err != nil {
			if errors.Is(err, ErrSummaryRefreshStale) {
				logger.Infof(ctx, "Discarding stale summary refresh for knowledge %s", payload.KnowledgeID)
				return nil
			}
			logger.Warnf(ctx, "Summary refresh failed for knowledge %s: %v", payload.KnowledgeID, err)
			if errors.Is(err, errInsufficientSummaryContent) {
				return nil
			}
			return err
		}
		return nil
	}

	// Open a subspan under the parent attempt's postprocess stage so the
	// trace surface shows the real summary-generation duration (LLM call
	// + chunk write + index) instead of just the upstream's enqueue time.
	// Closes via the deferred handler below — every return path lands in
	// the defer, including the early returns ahead.
	span := s.beginPostprocessSubspan(ctx, payload.KnowledgeID, payload.Attempt, "postprocess.summary",
		types.JSONMap{
			"language": payload.Language,
		})
	var summaryErr error
	summaryOut := types.JSONMap{}
	defer func() {
		// Decrement the parent's enrichment counter on terminal exit.
		// "Terminal" is keyed on the value RETURNED to asynq, not on
		// summaryErr: several branches record a failure on the span
		// (summaryErr != nil) yet deliberately `return nil` so asynq does
		// NOT retry (e.g. insufficient text content, KB/knowledge fetch
		// failures). Those are terminal and must drain — keying on
		// summaryErr would skip them and leave the row stuck in
		// "finalizing". When we DO return an error asynq will retry, so
		// we only drain on the final attempt.
		finalizeSubtaskDetached(ctx, s.repo, payload.KnowledgeID, "summary",
			retErr, false, isFinalAsynqAttempt(ctx))
		if span == nil {
			return
		}
		if summaryErr != nil {
			s.failPostprocessSubspan(ctx, span, "SUMMARY_FAILED", summaryErr.Error(), summaryErr)
		} else {
			s.endPostprocessSubspan(ctx, span, summaryOut)
		}
	}()

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		summaryErr = err
		return nil
	}
	// Capture the resolved model id on the span output the moment we
	// know it — debugging "summary stage took 60s" benefits hugely from
	// seeing WHICH chat model was actually used (kb config drift, fall-
	// throughs to a slow upstream, etc.).
	summaryOut["model_id"] = kb.SummaryModelID

	if kb.SummaryModelID == "" {
		logger.Warn(ctx, "Knowledge base summary model ID is empty, skipping summary generation")
		_ = s.repo.UpdateKnowledgeColumn(ctx, payload.KnowledgeID, "summary_status", types.SummaryStatusFailed)
		summaryOut["skipped"] = "no_summary_model"
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		summaryErr = err
		return nil
	}
	// Short-circuit when the user cancelled parsing or the row is being deleted.
	if knowledge != nil {
		switch knowledge.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			logger.Infof(ctx, "Summary generation: knowledge aborted (%s), skipping: %s",
				knowledge.ParseStatus, payload.KnowledgeID)
			summaryOut["skipped"] = "knowledge_" + knowledge.ParseStatus
			return nil
		}
	}

	// Update summary status to processing
	knowledge.SummaryStatus = types.SummaryStatusProcessing
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Warnf(ctx, "Failed to update summary status to processing: %v", err)
	}

	// Helper function to mark summary as failed
	markSummaryFailed := func() {
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
			logger.Warnf(ctx, "Failed to update summary status to failed: %v", err)
		}
	}

	// Get text chunks for this knowledge
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunks: %v", err)
		markSummaryFailed()
		summaryErr = err
		return nil
	}

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}
	summaryOut["text_chunks"] = len(textChunks)

	if len(textChunks) == 0 {
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		knowledge.Description = ""
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		summaryOut["skipped"] = "no_text_chunks"
		return nil
	}

	// Sort chunks by ChunkIndex for proper ordering
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
	})

	summaryMetadataVersion := string(knowledge.CustomMetadata)
	handleRetryableSummaryFailure := func(generationErr error) error {
		summaryErr = generationErr
		summaryOut["error"] = previewText(generationErr.Error(), 500)
		summaryOut["error_type"] = fmt.Sprintf("%T", generationErr)

		if summaryTaskWillRetry(ctx) {
			applyRetryableSummaryFailureState(knowledge, textChunks, true)
			if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
				logger.Warnf(ctx, "Failed to mark summary pending for retry: %v", updateErr)
			}
			summaryOut["retrying"] = true
			return fmt.Errorf("summary generation attempt failed: %w", generationErr)
		}

		// Before publishing the terminal fallback, make sure its source still
		// matches the chunks and metadata captured for this attempt.
		stale, staleErr := summarySourceChanged(
			ctx, s.repo, s.chunkRepo, payload.TenantID, payload.KnowledgeID,
			summaryMetadataVersion, textChunks,
		)
		if staleErr != nil {
			logger.Errorf(ctx, "Failed to verify summary fallback freshness for knowledge %s: %v",
				payload.KnowledgeID, staleErr)
			markSummaryFailed()
			summaryErr = staleErr
			return fmt.Errorf("verify summary fallback freshness: %w", staleErr)
		}
		if stale {
			logger.Infof(ctx, "Discarding stale summary fallback for knowledge %s", payload.KnowledgeID)
			summaryOut["skipped"] = "content_revision_changed"
			return nil
		}

		fallback := applyRetryableSummaryFailureState(knowledge, textChunks, false)
		if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
			logger.Errorf(ctx, "Failed to save terminal summary fallback: %v", updateErr)
			summaryErr = updateErr
			return fmt.Errorf("save terminal summary fallback: %w", updateErr)
		}
		if fallback == "" {
			summaryOut["fallback"] = "empty"
		} else {
			summaryOut["fallback"] = "first_chunk"
		}
		summaryOut["fallback_chars"] = len([]rune(fallback))
		return fmt.Errorf("summary generation exhausted retries: %w", generationErr)
	}

	// Initialize chat model for summary. Model resolution failures use the same
	// retry budget and terminal first-chunk fallback as LLM request failures.
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return handleRetryableSummaryFailure(fmt.Errorf("get chat model: %w", err))
	}

	// Generate summary
	summary, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate summary for knowledge %s: %v", payload.KnowledgeID, err)
		// Surface the underlying LLM/IO error on the span so the trace UI
		// can explain "why did this stage take 60s and then fall back?"
		// without forcing the operator to grep worker logs. We also capture
		// the error type to disambiguate timeouts from upstream HTTP errors
		// (deadline exceeded vs unexpected EOF vs 5xx, etc.).
		summaryOut["error"] = previewText(err.Error(), 500)
		summaryOut["error_type"] = fmt.Sprintf("%T", err)
		// For the insufficient-content case (scanned PDF without OCR, etc.)
		// we deliberately do NOT fall back to the first chunk's raw content,
		// since that chunk is typically just a bare markdown image reference
		// and surfacing it in the description is misleading.
		if errors.Is(err, errInsufficientSummaryContent) {
			knowledge.Description = ""
			knowledge.SummaryStatus = types.SummaryStatusFailed
			knowledge.UpdatedAt = time.Now()
			if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
				logger.Errorf(ctx, "Failed to mark summary as failed: %v", updateErr)
				summaryErr = updateErr
				return fmt.Errorf("failed to update knowledge: %w", updateErr)
			}
			summaryOut["fallback"] = "insufficient_content"
			summaryErr = err
			return nil
		}
		return handleRetryableSummaryFailure(err)
	}
	// Do not publish an answer derived from a superseded chunk or metadata
	// version. A user can explicitly refresh again from the latest revision.
	staleSummary, staleErr := summarySourceChanged(
		ctx, s.repo, s.chunkRepo, payload.TenantID, payload.KnowledgeID, summaryMetadataVersion, textChunks,
	)
	if staleErr != nil {
		logger.Errorf(ctx, "Failed to verify summary freshness for knowledge %s: %v", payload.KnowledgeID, staleErr)
		markSummaryFailed()
		summaryErr = staleErr
		return fmt.Errorf("verify summary freshness: %w", staleErr)
	}
	if staleSummary {
		logger.Infof(ctx, "Discarding stale summary for knowledge %s", payload.KnowledgeID)
		summaryOut["skipped"] = "content_revision_changed"
		return nil
	}

	// Update knowledge description
	knowledge.Description = summary
	knowledge.SummaryStatus = types.SummaryStatusCompleted
	knowledge.UpdatedAt = time.Now()
	summaryOut["summary_chars"] = len([]rune(summary))
	// Preview the generated summary on the span output so the trace
	// viewer can show "this is what the LLM produced" at a glance,
	// without hopping to the knowledge-detail page. Capped to keep
	// span rows compact.
	summaryOut["summary_preview"] = previewText(summary, 240)
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge description: %v", err)
		summaryErr = err
		return fmt.Errorf("failed to update knowledge: %w", err)
	}

	// Create summary chunk and index it — only when RAG indexing is enabled.
	// Wiki-only KBs don't need summary chunks in the vector index.
	if strings.TrimSpace(summary) != "" && kb.NeedsEmbeddingModel() {
		// Get max chunk index
		maxChunkIndex := 0
		for _, chunk := range chunks {
			if chunk.ChunkIndex > maxChunkIndex {
				maxChunkIndex = chunk.ChunkIndex
			}
		}

		// Embed only the LLM-generated summary in the indexed chunk.
		// We deliberately omit knowledge.FileName here: filenames are an
		// unreliable signal (e.g. "MX5280.pdf" for a scanned legal letter)
		// and surfacing them in retrieved RAG context can re-introduce the
		// hallucination vector this branch is meant to close.
		summaryChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Content:         fmt.Sprintf("# Summary\n%s", summary),
			ChunkIndex:      maxChunkIndex + 1,
			IsEnabled:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
			StartAt:         0,
			EndAt:           0,
			ChunkType:       types.ChunkTypeSummary,
			ParentChunkID:   textChunks[0].ID,
		}

		// Save summary chunk
		if err := s.chunkService.CreateChunks(ctx, []*types.Chunk{summaryChunk}); err != nil {
			logger.Errorf(ctx, "Failed to create summary chunk: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to create summary chunk: %w", err)
		}

		// Index summary chunk
		tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get tenant info: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to get tenant info: %w", err)
		}
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

		retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
			ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
		if err != nil {
			logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to init retrieve engine: %w", err)
		}

		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get embedding model: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to get embedding model: %w", err)
		}

		indexInfo := []*types.IndexInfo{{
			Content:         summaryChunk.Content,
			SourceID:        summaryChunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         summaryChunk.ID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			IsEnabled:       true,
		}}

		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo); err != nil {
			logger.Errorf(ctx, "Failed to index summary chunk: %v", err)
			summaryErr = err
			return fmt.Errorf("failed to index summary chunk: %w", err)
		}

		logger.Infof(ctx, "Successfully created and indexed summary chunk for knowledge: %s", payload.KnowledgeID)
		summaryOut["summary_chunk_indexed"] = true
	}

	logger.Infof(ctx, "Successfully generated summary for knowledge: %s", payload.KnowledgeID)
	summaryOut["status"] = "completed"
	return nil
}

// ProcessQuestionGeneration handles async question generation task. It
// dispatches between the batched fan-out path (current: one task per window of
// text chunks, payload.ChunkIDs set) and the legacy whole-knowledge path (kept
// for tasks enqueued before fan-out shipped, no chunk ids). A lone ChunkID
// (from an interim per-chunk build) is treated as a one-element batch.
func (s *knowledgeService) ProcessQuestionGeneration(ctx context.Context, t *asynq.Task) (retErr error) {
	var payload types.QuestionGenerationPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal question generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}
	if len(payload.ChunkIDs) > 0 || payload.ChunkID != "" {
		return s.processQuestionGenerationForChunks(ctx, t, payload)
	}
	return s.processQuestionGenerationForKnowledge(ctx, t, payload)
}

// processQuestionGenerationForKnowledge is the legacy whole-knowledge handler:
// it iterates every text chunk of the knowledge in one task. Retained for
// in-flight tasks queued before per-chunk fan-out; new enqueues always set
// payload.ChunkID and take the per-chunk path instead.
func (s *knowledgeService) processQuestionGenerationForKnowledge(ctx context.Context, t *asynq.Task, payload types.QuestionGenerationPayload) (retErr error) {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	exitStatus := "success"
	totalChunks := 0
	totalTextChunks := 0
	emptyContentChunks := 0
	llmCallAttempts := 0
	llmCallSuccess := 0
	llmCallFailed := 0
	llmCallEmpty := 0
	generatedQuestionsTotal := 0
	chunkMetadataSetFailed := 0
	chunkUpdateFailed := 0
	indexEntriesPrepared := 0
	indexBatchAttempted := false
	indexBatchSucceeded := false
	// Sample question + model id surfaced on the span output so the
	// trace viewer can answer "what did the LLM actually produce?" and
	// "which model did it run on?" without joining back to the chunk
	// store. Captured the first time we see a non-empty question batch.
	var sampleQuestion string
	var resolvedModelID string
	// Postprocess subspan for the trace viewer. Opened lazily after we
	// unmarshal the payload (so we have payload.Attempt) and closed in
	// the defer below alongside the stats log so the span output mirrors
	// what we already log to stdout.
	var qSpan *Span
	var qErr error
	// Set when a newer attempt supersedes this run; suppresses the
	// FinalizeSubtask decrement so a stale task can't drain the new
	// attempt's counter.
	superseded := false
	// Decrement enrichment counter on terminal exit. Keyed on the value
	// RETURNED to asynq (retErr), not qErr: some branches record a span
	// failure (qErr != nil) yet `return nil` so asynq won't retry (KB /
	// knowledge fetch failures); those are terminal and must drain.
	// Keying on qErr would skip them and strand the row in "finalizing".
	// When we return an error asynq retries, so we only drain on the
	// final attempt. Runs AFTER the stats-log defer below — defers
	// unwind LIFO, so this one declared first executes last.
	defer func() {
		finalizeSubtaskDetached(ctx, s.repo, payload.KnowledgeID, "question_legacy",
			retErr, superseded, isFinalAsynqAttempt(ctx))
	}()
	defer func() {
		logger.Infof(
			ctx,
			"Question generation stats: knowledge=%s kb=%s retry=%d/%d status=%s elapsed=%s chunks(total=%d,text=%d,empty_text=%d) llm(attempt=%d,success=%d,empty=%d,failed=%d) generated_questions=%d chunk_update_failed=%d metadata_set_failed=%d index(prepared=%d,attempted=%v,succeeded=%v)",
			payload.KnowledgeID,
			payload.KnowledgeBaseID,
			retryCount,
			maxRetry,
			exitStatus,
			time.Since(taskStartedAt).Round(time.Millisecond),
			totalChunks,
			totalTextChunks,
			emptyContentChunks,
			llmCallAttempts,
			llmCallSuccess,
			llmCallEmpty,
			llmCallFailed,
			generatedQuestionsTotal,
			chunkUpdateFailed,
			chunkMetadataSetFailed,
			indexEntriesPrepared,
			indexBatchAttempted,
			indexBatchSucceeded,
		)
		if qSpan != nil {
			out := types.JSONMap{
				"status":                 exitStatus,
				"total_chunks":           totalChunks,
				"text_chunks":            totalTextChunks,
				"empty_content_chunks":   emptyContentChunks,
				"llm_attempts":           llmCallAttempts,
				"llm_success":            llmCallSuccess,
				"llm_empty":              llmCallEmpty,
				"llm_failed":             llmCallFailed,
				"questions_generated":    generatedQuestionsTotal,
				"chunk_update_failed":    chunkUpdateFailed,
				"metadata_set_failed":    chunkMetadataSetFailed,
				"index_entries_prepared": indexEntriesPrepared,
				"index_batch_attempted":  indexBatchAttempted,
				"index_batch_succeeded":  indexBatchSucceeded,
				"retry":                  retryCount,
				"max_retry":              maxRetry,
			}
			// Surface the resolved model id and a sample question on the
			// span output. These help debugging "why is question generation
			// slow" — both questions ("which model was hit?") and ("what
			// did it produce?") are hard to answer from logs alone.
			if resolvedModelID != "" {
				out["model_id"] = resolvedModelID
			}
			if sampleQuestion != "" {
				out["sample_question"] = sampleQuestion
			}
			// Treat any non-success exitStatus as a failed run; the
			// existing stats-string already enumerates them. qErr stays
			// optional for callers that want to surface a Go error.
			if exitStatus != "success" || qErr != nil {
				msg := exitStatus
				var detailErr error = qErr
				if qErr != nil {
					msg = qErr.Error()
				}
				s.failPostprocessSubspan(ctx, qSpan, "QUESTION_FAILED", msg, detailErr)
			} else {
				s.endPostprocessSubspan(ctx, qSpan, out)
			}
		}
	}()

	logger.Infof(ctx, "Processing question generation for knowledge: %s", payload.KnowledgeID)

	// A newer attempt has superseded this one: skip before opening the span
	// so we don't read stale chunks. superseded suppresses the counter
	// decrement in the defer above; qSpan stays nil so the stats defer no-ops.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		superseded = true
		exitStatus = "superseded"
		logger.Infof(ctx, "question: attempt %d superseded for %s, skipping stale enrichment",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}

	// Open the postprocess.question subspan now that we have payload.Attempt.
	// Closes via the defer above.
	qSpan = s.beginPostprocessSubspan(ctx, payload.KnowledgeID, payload.Attempt, "postprocess.question",
		types.JSONMap{
			"question_count": payload.QuestionCount,
			"language":       payload.Language,
		})

	// Set tenant context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	if strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt) == "" {
		exitStatus = "prompt_not_configured"
		logger.Errorf(ctx, "GenerateQuestionsPrompt is empty: configure conversation.generate_questions_prompt_id")
		qErr = fmt.Errorf("generate questions prompt not configured")
		return qErr
	}

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		exitStatus = "kb_not_found"
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		qErr = err
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		exitStatus = "knowledge_not_found"
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		qErr = err
		return nil
	}
	// Short-circuit when the user cancelled parsing or the row is being deleted.
	if knowledge != nil {
		switch knowledge.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			exitStatus = "knowledge_" + knowledge.ParseStatus
			logger.Infof(ctx, "Question generation: knowledge aborted (%s), skipping: %s",
				knowledge.ParseStatus, payload.KnowledgeID)
			return nil
		}
	}

	// Get text chunks for this knowledge
	chunks, err := s.chunkService.ListChunksByKnowledgeID(ctx, payload.KnowledgeID)
	if err != nil {
		exitStatus = "list_chunks_failed"
		logger.Errorf(ctx, "Failed to get chunks: %v", err)
		return nil
	}
	totalChunks = len(chunks)

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}
	totalTextChunks = len(textChunks)

	if len(textChunks) == 0 {
		exitStatus = "no_text_chunks"
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		return nil
	}

	// Sort chunks by StartAt for context building
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].StartAt < textChunks[j].StartAt
	})

	// Initialize chat model
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		exitStatus = "get_chat_model_failed"
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}
	resolvedModelID = kb.SummaryModelID

	// Initialize embedding model and retrieval engine
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		exitStatus = "get_embedding_model_failed"
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		return fmt.Errorf("failed to get embedding model: %w", err)
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		exitStatus = "get_tenant_failed"
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil {
		exitStatus = "init_retrieve_engine_failed"
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		return fmt.Errorf("failed to init retrieve engine: %w", err)
	}

	questionCount := payload.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	processOverrides, _ := knowledge.ProcessOverrides()
	questionGenCfg := ResolveProcessConfig(kb, processOverrides).QuestionGenerationConfig
	customInstructions := questionGenCfg.CustomInstructions

	// Collect image info for all text chunks so question generation can
	// see caption / OCR text instead of bare image links.
	textChunkIDs := make([]string, len(textChunks))
	for i, c := range textChunks {
		textChunkIDs[i] = c.ID
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, payload.TenantID, textChunkIDs)

	enrichContent := func(chunk *types.Chunk) string {
		if info, ok := imageInfoMap[chunk.ID]; ok && info != "" {
			return searchutil.EnrichContentWithImageInfo(chunk.Content, info)
		}
		return chunk.Content
	}

	// Generate questions for each chunk with context
	var indexInfoList []*types.IndexInfo
	for i, chunk := range textChunks {
		if strings.TrimSpace(chunk.Content) == "" {
			emptyContentChunks++
			continue
		}

		// Build context from adjacent chunks
		var prevContent, nextContent string
		if i > 0 {
			prevContent = enrichContent(textChunks[i-1])
		}
		if i < len(textChunks)-1 {
			nextContent = enrichContent(textChunks[i+1])
		}

		generationRevision := chunk.ContentRevision
		llmCallAttempts++
		questions, err := s.generateQuestionsWithContext(ctx, chatModel, enrichContent(chunk), prevContent, nextContent,
			knowledge.Title, questionCount, customInstructions)
		if err != nil {
			llmCallFailed++
			logger.Warnf(ctx, "Failed to generate questions for chunk %s: %v", chunk.ID, err)
			continue
		}

		if len(questions) == 0 {
			llmCallEmpty++
			continue
		}
		latestChunk, latestErr := s.chunkRepo.GetChunkByID(ctx, payload.TenantID, chunk.ID)
		if latestErr != nil || latestChunk.ContentRevision != generationRevision {
			logger.Infof(ctx, "Skipping stale generated questions for chunk %s (revision changed)", chunk.ID)
			continue
		}
		chunk = latestChunk
		llmCallSuccess++
		generatedQuestionsTotal += len(questions)
		if sampleQuestion == "" && len(questions) > 0 {
			sampleQuestion = previewText(questions[0], 200)
		}

		// Update chunk metadata with unique IDs for each question
		generatedQuestions := make([]types.GeneratedQuestion, len(questions))
		questionRevision := chunk.ContentRevision
		for j, question := range questions {
			questionID := fmt.Sprintf("q%d", time.Now().UnixNano()+int64(j))
			generatedQuestions[j] = types.GeneratedQuestion{
				ID:              questionID,
				Question:        question,
				ContentRevision: &questionRevision,
			}
		}
		meta := &types.DocumentChunkMetadata{
			GeneratedQuestions: generatedQuestions, GeneratedQuestionsRevision: chunk.ContentRevision,
		}
		if err := chunk.SetDocumentMetadata(meta); err != nil {
			chunkMetadataSetFailed++
			logger.Warnf(ctx, "Failed to set document metadata for chunk %s: %v", chunk.ID, err)
			continue
		}

		// Update chunk in database
		if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
			chunkUpdateFailed++
			logger.Warnf(ctx, "Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}

		// Create index entries for generated questions
		for _, gq := range generatedQuestions {
			sourceID := types.GeneratedQuestionSourceID(chunk.ID, gq.ID)
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         buildKnowledgeIndexContent(knowledge, gq.Question),
				SourceID:        sourceID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				IsEnabled:       true,
			})
		}
		logger.Debugf(ctx, "Generated %d questions for chunk %s", len(questions), chunk.ID)
	}
	indexEntriesPrepared = len(indexInfoList)

	// Index generated questions
	if len(indexInfoList) > 0 {
		indexBatchAttempted = true
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
			exitStatus = "index_questions_failed"
			logger.Errorf(ctx, "Failed to index generated questions: %v", err)
			return fmt.Errorf("failed to index questions: %w", err)
		}
		indexBatchSucceeded = true
		logger.Infof(ctx, "Successfully indexed %d generated questions for knowledge: %s", len(indexInfoList), payload.KnowledgeID)
	}

	return nil
}

// processQuestionGenerationForChunks generates questions for a batch (window)
// of text chunks. This is the batched fan-out path (one asynq task per
// questionGenChunkBatchSize chunks), aligned with the graph-extract
// TypeChunkExtract pattern: independent retry, per-batch cancellation, and a
// postprocess.question.batch[i] subspan. The payload carries only chunk ids
// (never content); content is read fresh here, and all questions for the batch
// are indexed in a single embedding BatchIndex call.
func (s *knowledgeService) processQuestionGenerationForChunks(ctx context.Context, t *asynq.Task, payload types.QuestionGenerationPayload) (retErr error) {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	// Normalize the batch: prefer ChunkIDs, fall back to a lone ChunkID
	// (interim per-chunk build) so those in-flight tasks still run.
	batchIDs := payload.ChunkIDs
	if len(batchIDs) == 0 && payload.ChunkID != "" {
		batchIDs = []string{payload.ChunkID}
	}

	exitStatus := "success"
	chunksInBatch := len(batchIDs)
	chunksProcessed := 0
	emptyChunks := 0
	llmCallFailed := 0
	generatedQuestionsTotal := 0
	indexEntriesPrepared := 0
	indexBatchSucceeded := false
	var sampleQuestion string
	var resolvedModelID string
	var qSpan *Span
	var qErr error
	// Suppresses the FinalizeSubtask drain when a newer attempt superseded
	// this run, so a stale task can't decrement the new attempt's counter.
	superseded := false

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// Drain the parent's enrichment counter on terminal exit. Keyed on the
	// value RETURNED to asynq (retErr), not qErr: some branches record a
	// span failure yet `return nil` (terminal, must drain). Declared first
	// so it runs LAST (after the stats/span defer below).
	defer func() {
		finalizeSubtaskDetached(ctx, s.repo, payload.KnowledgeID,
			fmt.Sprintf("question_batch[%d]", payload.BatchIndex),
			retErr, superseded, isFinalAsynqAttempt(ctx))
	}()
	defer func() {
		logger.Infof(ctx,
			"Question generation (batch) stats: knowledge=%s batch=%d chunks(in_batch=%d,processed=%d,empty=%d) llm_failed=%d retry=%d/%d status=%s elapsed=%s generated_questions=%d index(entries=%d,succeeded=%v)",
			payload.KnowledgeID, payload.BatchIndex, chunksInBatch, chunksProcessed, emptyChunks, llmCallFailed,
			retryCount, maxRetry, exitStatus, time.Since(taskStartedAt).Round(time.Millisecond),
			generatedQuestionsTotal, indexEntriesPrepared, indexBatchSucceeded,
		)
		if qSpan != nil {
			out := types.JSONMap{
				"status":                 exitStatus,
				"batch_index":            payload.BatchIndex,
				"chunks_in_batch":        chunksInBatch,
				"chunks_processed":       chunksProcessed,
				"empty_chunks":           emptyChunks,
				"llm_failed":             llmCallFailed,
				"questions_generated":    generatedQuestionsTotal,
				"index_entries_prepared": indexEntriesPrepared,
				"index_batch_succeeded":  indexBatchSucceeded,
				"retry":                  retryCount,
				"max_retry":              maxRetry,
			}
			if resolvedModelID != "" {
				out["model_id"] = resolvedModelID
			}
			if sampleQuestion != "" {
				out["sample_question"] = sampleQuestion
			}
			if exitStatus != "success" || qErr != nil {
				msg := exitStatus
				if qErr != nil {
					msg = qErr.Error()
				}
				s.failPostprocessSubspan(ctx, qSpan, "QUESTION_FAILED", msg, qErr)
			} else {
				s.endPostprocessSubspan(ctx, qSpan, out)
			}
		}
	}()

	logger.Infof(ctx, "Processing question generation for knowledge=%s batch=%d chunks=%d",
		payload.KnowledgeID, payload.BatchIndex, chunksInBatch)

	if chunksInBatch == 0 {
		exitStatus = "empty_batch"
		return nil
	}

	// A newer attempt has superseded this one: skip before opening the span
	// so we don't read stale chunks and don't drain the new attempt.
	if attemptSuperseded(ctx, s.tracker(), payload.KnowledgeID, payload.Attempt) {
		superseded = true
		exitStatus = "superseded"
		logger.Infof(ctx, "question: attempt %d superseded for %s, skipping stale enrichment",
			payload.Attempt, payload.KnowledgeID)
		return nil
	}

	qSpan = s.beginQuestionBatchSubspan(ctx, payload.KnowledgeID, payload.Attempt,
		fmt.Sprintf("postprocess.question.batch[%d]", payload.BatchIndex),
		types.JSONMap{
			"batch_index":    payload.BatchIndex,
			"chunks":         chunksInBatch,
			"question_count": payload.QuestionCount,
			"language":       payload.Language,
		})

	if strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt) == "" {
		exitStatus = "prompt_not_configured"
		logger.Errorf(ctx, "GenerateQuestionsPrompt is empty: configure conversation.generate_questions_prompt_id")
		qErr = fmt.Errorf("generate questions prompt not configured")
		return qErr
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		exitStatus = "kb_not_found"
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		qErr = err
		return nil
	}

	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		exitStatus = "knowledge_not_found"
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		qErr = err
		return nil
	}
	// Short-circuit when the user cancelled parsing or the row is being
	// deleted — batched fan-out means we get this check for free on every
	// batch, so a cancel stops burning LLM quota on the remaining batches.
	if knowledge != nil {
		switch knowledge.ParseStatus {
		case types.ParseStatusCancelled, types.ParseStatusDeleting:
			exitStatus = "knowledge_" + knowledge.ParseStatus
			logger.Infof(ctx, "Question generation: knowledge aborted (%s), skipping batch %d",
				knowledge.ParseStatus, payload.BatchIndex)
			return nil
		}
	}

	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		exitStatus = "get_chat_model_failed"
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		return fmt.Errorf("failed to get chat model: %w", err)
	}
	resolvedModelID = kb.SummaryModelID

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		exitStatus = "get_embedding_model_failed"
		logger.Errorf(ctx, "Failed to get embedding model: %v", err)
		return fmt.Errorf("failed to get embedding model: %w", err)
	}

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		exitStatus = "get_tenant_failed"
		logger.Errorf(ctx, "Failed to get tenant info: %v", err)
		return fmt.Errorf("failed to get tenant info: %w", err)
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantInfo.ID, kb.VectorStoreID)
	if err != nil {
		exitStatus = "init_retrieve_engine_failed"
		logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
		return fmt.Errorf("failed to init retrieve engine: %w", err)
	}

	questionCount := payload.QuestionCount
	if questionCount <= 0 {
		questionCount = 3
	}
	if questionCount > 10 {
		questionCount = 10
	}

	processOverrides, _ := knowledge.ProcessOverrides()
	questionGenCfg := ResolveProcessConfig(kb, processOverrides).QuestionGenerationConfig
	customInstructions := questionGenCfg.CustomInstructions

	// Fetch the batch chunks (in payload order) plus the two boundary
	// neighbors so we can rebuild the same surrounding context the legacy
	// loop used, all enriched with image OCR / caption info. A vanished
	// chunk degrades gracefully (skipped / empty context).
	getChunk := func(id string) *types.Chunk {
		if id == "" {
			return nil
		}
		c, gerr := s.chunkRepo.GetChunkByID(ctx, payload.TenantID, id)
		if gerr != nil {
			return nil
		}
		return c
	}
	batchChunks := make([]*types.Chunk, len(batchIDs))
	for i, id := range batchIDs {
		batchChunks[i] = getChunk(id)
	}
	prevChunk := getChunk(payload.PrevChunkID)
	nextChunk := getChunk(payload.NextChunkID)

	infoIDs := make([]string, 0, len(batchIDs)+2)
	infoIDs = append(infoIDs, batchIDs...)
	if payload.PrevChunkID != "" {
		infoIDs = append(infoIDs, payload.PrevChunkID)
	}
	if payload.NextChunkID != "" {
		infoIDs = append(infoIDs, payload.NextChunkID)
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, payload.TenantID, infoIDs)
	enrich := func(c *types.Chunk) string {
		if c == nil {
			return ""
		}
		if info, ok := imageInfoMap[c.ID]; ok && info != "" {
			return searchutil.EnrichContentWithImageInfo(c.Content, info)
		}
		return c.Content
	}

	// neighborContent returns the context content for position i within the
	// batch: the in-batch neighbor when present, else the boundary chunk.
	prevContentAt := func(i int) string {
		if i > 0 {
			return enrich(batchChunks[i-1])
		}
		return enrich(prevChunk)
	}
	nextContentAt := func(i int) string {
		if i < len(batchChunks)-1 {
			return enrich(batchChunks[i+1])
		}
		return enrich(nextChunk)
	}

	var indexInfoList []*types.IndexInfo
	for i, chunk := range batchChunks {
		if chunk == nil || strings.TrimSpace(chunk.Content) == "" {
			emptyChunks++
			continue
		}

		generationRevision := chunk.ContentRevision
		questions, gerr := s.generateQuestionsWithContext(
			ctx, chatModel, enrich(chunk), prevContentAt(i), nextContentAt(i), knowledge.Title, questionCount,
			customInstructions)
		if gerr != nil {
			llmCallFailed++
			logger.Warnf(ctx, "Failed to generate questions for chunk %s: %v", chunk.ID, gerr)
			continue
		}
		if len(questions) == 0 {
			continue
		}
		latestChunk, latestErr := s.chunkRepo.GetChunkByID(ctx, payload.TenantID, chunk.ID)
		if latestErr != nil || latestChunk.ContentRevision != generationRevision {
			logger.Infof(ctx, "Skipping stale generated questions for chunk %s (revision changed)", chunk.ID)
			continue
		}
		chunk = latestChunk
		chunksProcessed++
		generatedQuestionsTotal += len(questions)
		if sampleQuestion == "" {
			sampleQuestion = previewText(questions[0], 200)
		}

		generatedQuestions := make([]types.GeneratedQuestion, len(questions))
		questionRevision := chunk.ContentRevision
		for j, question := range questions {
			generatedQuestions[j] = types.GeneratedQuestion{
				ID:              fmt.Sprintf("q%d", time.Now().UnixNano()+int64(j)),
				Question:        question,
				ContentRevision: &questionRevision,
			}
		}
		meta := &types.DocumentChunkMetadata{
			GeneratedQuestions: generatedQuestions, GeneratedQuestionsRevision: chunk.ContentRevision,
		}
		if err := chunk.SetDocumentMetadata(meta); err != nil {
			logger.Warnf(ctx, "Failed to set document metadata for chunk %s: %v", chunk.ID, err)
			continue
		}
		if err := s.chunkService.UpdateChunk(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to update chunk %s: %v", chunk.ID, err)
			continue
		}
		for _, gq := range generatedQuestions {
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         buildKnowledgeIndexContent(knowledge, gq.Question),
				SourceID:        types.GeneratedQuestionSourceID(chunk.ID, gq.ID),
				SourceType:      types.ChunkSourceType,
				ChunkID:         chunk.ID,
				KnowledgeID:     knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID,
				IsEnabled:       true,
			})
		}
	}

	indexEntriesPrepared = len(indexInfoList)
	if len(indexInfoList) > 0 {
		if err := retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfoList); err != nil {
			exitStatus = "index_questions_failed"
			qErr = err
			logger.Errorf(ctx, "Failed to index generated questions for batch %d: %v", payload.BatchIndex, err)
			return fmt.Errorf("failed to index questions: %w", err)
		}
		indexBatchSucceeded = true
		logger.Infof(ctx, "Indexed %d generated questions for knowledge=%s batch=%d",
			len(indexInfoList), payload.KnowledgeID, payload.BatchIndex)
	}
	return nil
}

// generateQuestionsWithContext generates questions for a chunk with surrounding context
func (s *knowledgeService) generateQuestionsWithContext(ctx context.Context,
	chatModel chat.Chat, content, prevContent, nextContent, docName string, questionCount int,
	customInstructions string,
) ([]string, error) {
	if content == "" || questionCount <= 0 {
		return nil, nil
	}

	prompt := strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt)
	if prompt == "" {
		return nil, fmt.Errorf("generate questions prompt not configured")
	}

	// Build context section
	var contextSection string
	if prevContent != "" || nextContent != "" {
		contextSection = "<surrounding_context>\n"
		if prevContent != "" {
			contextSection += fmt.Sprintf("<preceding_content>\n%s\n\n</preceding_content>\n\n", prevContent)
		}
		if nextContent != "" {
			contextSection += fmt.Sprintf("<following_content>\n%s\n\n</following_content>\n\n", nextContent)
		}
		contextSection += "</surrounding_context>\n\n"
	}

	langName := types.LanguageNameFromContext(ctx)
	prompt = types.RenderPromptPlaceholders(prompt, types.PlaceholderValues{
		"question_count": fmt.Sprintf("%d", questionCount),
		"content":        content,
		"context":        contextSection,
		"doc_name":       docName,
		"language":       langName,
	})
	prompt = types.AppendCustomPromptInstructions(prompt, customInstructions, "question_generation")

	thinking := false
	modelCtx := types.WithLLMCallMetadata(ctx, "question_generation", "")
	response, err := chatModel.Chat(modelCtx, []chat.Message{
		{
			Role:    "user",
			Content: prompt,
		},
	}, &chat.ChatOptions{
		Temperature: 0.7,
		MaxTokens:   512,
		Thinking:    &thinking,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate questions: %w", err)
	}

	// Parse response
	lines := strings.Split(response.Content, "\n")
	questions := make([]string, 0, questionCount)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "0123456789.-*) ")
		line = strings.TrimSpace(line)
		if line != "" && len(line) > 5 {
			questions = append(questions, line)
			if len(questions) >= questionCount {
				break
			}
		}
	}

	return questions, nil
}

// RegenerateChunkQuestions synchronously refreshes Doc2Query-style auxiliary
// questions for the current chunk revision and atomically replaces their
// retrieval entries through updateChunkVector.
func (s *knowledgeService) RegenerateChunkQuestions(
	ctx context.Context, chunkID string,
) ([]types.GeneratedQuestion, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	chunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}
	if chunk.ChunkType != types.ChunkTypeText {
		return nil, fmt.Errorf("questions can only be generated for text chunks")
	}
	generationRevision := chunk.ContentRevision
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
	if err != nil {
		return nil, err
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if kb.SummaryModelID == "" {
		return nil, fmt.Errorf("summary model is required for question generation")
	}
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		return nil, err
	}
	resolveNeighbor := func(id string) string {
		if id == "" {
			return ""
		}
		neighbor, getErr := s.chunkRepo.GetChunkByID(ctx, tenantID, id)
		if getErr != nil {
			return ""
		}
		return neighbor.Content
	}
	overrides, _ := knowledge.ProcessOverrides()
	config := ResolveProcessConfig(kb, overrides).QuestionGenerationConfig
	count := config.QuestionCount
	if count <= 0 {
		count = 3
	}
	if count > 10 {
		count = 10
	}
	questions, err := s.generateQuestionsWithContext(
		ctx, chatModel, chunk.Content, resolveNeighbor(chunk.PreChunkID),
		resolveNeighbor(chunk.NextChunkID), knowledge.Title, count, config.CustomInstructions,
	)
	if err != nil {
		return nil, err
	}
	latestChunk, err := s.chunkRepo.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}
	if latestChunk.ContentRevision != generationRevision {
		return nil, ErrChunkRevisionConflict
	}
	chunk = latestChunk
	generated := make([]types.GeneratedQuestion, 0, len(questions))
	questionRevision := chunk.ContentRevision
	for _, question := range questions {
		generated = append(generated, types.GeneratedQuestion{
			ID: uuid.NewString(), Question: question, ContentRevision: &questionRevision,
		})
	}
	meta := &types.DocumentChunkMetadata{
		GeneratedQuestions: generated, GeneratedQuestionsRevision: chunk.ContentRevision,
	}
	if err := chunk.SetDocumentMetadata(meta); err != nil {
		return nil, err
	}
	if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}
	if err := s.updateChunkVector(ctx, chunk.KnowledgeBaseID, []*types.Chunk{chunk}); err != nil {
		return nil, err
	}
	return generated, nil
}

// RegenerateKnowledgeSummary refreshes both the knowledge description and the
// summary chunk(s), then reindexes only those summary chunks. It is
// intentionally idempotent.
func (s *knowledgeService) RegenerateKnowledgeSummary(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, knowledge.KnowledgeBaseID)
	if err != nil {
		return nil, err
	}
	if kb.SummaryModelID == "" {
		return nil, fmt.Errorf("summary model is not configured")
	}
	allChunks, err := s.chunkRepo.ListChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		return nil, err
	}
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range allChunks {
		if chunk.ChunkType == types.ChunkTypeText && chunk.IsEnabled {
			textChunks = append(textChunks, chunk)
		}
	}
	if len(textChunks) == 0 {
		knowledge.Description = ""
		knowledge.SummaryStatus = types.SummaryStatusFailed
		knowledge.UpdatedAt = time.Now()
		if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
			return knowledge, updateErr
		}
		return knowledge, errInsufficientSummaryContent
	}
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
	})
	metadataVersion := string(knowledge.CustomMetadata)
	knowledge.SummaryStatus = types.SummaryStatusProcessing
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return nil, err
	}
	handleGenerationFailure := func(generationErr error) (*types.Knowledge, error) {
		if errors.Is(generationErr, errInsufficientSummaryContent) {
			knowledge.Description = ""
			knowledge.SummaryStatus = types.SummaryStatusFailed
			knowledge.UpdatedAt = time.Now()
			if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
				return knowledge, updateErr
			}
			return knowledge, generationErr
		}
		if summaryTaskWillRetry(ctx) {
			applyRetryableSummaryFailureState(knowledge, textChunks, true)
			if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
				logger.Warnf(ctx, "Failed to mark summary refresh pending for retry: %v", updateErr)
			}
			return knowledge, generationErr
		}

		stale, staleErr := summarySourceChanged(
			ctx, s.repo, s.chunkRepo, tenantID, knowledgeID, metadataVersion, textChunks,
		)
		if staleErr != nil {
			knowledge.SummaryStatus = types.SummaryStatusFailed
			_ = s.repo.UpdateKnowledge(ctx, knowledge)
			return knowledge, fmt.Errorf("verify summary fallback freshness: %w", staleErr)
		}
		if stale {
			return knowledge, ErrSummaryRefreshStale
		}

		applyRetryableSummaryFailureState(knowledge, textChunks, false)
		if updateErr := s.repo.UpdateKnowledge(ctx, knowledge); updateErr != nil {
			return knowledge, updateErr
		}
		return knowledge, generationErr
	}

	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		return handleGenerationFailure(fmt.Errorf("get chat model: %w", err))
	}
	summary, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		return handleGenerationFailure(err)
	}
	stale, err := summarySourceChanged(
		ctx, s.repo, s.chunkRepo, tenantID, knowledgeID, metadataVersion, textChunks,
	)
	if err != nil {
		return nil, fmt.Errorf("verify summary freshness: %w", err)
	}
	if stale {
		logger.Infof(ctx, "Discarding stale summary refresh for knowledge %s", knowledgeID)
		return nil, ErrSummaryRefreshStale
	}
	knowledge.Description = summary
	knowledge.SummaryStatus = types.SummaryStatusCompleted
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		return nil, err
	}
	if kb.NeedsEmbeddingModel() {
		found := false
		maxIndex := 0
		summaryChunks := make([]*types.Chunk, 0, 1)
		for _, chunk := range allChunks {
			if chunk.ChunkIndex > maxIndex {
				maxIndex = chunk.ChunkIndex
			}
			if chunk.ChunkType == types.ChunkTypeSummary {
				chunk.Content = "# Summary\n" + summary
				chunk.SourceContent = chunk.Content
				chunk.IsEnabled = true
				chunk.UpdatedAt = time.Now()
				if err := s.chunkRepo.UpdateChunk(ctx, chunk); err != nil {
					return nil, err
				}
				summaryChunks = append(summaryChunks, chunk)
				found = true
			}
		}
		if !found {
			summaryChunk := &types.Chunk{
				ID: uuid.NewString(), TenantID: tenantID, KnowledgeID: knowledge.ID,
				KnowledgeBaseID: knowledge.KnowledgeBaseID, Content: "# Summary\n" + summary,
				ChunkIndex: maxIndex + 1, IsEnabled: true, ChunkType: types.ChunkTypeSummary,
				ParentChunkID: textChunks[0].ID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
			}
			if err := s.chunkRepo.CreateChunks(ctx, []*types.Chunk{summaryChunk}); err != nil {
				return nil, err
			}
			summaryChunks = append(summaryChunks, summaryChunk)
		}
		if err := s.updateChunkVector(ctx, knowledge.KnowledgeBaseID, summaryChunks); err != nil {
			return nil, err
		}
	}
	return knowledge, nil
}

// ReparseKnowledge deletes existing document content and re-parses the knowledge asynchronously.
// This method reuses the logic from UpdateManualKnowledge for resource cleanup and async parsing.
func (s *knowledgeService) ReparseKnowledge(
	ctx context.Context,
	knowledgeID string,
	processOverrides *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	logger.Info(ctx, "Start re-parsing knowledge")

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to load knowledge: %v", err)
		return nil, err
	}

	// Allocate a fresh span tree attempt up front. Doing this BEFORE
	// the cleanup + enqueue means: (a) the UI immediately sees a new
	// attempt with all five stages back to "pending" instead of the
	// previous run's "failed" badge lingering; (b) the worker's
	// fallback path won't double-allocate when payload.Attempt is
	// already set on the queued task.
	reparseAttempt := 0
	if root, n, err := s.tracker().OpenAttempt(ctx, existing.ID, ""); err == nil && root != nil {
		reparseAttempt = n
	} else if err != nil {
		logger.Warnf(ctx, "[Reparse] OpenAttempt failed for %s: %v (will fall back in worker)", existing.ID, err)
	}

	// Get knowledge base configuration
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, existing.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base for reparse: %v", err)
		return nil, err
	}

	// When the caller supplies new overrides (e.g. via the reparse confirm
	// dialog), validate them against this knowledge's file type, then persist
	// to metadata so both this call's enqueue and the worker re-read the same
	// config. nil keeps whatever was stored at upload time.
	if processOverrides != nil {
		if err := ValidateProcessOverrides(ctx, kb, processOverrides, reparseFileTypes(existing)); err != nil {
			return nil, err
		}
		if err := existing.SetProcessOverrides(processOverrides); err != nil {
			logger.Errorf(ctx, "Failed to set process overrides on reparse: %v", err)
			return nil, err
		}
		if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "metadata", existing.Metadata); err != nil {
			logger.Errorf(ctx, "Failed to persist process overrides on reparse: %v", err)
			return nil, err
		}
	}

	processOverrides, _ = existing.ProcessOverrides()
	reparseEff := ResolveProcessConfig(kb, processOverrides)

	// Keep wiki's pending queue consistent across both manual and non-manual
	// paths. The destructive work (swapping old wiki contributions for new)
	// happens asynchronously inside mapOneDocument — see its oldPageSlugs
	// handling — once post-process re-enqueues wiki ingest. All we need to
	// do here is stop any stale pending ingest op from firing against the
	// pre-reparse chunk set.
	if kb != nil && kb.IsWikiEnabled() {
		s.prepareWikiForReparse(ctx, existing)
	}
	recordReparseStarted := func() {
		recordKBActivity(ctx, s.audit, tenantID, existing.KnowledgeBaseID, types.AuditActionKnowledgeReparseStarted,
			"knowledge", existing.ID, types.AuditOutcomeAccepted,
			map[string]any{"title": existing.Title, "type": existing.Type, "attempt": reparseAttempt})
	}

	// For manual knowledge, use async manual processing (cleanup + re-indexing in worker)
	if existing.IsManual() {
		meta, metaErr := existing.ManualMetadata()
		if metaErr != nil || meta == nil {
			logger.Errorf(ctx, "Failed to get manual metadata for reparse: %v", metaErr)
			return nil, werrors.NewBadRequestError("无法获取手工知识内容")
		}

		resetKnowledgeForReparse(existing, kb)

		if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
			logger.Errorf(ctx, "Failed to update knowledge status before reparse: %v", err)
			return nil, err
		}
		if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "pending_subtasks_count", 0); err != nil {
			logger.Errorf(ctx, "Failed to reset pending_subtasks_count before reparse: %v", err)
			return nil, err
		}

		if _, err := s.enqueueManualProcessing(ctx, existing, meta.Content, true); err != nil {
			logger.Errorf(ctx, "Failed to enqueue manual reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		} else {
			recordReparseStarted()
		}
		return existing, nil
	}

	// For non-manual knowledge, cleanup synchronously then enqueue document processing
	logger.Infof(ctx, "Cleaning up existing resources for knowledge: %s", knowledgeID)
	if err := s.cleanupKnowledgeResources(ctx, existing); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": knowledgeID,
		})
		return nil, err
	}

	// Step 2: Update knowledge status and metadata
	resetKnowledgeForReparse(existing, kb)

	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge status before reparse: %v", err)
		return nil, err
	}
	if err := s.repo.UpdateKnowledgeColumn(ctx, existing.ID, "pending_subtasks_count", 0); err != nil {
		logger.Errorf(ctx, "Failed to reset pending_subtasks_count before reparse: %v", err)
		return nil, err
	}

	// Step 3: Trigger async re-parsing based on knowledge type
	logger.Infof(ctx, "Knowledge status updated, scheduling async reparse, ID: %s, Type: %s", existing.ID, existing.Type)

	// For file-based knowledge, enqueue document processing task
	if existing.FilePath != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := reparseEff.EnableMultimodel
		enableQuestionGeneration := reparseEff.QuestionGenerationConfig.Enabled
		questionCount := reparseEff.QuestionGenerationConfig.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			FilePath:                 existing.FilePath,
			FileName:                 existing.FileName,
			FileType:                 getFileType(existing.FileName),
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
			Attempt:                  reparseAttempt,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal reparse task payload: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(s.config, asynq.MaxRetry(3))...,
		)
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		logger.Infof(ctx, "Enqueued reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)
		recordReparseStarted()

		// For data tables (csv, xlsx, xls), also enqueue summary task
		enqueueDataTableSummaryIfNeeded(ctx, s.task, tenantID, existing.ID, existing.FileName, existing.FileType, kb.SummaryModelID, kb.EmbeddingModelID)

		return existing, nil
	}

	// For file-URL-based knowledge, enqueue document processing task with FileURL field
	if existing.Type == "file_url" && existing.Source != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := reparseEff.EnableMultimodel
		enableQuestionGeneration := reparseEff.QuestionGenerationConfig.Enabled
		questionCount := reparseEff.QuestionGenerationConfig.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			FileURL:                  existing.Source,
			FileName:                 existing.FileName,
			FileType:                 existing.FileType,
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
			Attempt:                  reparseAttempt,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal file URL reparse task payload: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(s.config)...,
		)
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue file URL reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		logger.Infof(ctx, "Enqueued file URL reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)
		recordReparseStarted()

		enqueueDataTableSummaryIfNeeded(ctx, s.task, tenantID, existing.ID, existing.FileName, existing.FileType, kb.SummaryModelID, kb.EmbeddingModelID)

		return existing, nil
	}

	// For URL-based knowledge, enqueue URL processing task
	if existing.Type == "url" && existing.Source != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := reparseEff.EnableMultimodel
		enableQuestionGeneration := reparseEff.QuestionGenerationConfig.Enabled
		questionCount := reparseEff.QuestionGenerationConfig.QuestionCount
		if questionCount <= 0 {
			questionCount = 3
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			URL:                      existing.Source,
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
			Attempt:                  reparseAttempt,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal URL reparse task payload: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}

		task := asynq.NewTask(
			types.TypeDocumentProcess,
			payloadBytes,
			documentProcessTaskOptions(s.config, asynq.MaxRetry(3))...,
		)
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue URL reparse task: %v", err)
			s.markKnowledgeEnqueueFailed(ctx, existing)
			return existing, werrors.NewInternalServerError("Failed to submit processing task")
		}
		logger.Infof(ctx, "Enqueued URL reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)
		recordReparseStarted()

		return existing, nil
	}

	logger.Warnf(ctx, "Knowledge %s has no parseable content (no file, URL, or manual content)", knowledgeID)
	existing.ParseStatus = types.ParseStatusFailed
	existing.ErrorMessage = "Knowledge has no parseable content"
	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to persist unparseable knowledge state for %s: %v",
			secutils.SanitizeForLog(knowledgeID), err)
	}
	return existing, werrors.NewBadRequestError("Knowledge has no parseable content")
}

// resetKnowledgeForReparse makes the top-level knowledge state describe the
// new processing attempt rather than retaining terminal state from the
// previous one.
func resetKnowledgeForReparse(knowledge *types.Knowledge, kb *types.KnowledgeBase) {
	knowledge.ParseStatus = types.ParseStatusPending
	knowledge.EnableStatus = "disabled"
	knowledge.Description = ""
	knowledge.ProcessedAt = nil
	knowledge.ErrorMessage = ""
	knowledge.EmbeddingModelID = kb.EmbeddingModelID
	// UpdateKnowledge deliberately omits pending_subtasks_count, so callers
	// must still persist this reset through an explicit column update.
	knowledge.PendingSubtasksCount = 0
}

// CancelKnowledgeParse marks an in-progress parse as cancelled by the user.
//
// Semantics (kept aligned with the existing deleting path, but partial work
// is preserved instead of cleaned up):
//   - parse_status is set to "cancelled"; partial chunks/index already written
//     to the database remain on disk. The user can re-trigger parsing via the
//     existing ReparseKnowledge API, which overwrites status back to pending.
//   - Any in-flight worker reads the new status at its next checkpoint and
//     bails (see processChunks / ProcessDocument / downstream handlers).
//   - The asynq inspector (if available) dequeues pending / scheduled / retry
//     tasks for this knowledge_id across all queues used by the pipeline
//     and signals active workers to stop. Lite mode (no Redis) skips the
//     dequeue step — the checkpoint-based abort is the only stop signal there.
//   - Idempotent: re-calling on an already-cancelled row is a no-op.
//
// Errors:
//   - ParseStatusCompleted / ParseStatusFailed: the parse has already finished.
//   - ParseStatusDeleting: a delete is in progress; cancel cannot supersede it.
func (s *knowledgeService) CancelKnowledgeParse(
	ctx context.Context, knowledgeID string,
) (*types.Knowledge, error) {
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "CancelKnowledgeParse: failed to load knowledge: %v", err)
		return nil, err
	}
	if existing == nil {
		return nil, werrors.NewNotFoundError("knowledge not found")
	}

	switch existing.ParseStatus {
	case types.ParseStatusCancelled:
		// Idempotent — still attempt the dequeue in case earlier calls
		// raced an enqueue, but skip the row update / span close path.
		s.dequeueKnowledgeTasks(ctx, knowledgeID)
		return existing, nil
	case types.ParseStatusCompleted, types.ParseStatusFailed:
		return nil, werrors.NewBadRequestError("解析已结束，无法取消")
	case types.ParseStatusDeleting:
		return nil, werrors.NewBadRequestError("知识正在删除中，无法取消解析")
	case types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing:
		// Cancellable. `finalizing` is the post-process fan-out window
		// where graph-extract / summary / question subtasks are still
		// running; cancel here stops the LLM cost they would burn.
	default:
		// Unknown status — let it through but log. Should never happen
		// outside test fixtures or hand-edited rows.
		logger.Warnf(ctx, "CancelKnowledgeParse: unexpected status %q for %s, proceeding",
			existing.ParseStatus, knowledgeID)
	}

	// Flip the row to cancelled and zero the enrichment counter in one
	// update so a late subtask FinalizeSubtask call can't race-promote
	// the row back to completed. Persisted partial data is left in
	// place — the user can reuse it on the next reparse attempt.
	now := time.Now()
	if err := s.repo.UpdateKnowledgeColumns(ctx, existing.ID, map[string]interface{}{
		"parse_status":           types.ParseStatusCancelled,
		"error_message":          "用户已取消解析",
		"pending_subtasks_count": 0,
		"updated_at":             now,
	}); err != nil {
		logger.Errorf(ctx, "CancelKnowledgeParse: failed to mark knowledge cancelled: %v", err)
		return nil, err
	}
	existing.ParseStatus = types.ParseStatusCancelled
	existing.ErrorMessage = "用户已取消解析"
	existing.PendingSubtasksCount = 0
	existing.UpdatedAt = now
	logger.Infof(ctx, "Knowledge %s marked as cancelled by user", knowledgeID)

	// Close the active attempt span tree so the UI stops showing "进行中"
	// for the cancelled run. AbortAttempt cascade-cancels every still-
	// running descendant (multimodal per-image, postprocess subtasks,
	// graph chunks) BEFORE closing the root, otherwise the trace
	// viewer would leave those striped/running bars hanging forever
	// because workers exit via their abort-guard without ever calling
	// FailSpan on their own subspan. Best-effort: nil tracker / missing
	// attempt no-ops.
	if attempt := s.tracker().LatestAttempt(ctx, knowledgeID); attempt > 0 {
		s.tracker().AbortAttempt(ctx, knowledgeID, attempt,
			"USER_CANCELLED", "用户已取消解析", "用户已取消解析")
	}

	// Best-effort dequeue. Failures here don't block the cancel — the
	// downstream tasks will still self-abort at their entry guards.
	s.dequeueKnowledgeTasks(ctx, knowledgeID)
	// Wiki ingest lives in its own per-KB pending queue (task_pending_ops)
	// rather than asynq, so dequeueKnowledgeTasks above can't see it.
	// Mirror the deletion path's scrub so a cancelled knowledge doesn't
	// get picked up by the next 30s batch and burn a wiki LLM call on a
	// doc the user already abandoned. The in-flight worker would skip it
	// at isWikiKnowledgeAborted anyway, but scrubbing avoids waking the
	// batch in the first place.
	s.scrubWikiPendingIngest(ctx, existing.KnowledgeBaseID, knowledgeID, "cancel")
	recordKBActivity(ctx, s.audit, tenantID, existing.KnowledgeBaseID, types.AuditActionKnowledgeParseCanceled,
		"knowledge", existing.ID, types.AuditOutcomeCanceled,
		map[string]any{"title": existing.Title, "type": existing.Type})
	return existing, nil
}

// dequeueKnowledgeTasks asks the task inspector to remove any queued
// tasks for this knowledge and signal active workers to stop. Safe to
// call when the inspector is a no-op (Lite mode).
func (s *knowledgeService) dequeueKnowledgeTasks(ctx context.Context, knowledgeID string) {
	if s.taskInspector == nil {
		return
	}
	if _, _, err := s.taskInspector.CancelTasksForKnowledge(ctx, knowledgeID); err != nil {
		logger.Warnf(ctx, "CancelKnowledgeParse: dequeue best-effort failed for %s: %v", knowledgeID, err)
	}
}

func (s *knowledgeService) updateChunkVector(ctx context.Context, kbID string, chunks []*types.Chunk) error {
	// Get embedding model from knowledge base
	sourceKB, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	if !sourceKB.NeedsEmbeddingModel() {
		return nil
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, sourceKB.EmbeddingModelID)
	if err != nil {
		return err
	}

	// Initialize composite retrieve engine from tenant configuration
	indexInfo := make([]*types.IndexInfo, 0, len(chunks))
	ids := make([]string, 0, len(chunks))
	knowledgeCache := make(map[string]*types.Knowledge)
	for _, chunk := range chunks {
		if chunk.KnowledgeBaseID != kbID {
			logger.Warnf(ctx, "Knowledge base ID mismatch: %s != %s", chunk.KnowledgeBaseID, kbID)
			continue
		}
		ids = append(ids, chunk.ID)
		if !chunk.IsEnabled || chunk.ChunkType == types.ChunkTypeParentText {
			continue
		}
		knowledge := knowledgeCache[chunk.KnowledgeID]
		if knowledge == nil {
			knowledge, err = s.repo.GetKnowledgeByID(ctx, chunk.TenantID, chunk.KnowledgeID)
			if err != nil {
				return err
			}
			knowledgeCache[chunk.KnowledgeID] = knowledge
		}
		indexInfo = append(indexInfo, &types.IndexInfo{
			Content:         buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent()),
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			KnowledgeType:   sourceKB.Type,
			IsEnabled:       chunk.IsEnabled,
		})
		meta, metaErr := chunk.DocumentMetadata()
		if metaErr != nil {
			return metaErr
		}
		if meta != nil {
			for _, q := range meta.GeneratedQuestions {
				if strings.TrimSpace(q.Question) != "" {
					indexInfo = append(indexInfo, &types.IndexInfo{
						Content: buildKnowledgeIndexContent(knowledge, q.Question), SourceID: types.GeneratedQuestionSourceID(chunk.ID, q.ID),
						SourceType: types.ChunkSourceType, ChunkID: chunk.ID,
						KnowledgeID: chunk.KnowledgeID, KnowledgeBaseID: chunk.KnowledgeBaseID,
						KnowledgeType: sourceKB.Type, IsEnabled: true,
					})
				}
			}
		}
	}

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, types.MustTenantIDFromContext(ctx), sourceKB.VectorStoreID)
	if err != nil {
		return err
	}

	// Delete old vector representation of the chunk
	err = retrieveEngine.DeleteByChunkIDList(ctx, ids, embeddingModel.GetDimensions(), sourceKB.Type)
	if err != nil {
		return err
	}

	// Index updated chunk content with new vector representation
	err = retrieveEngine.BatchIndex(ctx, embeddingModel, indexInfo)
	if err != nil {
		return err
	}
	return nil
}

func (s *knowledgeService) UpdateImageInfo(
	ctx context.Context,
	knowledgeID string,
	chunkID string,
	imageInfo string,
) error {
	var images []*types.ImageInfo
	if err := json.Unmarshal([]byte(imageInfo), &images); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal image info: %v", err)
		return err
	}
	if len(images) != 1 {
		logger.Warnf(ctx, "Expected exactly one image info, got %d", len(images))
		return nil
	}
	image := images[0]

	// Retrieve all chunks with the given parent chunk ID
	chunk, err := s.chunkService.GetChunkByID(ctx, chunkID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chunk: %v", err)
		return err
	}
	chunk.ImageInfo = imageInfo
	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	chunkChildren, err := s.chunkService.ListChunkByParentID(ctx, tenantID, chunkID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"parent_chunk_id": chunkID,
			"tenant_id":       tenantID,
		})
		return err
	}
	logger.Infof(ctx, "Found %d chunks with parent chunk ID: %s", len(chunkChildren), chunkID)

	// Iterate through each chunk and update its content based on the image information
	updateChunk := []*types.Chunk{chunk}
	var addChunk []*types.Chunk

	// Track whether we've found OCR and caption child chunks for this image
	hasOCRChunk := false
	hasCaptionChunk := false

	for i, child := range chunkChildren {
		// Skip chunks that are not image types
		var cImageInfo []*types.ImageInfo
		err = json.Unmarshal([]byte(child.ImageInfo), &cImageInfo)
		if err != nil {
			logger.Warnf(ctx, "Failed to unmarshal image %s info: %v", child.ID, err)
			continue
		}
		if len(cImageInfo) == 0 {
			continue
		}
		if cImageInfo[0].OriginalURL != image.OriginalURL {
			logger.Warnf(ctx, "Skipping chunk ID: %s, image URL mismatch: %s != %s",
				child.ID, cImageInfo[0].OriginalURL, image.OriginalURL)
			continue
		}

		// Mark that we've found chunks for this image
		switch child.ChunkType {
		case types.ChunkTypeImageCaption:
			hasCaptionChunk = true
			// Update caption if it has changed
			if image.Caption != cImageInfo[0].Caption {
				child.Content = image.Caption
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		case types.ChunkTypeImageOCR:
			hasOCRChunk = true
			// Update OCR if it has changed
			if image.OCRText != cImageInfo[0].OCRText {
				child.Content = image.OCRText
				child.ImageInfo = imageInfo
				updateChunk = append(updateChunk, chunkChildren[i])
			}
		}
	}

	// Create a new caption chunk if it doesn't exist and we have caption data
	if !hasCaptionChunk && image.Caption != "" {
		captionChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Content:         image.Caption,
			ChunkType:       types.ChunkTypeImageCaption,
			ParentChunkID:   chunk.ID,
			ImageInfo:       imageInfo,
		}
		addChunk = append(addChunk, captionChunk)
		logger.Infof(ctx, "Created new caption chunk ID: %s for image URL: %s", captionChunk.ID, image.OriginalURL)
	}

	// Create a new OCR chunk if it doesn't exist and we have OCR data
	if !hasOCRChunk && image.OCRText != "" {
		ocrChunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        tenantID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			Content:         image.OCRText,
			ChunkType:       types.ChunkTypeImageOCR,
			ParentChunkID:   chunk.ID,
			ImageInfo:       imageInfo,
		}
		addChunk = append(addChunk, ocrChunk)
		logger.Infof(ctx, "Created new OCR chunk ID: %s for image URL: %s", ocrChunk.ID, image.OriginalURL)
	}
	logger.Infof(ctx, "Updated %d chunks out of %d total chunks", len(updateChunk), len(chunkChildren)+1)

	if len(addChunk) > 0 {
		err := s.chunkService.CreateChunks(ctx, addChunk)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"add_chunk_size": len(addChunk),
			})
			return err
		}
	}

	// Update the chunks
	for _, c := range updateChunk {
		err := s.chunkService.UpdateChunk(ctx, c)
		if err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"chunk_id":     c.ID,
				"knowledge_id": c.KnowledgeID,
			})
			return err
		}
	}

	// Update the chunk vector
	err = s.updateChunkVector(ctx, chunk.KnowledgeBaseID, append(updateChunk, addChunk...))
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id":     chunk.ID,
			"knowledge_id": chunk.KnowledgeID,
		})
		return err
	}

	// Update the knowledge file hash
	knowledge, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return err
	}
	fileHash := calculateStr(knowledgeID, knowledge.FileHash, imageInfo)
	knowledge.FileHash = fileHash
	err = s.repo.UpdateKnowledge(ctx, knowledge)
	if err != nil {
		logger.Warnf(ctx, "Failed to update knowledge file hash: %v", err)
	}

	logger.Infof(ctx, "Updated chunk successfully, chunk ID: %s, knowledge ID: %s", chunk.ID, chunk.KnowledgeID)
	return nil
}

// ProcessManualUpdate handles Asynq manual knowledge update tasks.
// It performs cleanup of old indexes/chunks (when NeedCleanup is true) and re-indexes the content.
func (s *knowledgeService) ProcessManualUpdate(ctx context.Context, t *asynq.Task) error {
	var payload types.ManualProcessPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal manual process task payload: %v", err)
		return nil
	}

	ctx = logger.WithRequestID(ctx, payload.RequestId)
	ctx = logger.WithField(ctx, "manual_process", payload.KnowledgeID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to get tenant: %v", err)
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to get knowledge: %v", err)
		return nil
	}
	if knowledge == nil {
		logger.Warnf(ctx, "ProcessManualUpdate: knowledge not found: %s", payload.KnowledgeID)
		return nil
	}

	// Skip if already completed or being deleted
	if knowledge.ParseStatus == types.ParseStatusCompleted {
		logger.Infof(ctx, "ProcessManualUpdate: already completed, skipping: %s", payload.KnowledgeID)
		return nil
	}
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		logger.Infof(ctx, "ProcessManualUpdate: being deleted, skipping: %s", payload.KnowledgeID)
		return nil
	}
	if knowledge.ParseStatus == types.ParseStatusCancelled {
		logger.Infof(ctx, "ProcessManualUpdate: cancelled by user, skipping: %s", payload.KnowledgeID)
		return nil
	}

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to get knowledge base: %v", err)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = fmt.Sprintf("failed to get knowledge base: %v", err)
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// Re-check abort status right before marking processing — see the same
	// note in ProcessDocument for the cancel race this guards.
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "ProcessManualUpdate: knowledge aborted (%s), skipping: %s", status, knowledge.ID)
		return nil
	}
	// Update status to processing
	markKnowledgeProcessing(knowledge, time.Now())
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to update status to processing: %v", err)
		return nil
	}

	// Allocate a fresh span-tracking attempt for this manual (re)index.
	// Without it attemptFromCtx stays 0, so processChunks drops all stage
	// spans and KnowledgePostProcess falls back to LatestAttempt — piling
	// this run's summary/wiki subspans onto the previous attempt's trace.
	attempt := 0
	if root, n, err := s.tracker().OpenAttempt(ctx, knowledge.ID, payload.LangfuseTraceID); err == nil && root != nil {
		attempt = n
	} else if err != nil {
		logger.Warnf(ctx, "ProcessManualUpdate: OpenAttempt failed for %s: %v", knowledge.ID, err)
	}
	ctx = withAttempt(ctx, attempt)

	// Cleanup old resources (indexes, chunks, graph) for update operations
	if payload.NeedCleanup {
		if err := s.cleanupKnowledgeResources(ctx, knowledge); err != nil {
			logger.ErrorWithFields(ctx, err, map[string]interface{}{
				"knowledge_id": payload.KnowledgeID,
			})
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("failed to cleanup old resources: %v", err)
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}
	}

	// Run manual processing (image resolution + chunking + embedding) synchronously within the worker
	return s.triggerManualProcessing(ctx, kb, knowledge, payload.Content, true)
}

// ProcessDocument handles Asynq document processing tasks
func (s *knowledgeService) ProcessDocument(ctx context.Context, t *asynq.Task) error {
	var payload types.DocumentProcessPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "failed to unmarshal document process task payload: %v", err)
		return nil
	}

	ctx = logger.WithRequestID(ctx, payload.RequestId)
	ctx = logger.WithField(ctx, "document_process", payload.KnowledgeID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	// 获取任务重试信息，用于判断是否是最后一次重试
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)
	isLastRetry := retryCount >= maxRetry

	tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "failed to get tenant: %v", err)
		return nil
	}
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

	logger.Infof(ctx, "Processing document task: knowledge_id=%s, file_path=%s, retry=%d/%d",
		payload.KnowledgeID, payload.FilePath, retryCount, maxRetry)

	// 幂等性检查：获取knowledge记录
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge: %v", err)
		return nil
	}

	if knowledge == nil {
		return nil
	}

	// 检查是否正在删除 / 已被用户取消 - 如果是则直接退出
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		logger.Infof(ctx, "Knowledge is being deleted, aborting processing: %s", payload.KnowledgeID)
		return nil
	}
	if knowledge.ParseStatus == types.ParseStatusCancelled {
		logger.Infof(ctx, "Knowledge cancelled by user, aborting processing: %s", payload.KnowledgeID)
		return nil
	}

	// 检查任务状态 - 幂等性处理
	if knowledge.ParseStatus == types.ParseStatusCompleted {
		logger.Infof(ctx, "Document already completed, skipping: %s", payload.KnowledgeID)
		return nil // 幂等：已完成的任务直接返回
	}

	if knowledge.ParseStatus == types.ParseStatusFailed {
		// 检查是否可恢复（例如：超时、临时错误等）
		// 对于不可恢复的错误，直接返回
		logger.Warnf(
			ctx,
			"Document processing previously failed: %s, error: %s",
			payload.KnowledgeID,
			knowledge.ErrorMessage,
		)
		// 这里可以根据错误类型判断是否可恢复，暂时允许重试
	}

	// 检查是否有部分处理（有chunks但状态不是completed）
	if knowledge.ParseStatus != "completed" && knowledge.ParseStatus != "pending" &&
		knowledge.ParseStatus != "processing" {
		// 状态异常，记录日志但继续处理
		logger.Warnf(ctx, "Unexpected parse status: %s for knowledge: %s", knowledge.ParseStatus, payload.KnowledgeID)
	}

	// 获取知识库信息
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "failed to get knowledge base: %v", err)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = fmt.Sprintf("failed to get knowledge base: %v", err)
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	processOverrides, _ := knowledge.ProcessOverrides()
	eff := ResolveProcessConfig(kb, processOverrides)

	// Re-check abort status right before flipping to "processing" — closes
	// the race where the user cancels between the entry guard above and
	// this write (otherwise the worker would overwrite cancelled→processing
	// and downstream checkpoints would treat the run as live).
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		logger.Infof(ctx, "Knowledge aborted (%s) before marking processing: %s", status, knowledge.ID)
		return nil
	}
	markKnowledgeProcessing(knowledge, time.Now())
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "failed to update knowledge status to processing: %v", err)
		return nil
	}

	// Resolve the attempt for span tracking. The enqueue site sets
	// payload.Attempt to a fresh number for the initial parse and to
	// max+1 for each user-initiated reparse. Asynq retries within a
	// single user action keep the same payload (so retries record
	// onto the same attempt). For payloads predating this code we
	// fall back to OpenAttempt.
	attempt := payload.Attempt
	if attempt <= 0 {
		if root, n, err := s.tracker().OpenAttempt(ctx, knowledge.ID, payload.LangfuseTraceID); err == nil && root != nil {
			attempt = n
		}
	}
	ctx = withAttempt(ctx, attempt)

	// 检查多模态配置（仅对文件导入）
	if payload.FilePath != "" && !payload.EnableMultimodel && IsImageType(payload.FileType) {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			WithField("error", ErrImageNotParse).Errorf("processDocument image without enable multimodel")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = ErrImageNotParse.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// 检查音频ASR配置（仅对文件导入）
	if payload.FilePath != "" && IsAudioType(payload.FileType) && !eff.ASRConfig.IsASREnabled() {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			Errorf("processDocument audio without ASR model configured")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "上传音频文件需要设置ASR语音识别模型"
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// 视频文件不再支持入库解析
	if payload.FilePath != "" && IsVideoType(payload.FileType) {
		logger.GetLogger(ctx).WithField("knowledge_id", knowledge.ID).
			Errorf("processDocument video not supported")
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "暂不支持视频文件"
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// New pipeline: convert -> store images -> chunk -> vectorize -> multimodal tasks
	var convertResult *types.ReadResult
	var chunks []types.ParsedChunk

	if payload.FileURL != "" {
		// file_url import: SSRF re-check (防 DNS 重绑定), download, persist, then delegate to convert()
		if err := secutils.ValidateURLForSSRF(payload.FileURL); err != nil {
			logger.Errorf(ctx, "File URL rejected for SSRF protection in ProcessDocument: %s, err: %v", payload.FileURL, err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "File URL is not allowed for security reasons"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}

		resolvedFileName := payload.FileName
		resolvedFileType := payload.FileType
		contentBytes, err := downloadFileFromURL(ctx, payload.FileURL, &resolvedFileName, &resolvedFileType)
		if err != nil {
			logger.Errorf(ctx, "Failed to download file from URL: %s, error: %v", payload.FileURL, err)
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("failed to download file from URL: %w", err)
		}

		if resolvedFileType != "" && !isSupportedImportExtension(resolvedFileType) {
			logger.Errorf(ctx, "Unsupported file type resolved from file URL: %s", resolvedFileType)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("unsupported file type: %s", resolvedFileType)
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}

		if resolvedFileName != "" && knowledge.FileName == "" {
			knowledge.FileName = resolvedFileName
		}
		if resolvedFileType != "" && knowledge.FileType == "" {
			knowledge.FileType = resolvedFileType
			s.repo.UpdateKnowledge(ctx, knowledge)
		}

		fileSvc := s.resolveFileService(ctx, kb)
		filePath, err := fileSvc.SaveBytes(ctx, contentBytes, payload.TenantID, resolvedFileName, true)
		if err != nil {
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("failed to save downloaded file: %w", err)
		}

		payload.FilePath = filePath
		payload.FileName = resolvedFileName
		payload.FileType = resolvedFileType
		convertResult, err = s.convert(ctx, payload, kb, knowledge, eff, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
	} else if payload.URL != "" {
		// URL import
		convertResult, err = s.convert(ctx, payload, kb, knowledge, eff, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
		// Update knowledge title from extracted page title if not already set
		if knowledge.Title == "" || knowledge.Title == payload.URL {
			if extractedTitle := convertResult.Metadata["title"]; extractedTitle != "" {
				knowledge.Title = extractedTitle
				knowledge.UpdatedAt = time.Now()
				if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
					logger.Warnf(ctx, "Failed to update knowledge title from extracted page title: %v", err)
				} else {
					logger.Infof(ctx, "Updated knowledge title to extracted page title: %s", extractedTitle)
				}
			}
		}
	} else if len(payload.Passages) > 0 {
		// Text passage import - direct chunking, no conversion needed
		passageChunks := make([]types.ParsedChunk, 0, len(payload.Passages))
		start, end := 0, 0
		for i, p := range payload.Passages {
			if p == "" {
				continue
			}
			end += len([]rune(p))
			passageChunks = append(passageChunks, types.ParsedChunk{
				Content: p,
				Seq:     i,
				Start:   start,
				End:     end,
			})
			start = end
		}
		passageOpts := ProcessChunksOptions{
			EnableQuestionGeneration: payload.EnableQuestionGeneration,
			QuestionCount:            payload.QuestionCount,
		}
		return s.processChunks(ctx, kb, knowledge, passageChunks, passageOpts)
	} else {
		// File import
		convertResult, err = s.convert(ctx, payload, kb, knowledge, eff, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
	}

	// Step 1.5: ASR transcription for audio files
	if convertResult != nil && convertResult.IsAudio && len(convertResult.AudioData) > 0 {
		if !eff.ASRConfig.IsASREnabled() {
			logger.Error(ctx, "Audio file detected but ASR is not configured")
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "ASR model is not configured for audio transcription"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}

		logger.Infof(ctx, "[ASR] Starting audio transcription for knowledge %s, audio size=%d bytes",
			knowledge.ID, len(convertResult.AudioData))

		asrModel, err := s.modelService.GetASRModel(ctx, eff.ASRConfig.ModelID)
		if err != nil {
			logger.Errorf(ctx, "[ASR] Failed to get ASR model: %v", err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = fmt.Sprintf("failed to get ASR model: %v", err)
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}

		transcriptionResult, err := asrModel.Transcribe(ctx, convertResult.AudioData, knowledge.FileName)
		if err != nil {
			logger.Errorf(ctx, "[ASR] Transcription failed: %v", err)
			if isLastRetry {
				knowledge.ParseStatus = "failed"
				knowledge.ErrorMessage = fmt.Sprintf("audio transcription failed: %v", err)
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
			}
			return fmt.Errorf("audio transcription failed: %w", err)
		}

		var transcribedText string
		if transcriptionResult != nil {
			transcribedText = transcriptionResult.Text
		}

		if transcribedText == "" {
			logger.Warn(ctx, "[ASR] Transcription returned empty text")
			transcribedText = "[No speech detected in audio file]"
		}

		logger.Infof(ctx, "[ASR] Transcription completed, text length=%d", len(transcribedText))
		// Replace the audio placeholder with the transcribed text
		convertResult.MarkdownContent = transcribedText
		convertResult.IsAudio = false
		convertResult.AudioData = nil
	}

	// Step 2: Store images and update markdown references
	var storedImages []docparser.StoredImage

	if s.imageResolver != nil && convertResult != nil {
		fileSvc := s.resolveFileService(ctx, kb)
		tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
		updatedMarkdown, images, resolveErr := s.imageResolver.ResolveAndStore(ctx, convertResult, fileSvc, tenantID)
		if resolveErr != nil {
			logger.Warnf(ctx, "Image resolution partially failed: %v", resolveErr)
		}
		if updatedMarkdown != "" {
			convertResult.MarkdownContent = updatedMarkdown
		}
		storedImages = images

		// Resolve remote http(s) images (e.g. markdown external URLs) → download + upload to storage.
		// ResolveAndStore handles inline bytes and base64; ResolveRemoteImages handles http/https URLs.
		updatedContent, remoteImages, remoteErr := s.imageResolver.ResolveRemoteImages(ctx, convertResult.MarkdownContent, fileSvc, tenantID)
		if remoteErr != nil {
			logger.Warnf(ctx, "Remote image resolution partially failed: %v", remoteErr)
		}
		if len(remoteImages) > 0 {
			logger.Infof(ctx, "Resolved %d remote images for knowledge %s", len(remoteImages), knowledge.ID)
			convertResult.MarkdownContent = updatedContent
			storedImages = append(storedImages, remoteImages...)
		}

		logger.Infof(ctx, "Resolved %d total images for knowledge %s", len(storedImages), knowledge.ID)
	}

	// Step 3: Split into chunks using Go chunker. Browser textareas normalize
	// pasted content to LF, so normalize uploaded source text before calculating
	// chunk boundaries as well.
	convertResult.MarkdownContent = chunker.NormalizeLineEndings(convertResult.MarkdownContent)
	chunkCfg := buildSplitterConfigFromChunking(eff.ChunkingConfig)

	processOpts := ProcessChunksOptions{
		EnableQuestionGeneration: payload.EnableQuestionGeneration,
		QuestionCount:            payload.QuestionCount,
		EnableMultimodel:         payload.EnableMultimodel,
		StoredImages:             storedImages,
	}

	if convertResult != nil {
		processOpts.Metadata = convertResult.Metadata
	}

	if eff.ChunkingConfig.EnableParentChild {
		parentCfg, childCfg := buildParentChildConfigs(eff.ChunkingConfig, chunkCfg)
		pcResult := chunker.SplitParentChild(convertResult.MarkdownContent, parentCfg, childCfg)
		chunks = make([]types.ParsedChunk, len(pcResult.Children))
		for i, c := range pcResult.Children {
			chunks[i] = types.ParsedChunk{
				Content:       c.Content,
				ContextHeader: c.ContextHeader,
				Seq:           c.Seq,
				Start:         c.Start,
				End:           c.End,
				ParentIndex:   c.ParentIndex,
			}
		}
		parentChunks := make([]types.ParsedParentChunk, len(pcResult.Parents))
		for i, p := range pcResult.Parents {
			parentChunks[i] = types.ParsedParentChunk{Content: p.Content, Seq: p.Seq, Start: p.Start, End: p.End}
		}
		processOpts.ParentChunks = parentChunks
		logger.Infof(ctx, "Split document into %d parent + %d child chunks for knowledge %s",
			len(pcResult.Parents), len(pcResult.Children), knowledge.ID)
	} else {
		splitChunks := chunker.Split(convertResult.MarkdownContent, chunkCfg)
		chunks = make([]types.ParsedChunk, len(splitChunks))
		for i, c := range splitChunks {
			chunks[i] = types.ParsedChunk{
				Content:       c.Content,
				ContextHeader: c.ContextHeader,
				Seq:           c.Seq,
				Start:         c.Start,
				End:           c.End,
			}
		}
		logger.Infof(ctx, "Split document into %d chunks for knowledge %s", len(chunks), knowledge.ID)
	}

	// Step 4: Process chunks (vectorize + index + enqueue async tasks)
	return s.processChunks(ctx, kb, knowledge, chunks, processOpts)
}

// convert handles both file and URL reading using a unified ReadRequest.
func (s *knowledgeService) convert(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	eff types.EffectiveProcessConfig,
	isLastRetry bool,
) (*types.ReadResult, error) {
	// Stage tracking: docreader. Mark the stage as running here so the
	// timeline reflects "DocReader" the moment a worker picks the task
	// up — before that, the stage stays "pending" from the initial
	// upload. Failure/skip transitions are emitted at the specific
	// failure points below; success is emitted at the bottom.
	docInput := types.JSONMap{
		"file_name": payload.FileName,
		"file_type": payload.FileType,
		"is_url":    payload.URL != "",
	}
	if payload.URL != "" {
		docInput["url"] = payload.URL
	}
	s.beginStage(ctx, knowledge.ID, types.StageDocReader, docInput)
	isURL := payload.URL != ""
	fileType := payload.FileType
	tenantOverrides := s.getParserEngineOverridesFromContext(ctx)
	var uploadOverrides map[string]string
	if processOverrides, err := knowledge.ProcessOverrides(); err == nil && processOverrides != nil {
		uploadOverrides = processOverrides.ParserEngineOverrides
	}
	mergedOverrides := MergeParserEngineOverrides(tenantOverrides, uploadOverrides)
	applyParserRuleOverrides(mergedOverrides, eff.ChunkingConfig, fileType)
	if err := validateParserEngineOverrideURLs(mergedOverrides); err != nil {
		logger.Errorf(ctx, "Parser endpoint rejected for SSRF protection: %v", err)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "Parser endpoint is not allowed for security reasons"
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderParseFailed, knowledge.ErrorMessage, err)
		return nil, nil
	}

	if isURL {
		if err := secutils.ValidateURLForSSRF(payload.URL); err != nil {
			logger.Errorf(ctx, "URL rejected for SSRF protection: %s, err: %v", payload.URL, err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "URL is not allowed for security reasons"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			s.failStage(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, "URL rejected for security reasons", err)
			return nil, nil
		}
	}

	parserEngine := eff.ChunkingConfig.ResolveParserEngine(fileType)
	if isURL {
		parserEngine = eff.ChunkingConfig.ResolveParserEngine("url")
	}

	logger.Infof(ctx, "[convert] kb=%s fileType=%s isURL=%v engine=%q rules=%+v",
		kb.ID, fileType, isURL, parserEngine, eff.ChunkingConfig.ParserEngineRules)

	var reader interfaces.DocReader = s.resolveDocReader(ctx, parserEngine, fileType, isURL, mergedOverrides)
	if reader == nil {
		logger.Errorf(ctx, "[convert] no doc reader for kb=%s knowledge=%s fileType=%s engine=%q isURL=%v",
			kb.ID, knowledge.ID, fileType, parserEngine, isURL)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "Document parsing service is not configured. Please use text/paragraph import or set DOCREADER_ADDR."
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderUnavailable, knowledge.ErrorMessage, nil)
		return nil, nil
	}

	req := &types.ReadRequest{
		URL:                   payload.URL,
		Title:                 knowledge.Title,
		ParserEngine:          parserEngine,
		RequestID:             payload.RequestId,
		ParserEngineOverrides: mergedOverrides,
	}

	if !isURL {
		fileReader, err := s.resolveFileServiceForPath(ctx, kb, payload.FilePath).GetFile(ctx, payload.FilePath)
		if err != nil {
			s.failStage(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, "failed to get file", err)
			return s.failKnowledge(ctx, knowledge, isLastRetry, "failed to get file: %v", err)
		}
		defer fileReader.Close()
		contentBytes, err := io.ReadAll(fileReader)
		if err != nil {
			s.failStage(ctx, knowledge.ID, types.StageDocReader,
				werrors.ErrCodeDocReaderParseFailed, "failed to read file", err)
			return s.failKnowledge(ctx, knowledge, isLastRetry, "failed to read file: %v", err)
		}
		req.FileContent = contentBytes
		req.FileName = payload.FileName
		req.FileType = fileType
	}

	result, err := s.callDocReaderWithTimeout(ctx, reader, req)
	if err != nil {
		// Distinguish DocReader timeout (a knowable user-facing
		// failure) from generic read errors so the UI can suggest
		// "split this large file" specifically when relevant.
		code := werrors.ErrCodeDocReaderParseFailed
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "docreader call timeout") {
			code = werrors.ErrCodeDocReaderTimeout
		}
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			code, "document read failed", err)
		return s.failKnowledge(ctx, knowledge, isLastRetry, "document read failed: %v", err)
	}
	if result.Error != "" {
		logger.Errorf(ctx, "[convert] parser returned error kb=%s knowledge=%s file=%q type=%s engine=%q: %s",
			kb.ID, knowledge.ID, req.FileName, fileType, parserEngine, result.Error)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = result.Error
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		s.failStage(ctx, knowledge.ID, types.StageDocReader,
			werrors.ErrCodeDocReaderParseFailed, result.Error, nil)
		return nil, nil
	}
	docOutput := types.JSONMap{
		"text_length":  len(result.MarkdownContent),
		"images_found": len(result.ImageRefs),
		"is_audio":     result.IsAudio,
	}
	if pages := result.Metadata["pages"]; pages != "" {
		docOutput["pages"] = pages
	}
	s.endStage(ctx, knowledge.ID, types.StageDocReader, docOutput)
	return result, nil
}

// callDocReaderWithTimeout wraps the DocReader RPC in a child context whose
// deadline is min(parent_deadline, DocReaderCallTimeout). Without this cap,
// a hung docreader (network partition, GC pause, OCR runaway) silently
// burns the whole DocumentProcessTimeout budget and pins a worker for hours
// — the #1 cause of "knowledge stuck in processing" reports.
//
// On timeout we annotate the error so retries / dead-letter consumers can
// distinguish "docreader was slow" from "docreader returned an error".
func (s *knowledgeService) callDocReaderWithTimeout(
	ctx context.Context, reader interfaces.DocReader, req *types.ReadRequest,
) (*types.ReadResult, error) {
	timeout := 30 * time.Minute
	if s.config != nil && s.config.KnowledgeBase != nil && s.config.KnowledgeBase.DocReaderCallTimeout > 0 {
		timeout = s.config.KnowledgeBase.DocReaderCallTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	result, err := reader.Read(callCtx, req)
	elapsed := time.Since(start)
	if err != nil {
		// Promote DeadlineExceeded into a clearer message; retain underlying
		// error via %w so errors.Is(callCtx.Err(), context.DeadlineExceeded)
		// still works for upstream classification.
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
			logger.Errorf(ctx, "[convert] docreader call timed out after %s (limit %s) for %q",
				elapsed, timeout, req.FileName)
			return nil, fmt.Errorf("docreader call timeout after %s: %w", timeout, err)
		}
		return nil, err
	}
	logger.Infof(ctx, "[convert] docreader call ok in %s for %q", elapsed, req.FileName)
	return result, nil
}

// isLikelyRateLimitError performs a fuzzy classification of an error as a
// rate-limit / quota / backpressure failure. We only need a hint — the
// caller maps to one of two error_codes so the UI can offer "retry later"
// vs. "fix configuration" advice. False positives are harmless (the
// detail is preserved in error_detail anyway).
func isLikelyRateLimitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{"rate limit", "ratelimit", "429", "too many requests", "quota"} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// resolveDocReader picks the reader for one parse request. The engine catalog
// itself lives in the docparser registry; this only supplies the dependencies
// the service owns. Returns nil when the chosen engine cannot run — an
// unconfigured cloud engine, a disconnected docreader — after logging why.
func (s *knowledgeService) resolveDocReader(
	ctx context.Context, engine, fileType string, isURL bool, overrides map[string]string,
) interfaces.DocReader {
	reader, err := docparser.NewReader(ctx, engine, fileType, isURL, docparser.ReaderDeps{
		Overrides:               overrides,
		Remote:                  s.documentReader,
		WeKnoraCloudCredentials: s.tenantService.GetWeKnoraCloudCredentials,
	})
	if err != nil {
		logger.Warnf(ctx, "[resolveDocReader] engine=%q fileType=%q unusable: %v", engine, fileType, err)
		return nil
	}
	return reader
}

// failKnowledge marks knowledge as failed (only on last retry) and returns an error.
func (s *knowledgeService) failKnowledge(
	ctx context.Context,
	knowledge *types.Knowledge,
	isLastRetry bool,
	format string,
	args ...interface{},
) (*types.ReadResult, error) {
	errMsg := fmt.Sprintf(format, args...)
	if isLastRetry {
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = errMsg
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
	}
	return nil, fmt.Errorf(format, args...)
}

// enqueueImageMultimodalTasks enqueues asynq tasks for multimodal image processing.
func (s *knowledgeService) enqueueImageMultimodalTasks(
	ctx context.Context,
	knowledge *types.Knowledge,
	kb *types.KnowledgeBase,
	images []docparser.StoredImage,
	chunks []types.ParsedChunk,
	metadata map[string]string,
) {
	if s.task == nil || len(images) == 0 {
		return
	}

	attempt := attemptFromCtx(ctx)
	redisKey := fmt.Sprintf("multimodal:pending:%s", knowledge.ID)
	if s.redisClient != nil {
		if err := s.redisClient.Set(ctx, redisKey, len(images), 24*time.Hour).Err(); err != nil {
			logger.Warnf(ctx, "Failed to set multimodal pending count for %s: %v", knowledge.ID, err)
		}
	}

	for idx, img := range images {
		// Match image to the ParsedChunk whose content contains the image URL.
		// ChunkID was populated by processChunks with the real DB UUID.
		chunkID := ""
		for _, c := range chunks {
			if strings.Contains(c.Content, img.ServingURL) {
				chunkID = c.ChunkID
				break
			}
		}
		if chunkID == "" && len(chunks) > 0 {
			chunkID = chunks[0].ChunkID
		}

		lang := types.LanguageFromContextOrDefault(ctx)
		payload := types.ImageMultimodalPayload{
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: kb.ID,
			ChunkID:         chunkID,
			ImageURL:        img.ServingURL,
			EnableOCR:       true,
			EnableCaption:   true,
			Language:        lang,
			ImageSourceType: metadata["image_source_type"],
			Attempt:         attempt,
			ImageIndex:      idx,
		}

		langfuse.InjectTracing(ctx, &payload)
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			logger.Warnf(ctx, "Failed to marshal image multimodal payload: %v", err)
			continue
		}

		task := asynq.NewTask(types.TypeImageMultimodal, payloadBytes,
			asynq.Queue(types.QueueMultimodal), asynq.MaxRetry(3), asynq.Timeout(30*time.Minute))
		if _, err := s.task.Enqueue(task); err != nil {
			logger.Warnf(ctx, "Failed to enqueue image multimodal task for %s: %v", img.ServingURL, err)
		} else {
			logger.Infof(ctx, "Enqueued image:multimodal task for %s", img.ServingURL)
		}
	}
}

// ProcessKnowledgeListReparse handles Asynq knowledge list reparse tasks.
func (s *knowledgeService) ProcessKnowledgeListReparse(ctx context.Context, t *asynq.Task) error {
	var payload types.KnowledgeListReparsePayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		logger.Errorf(ctx, "Failed to unmarshal knowledge list reparse payload: %v", err)
		return err
	}
	ctx = payload.Initiator.Apply(ctx)
	taskID, _ := asynq.GetTaskID(ctx)
	ctx = withKBActivityTask(ctx, taskID, kbActivityTrigger(ctx))

	logger.Infof(ctx, "Processing knowledge list reparse task for %d knowledge items", len(payload.KnowledgeIDs))

	tenant, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get tenant %d: %v", payload.TenantID, err)
		return err
	}

	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)

	outcome, err := runKnowledgeListReparseSubmissions(payload.KnowledgeIDs, func(id string) error {
		_, err := s.ReparseKnowledge(ctx, id, payload.ProcessConfig)
		return err
	})
	logger.Infof(ctx, "Knowledge list reparse task finished: %d submitted, %d failed",
		outcome.Submitted, outcome.Failed)
	return err
}

type knowledgeListReparseOutcome struct {
	Submitted int
	Failed    int
}

// runKnowledgeListReparseSubmissions attempts every item so one bad document
// cannot block the remainder of the batch. A partial failure is non-retryable:
// retrying the wrapper task would destructively reparse items that were already
// submitted successfully. Failed rows remain selectable for an explicit retry.
func runKnowledgeListReparseSubmissions(
	ids []string,
	submit func(string) error,
) (knowledgeListReparseOutcome, error) {
	var outcome knowledgeListReparseOutcome
	failures := make([]error, 0)
	for _, id := range ids {
		if err := submit(id); err != nil {
			failures = append(failures, fmt.Errorf("knowledge %s: %w", secutils.SanitizeForLog(id), err))
			outcome.Failed++
			continue
		}
		outcome.Submitted++
	}
	if len(failures) == 0 {
		return outcome, nil
	}
	return outcome, fmt.Errorf(
		"%w: batch reparse submitted %d item(s) and failed %d: %w",
		asynq.SkipRetry, outcome.Submitted, outcome.Failed, errors.Join(failures...),
	)
}
