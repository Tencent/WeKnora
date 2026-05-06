package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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
	"github.com/Tencent/WeKnora/internal/tracing"
	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel/attribute"
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
		FileType:         src.FileType,
		FileSize:         src.FileSize,
		FileHash:         src.FileHash,
		FilePath:         src.FilePath,
		StorageSize:      src.StorageSize,
		Metadata:         src.Metadata,
	}
	defer func() {
		if err != nil {
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
	s.processChunks(ctx, kb, knowledge, chunks, opts)
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

// buildSplitterConfig creates a SplitterConfig with fallbacks from a KnowledgeBase.
func buildSplitterConfig(kb *types.KnowledgeBase) chunker.SplitterConfig {
	chunkCfg := chunker.SplitterConfig{
		ChunkSize:    kb.ChunkingConfig.ChunkSize,
		ChunkOverlap: kb.ChunkingConfig.ChunkOverlap,
		Separators:   kb.ChunkingConfig.Separators,
	}
	if chunkCfg.ChunkSize <= 0 {
		chunkCfg.ChunkSize = 512
	}
	if chunkCfg.ChunkOverlap <= 0 {
		chunkCfg.ChunkOverlap = 50
	}
	if len(chunkCfg.Separators) == 0 {
		chunkCfg.Separators = []string{"\n\n", "\n", "。"}
	}
	return chunkCfg
}

// buildParentChildConfigs derives parent and child SplitterConfig from ChunkingConfig.
// The base config (already validated with defaults) is used for separators.
func buildParentChildConfigs(cc types.ChunkingConfig, base chunker.SplitterConfig) (parent, child chunker.SplitterConfig) {
	parentSize := cc.ParentChunkSize
	if parentSize <= 0 {
		parentSize = 4096
	}
	childSize := cc.ChildChunkSize
	if childSize <= 0 {
		childSize = 384
	}
	parent = chunker.SplitterConfig{
		ChunkSize:    parentSize,
		ChunkOverlap: base.ChunkOverlap, // reuse configured overlap for parents
		Separators:   base.Separators,
	}
	child = chunker.SplitterConfig{
		ChunkSize:    childSize,
		ChunkOverlap: childSize / 5, // ~20% overlap for child chunks
		Separators:   base.Separators,
	}
	return
}

// processChunks processes chunks and creates embeddings for knowledge content
func (s *knowledgeService) processChunks(ctx context.Context,
	kb *types.KnowledgeBase, knowledge *types.Knowledge, chunks []types.ParsedChunk,
	opts ...ProcessChunksOptions,
) {
	// Get options
	var options ProcessChunksOptions
	if len(opts) > 0 {
		options = opts[0]
	}

	ctx, span := tracing.ContextWithSpan(ctx, "knowledgeService.processChunks")
	defer span.End()
	span.SetAttributes(
		attribute.Int("tenant_id", int(knowledge.TenantID)),
		attribute.String("knowledge_base_id", knowledge.KnowledgeBaseID),
		attribute.String("knowledge_id", knowledge.ID),
		attribute.String("embedding_model_id", kb.EmbeddingModelID),
		attribute.Int("chunk_count", len(chunks)),
	)

	// Check if knowledge is being deleted before processing
	if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
		logger.Infof(ctx, "Knowledge is being deleted, aborting chunk processing: %s", knowledge.ID)
		span.AddEvent("aborted: knowledge is being deleted")
		return
	}

	// Get embedding model for vectorization — only needed when vector/keyword indexing is enabled
	var embeddingModel embedding.Embedder
	if kb.NeedsEmbeddingModel() {
		var err error
		embeddingModel, err = s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks get embedding model failed")
			span.RecordError(err)
			return
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
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
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

	// 仅为文本类型的Chunk设置前后关系（child chunks only, parents already linked above）
	textChunks := make([]*types.Chunk, 0, len(chunks))
	for _, chunk := range insertChunks {
		if chunk.ChunkType == types.ChunkTypeText && chunk.ParentChunkID != "" {
			// This is a child chunk in parent-child mode
			textChunks = append(textChunks, chunk)
		} else if chunk.ChunkType == types.ChunkTypeText && !hasParentChild {
			// Normal flat chunk (no parent-child mode)
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

	// Check if knowledge is being deleted before writing to database
	if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
		logger.Infof(ctx, "Knowledge is being deleted, aborting before saving chunks: %s", knowledge.ID)
		span.AddEvent("aborted: knowledge is being deleted before saving")
		return
	}

	// Save chunks to database — ALWAYS, regardless of indexing strategy.
	// Chunks are needed for wiki generation, graph extraction, and summary generation
	// even when vector/keyword indexing is disabled.
	span.AddEvent("create chunks")
	if err := s.chunkService.CreateChunks(ctx, insertChunks); err != nil {
		knowledge.ParseStatus = types.ParseStatusFailed
		knowledge.ErrorMessage = err.Error()
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		span.RecordError(err)
		return
	}

	// Create index information and perform vector indexing — only when vector/keyword is enabled.
	// Chunks are ALWAYS saved to DB (above) because wiki and graph need them even without vector indexing.
	var totalStorageSize int64
	if kb.NeedsEmbeddingModel() && embeddingModel != nil {
		// Create index information — only for child/flat chunks, NOT parent chunks.
		// Parent chunks are stored for context retrieval but do not need vector embeddings.
		// Prepend the document title to improve semantic alignment between
		// question-style queries and statement-style chunk content.
		indexInfoList := make([]*types.IndexInfo, 0, len(textChunks))
		titlePrefix := ""
		if t := strings.TrimSpace(knowledge.Title); t != "" {
			titlePrefix = t + "\n"
		}
		for _, chunk := range textChunks {
			indexContent := titlePrefix + chunk.Content
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
		span.AddEvent("estimate storage size")
		totalStorageSize = retrieveEngine.EstimateStorageSize(ctx, embeddingModel, indexInfoList)
		if tenantInfo.StorageQuota > 0 {
			// Re-fetch tenant storage information
			tenantInfo, err = s.tenantRepo.GetTenantByID(ctx, tenantInfo.ID)
			if err != nil {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = err.Error()
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
				span.RecordError(err)
				return
			}
			// Check if there's enough storage quota available
			if tenantInfo.StorageUsed+totalStorageSize > tenantInfo.StorageQuota {
				knowledge.ParseStatus = types.ParseStatusFailed
				knowledge.ErrorMessage = "存储空间不足"
				knowledge.UpdatedAt = time.Now()
				s.repo.UpdateKnowledge(ctx, knowledge)
				span.RecordError(errors.New("storage quota exceeded"))
				return
			}
		}

		// Check again before batch indexing (this is a heavy operation)
		if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
			logger.Infof(ctx, "Knowledge is being deleted, cleaning up and aborting before indexing: %s", knowledge.ID)
			// Clean up the chunks we just created
			if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
				logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
			}
			span.AddEvent("aborted: knowledge is being deleted before indexing")
			return
		}

		span.AddEvent("batch index")
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
			span.RecordError(err)
			return
		}
		logger.GetLogger(ctx).Infof("processChunks batch index successfully, with %d index", len(indexInfoList))

		// Final check before marking as completed - if deleted during processing, don't update status
		if s.isKnowledgeDeleting(ctx, knowledge.TenantID, knowledge.ID) {
			logger.Infof(ctx, "Knowledge was deleted during processing, skipping completion update: %s", knowledge.ID)
			// Clean up the data we just created since the knowledge is being deleted
			if err := s.chunkService.DeleteChunksByKnowledgeID(ctx, knowledge.ID); err != nil {
				logger.Warnf(ctx, "Failed to cleanup chunks after deletion detected: %v", err)
			}
			if err := retrieveEngine.DeleteByKnowledgeIDList(ctx, []string{knowledge.ID}, embeddingModel.GetDimensions(), kb.Type); err != nil {
				logger.Warnf(ctx, "Failed to cleanup index after deletion detected: %v", err)
			}
			span.AddEvent("aborted: knowledge was deleted during processing")
			return
		}
	} else {
		logger.Infof(ctx, "Vector/keyword indexing disabled for KB %s, skipping BatchIndex", kb.ID)
	}

	// Check if this document has extracted images that will be processed asynchronously
	isImage := IsImageType(knowledge.FileType)
	isVideo := IsVideoType(knowledge.FileType)
	pendingMultimodal := isImage && options.EnableMultimodel && len(options.StoredImages) > 0
	pendingPDFMultimodal := !isImage && !isVideo && options.EnableMultimodel && len(options.StoredImages) > 0

	// For image files or documents with pending multimodal processing, keep "processing" status
	if pendingMultimodal || pendingPDFMultimodal {
		knowledge.ParseStatus = types.ParseStatusProcessing
	}
	knowledge.EnableStatus = "enabled"
	knowledge.StorageSize = totalStorageSize
	now := time.Now()
	knowledge.ProcessedAt = &now
	knowledge.UpdatedAt = now

	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.GetLogger(ctx).WithField("error", err).Errorf("processChunks update knowledge failed")
	}

	// Enqueue multimodal tasks for images (async, non-blocking)
	if options.EnableMultimodel && len(options.StoredImages) > 0 {
		s.enqueueImageMultimodalTasks(ctx, knowledge, kb, options.StoredImages, chunks, options.Metadata)
	} else {
		// If there are no multimodal tasks, enqueue the post process task immediately
		lang, _ := types.LanguageFromContext(ctx)
		postProcessPayload := types.KnowledgePostProcessPayload{
			TenantID:        knowledge.TenantID,
			KnowledgeID:     knowledge.ID,
			KnowledgeBaseID: knowledge.KnowledgeBaseID,
			Language:        lang,
		}
		langfuse.InjectTracing(ctx, &postProcessPayload)
		payloadBytes, err := json.Marshal(postProcessPayload)
		if err == nil {
			task := asynq.NewTask(types.TypeKnowledgePostProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
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
}

// defaultMaxInputChars is the default maximum characters used as input for summary generation.
const defaultMaxInputChars = 1024 * 24

// errInsufficientSummaryContent signals that getSummary refused to call the
// LLM because the document had no usable text after image markup was stripped
// (typical for scanned PDFs where VLM OCR yielded nothing). Callers should
// mark the knowledge's summary as failed instead of falling back to the first
// chunk's raw content (which would just be a bare image reference).
var errInsufficientSummaryContent = errors.New("insufficient text content for summary generation")

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

	// Sort chunks by StartAt for proper concatenation
	sortedChunks := make([]*types.Chunk, len(chunks))
	copy(sortedChunks, chunks)
	sort.Slice(sortedChunks, func(i, j int) bool {
		return sortedChunks[i].StartAt < sortedChunks[j].StartAt
	})

	// Concatenate original chunk contents by StartAt offset to reconstruct the
	// document, then enrich with image info in a second pass. Enrichment must
	// happen AFTER concatenation because StartAt is based on original document
	// offsets — enriched (longer) content would break the positioning.
	chunkContents := ""
	for _, chunk := range sortedChunks {
		runes := []rune(chunkContents)
		if chunk.StartAt <= len(runes) {
			chunkContents = string(runes[:chunk.StartAt]) + chunk.Content
		} else {
			chunkContents = chunkContents + chunk.Content
		}
	}

	// Collect image_info from image_ocr/image_caption children and enrich
	chunkIDs := make([]string, len(sortedChunks))
	for i, c := range sortedChunks {
		chunkIDs[i] = c.ID
	}
	imageInfoMap := searchutil.CollectImageInfoByChunkIDs(ctx, s.chunkRepo, knowledge.TenantID, chunkIDs)
	mergedImageInfo := searchutil.MergeImageInfoJSON(imageInfoMap)
	if mergedImageInfo != "" {
		chunkContents = searchutil.EnrichContentCaptionOnly(chunkContents, mergedImageInfo)
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

	// Pass the raw chunk text to the LLM with no filename / file-type framing.
	contentWithMetadata := chunkContents

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
	summary, err := summaryModel.Chat(ctx, []chat.Message{
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
	logger.GetLogger(ctx).WithField("summary", summary.Content).Infof("GetSummary success")
	return summary.Content, nil
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
func (s *knowledgeService) ProcessSummaryGeneration(ctx context.Context, t *asynq.Task) error {
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

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil
	}

	if kb.SummaryModelID == "" {
		logger.Warn(ctx, "Knowledge base summary model ID is empty, skipping summary generation")
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return nil
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
		return nil
	}

	// Filter text chunks only
	textChunks := make([]*types.Chunk, 0)
	for _, chunk := range chunks {
		if chunk.ChunkType == types.ChunkTypeText {
			textChunks = append(textChunks, chunk)
		}
	}

	if len(textChunks) == 0 {
		logger.Infof(ctx, "No text chunks found for knowledge: %s", payload.KnowledgeID)
		// Mark as completed since there's nothing to summarize
		knowledge.SummaryStatus = types.SummaryStatusCompleted
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// Sort chunks by ChunkIndex for proper ordering
	sort.Slice(textChunks, func(i, j int) bool {
		return textChunks[i].ChunkIndex < textChunks[j].ChunkIndex
	})

	// Initialize chat model for summary
	chatModel, err := s.modelService.GetChatModel(ctx, kb.SummaryModelID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get chat model: %v", err)
		markSummaryFailed()
		return fmt.Errorf("failed to get chat model: %w", err)
	}

	// Generate summary
	summary, err := s.getSummary(ctx, chatModel, knowledge, textChunks)
	if err != nil {
		logger.Errorf(ctx, "Failed to generate summary for knowledge %s: %v", payload.KnowledgeID, err)
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
				return fmt.Errorf("failed to update knowledge: %w", updateErr)
			}
			return nil
		}
		// For other errors (LLM API issues etc.), fall back to the first chunk.
		if len(textChunks) > 0 {
			summary = textChunks[0].Content
			if len(summary) > 500 {
				runes := []rune(summary)
				if len(runes) > 500 {
					summary = string(runes[:500])
				}
			}
		}
	}

	// Update knowledge description
	knowledge.Description = summary
	knowledge.SummaryStatus = types.SummaryStatusCompleted
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge description: %v", err)
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
			return fmt.Errorf("failed to create summary chunk: %w", err)
		}

		// Index summary chunk
		tenantInfo, err := s.tenantRepo.GetTenantByID(ctx, payload.TenantID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get tenant info: %v", err)
			return fmt.Errorf("failed to get tenant info: %w", err)
		}
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenantInfo)

		retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
		if err != nil {
			logger.Errorf(ctx, "Failed to init retrieve engine: %v", err)
			return fmt.Errorf("failed to init retrieve engine: %w", err)
		}

		embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
		if err != nil {
			logger.Errorf(ctx, "Failed to get embedding model: %v", err)
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
			return fmt.Errorf("failed to index summary chunk: %w", err)
		}

		logger.Infof(ctx, "Successfully created and indexed summary chunk for knowledge: %s", payload.KnowledgeID)
	}

	logger.Infof(ctx, "Successfully generated summary for knowledge: %s", payload.KnowledgeID)
	return nil
}

// ProcessQuestionGeneration handles async question generation task
func (s *knowledgeService) ProcessQuestionGeneration(ctx context.Context, t *asynq.Task) error {
	taskStartedAt := time.Now()
	retryCount, _ := asynq.GetRetryCount(ctx)
	maxRetry, _ := asynq.GetMaxRetry(ctx)

	var payload types.QuestionGenerationPayload
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
	}()

	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		exitStatus = "invalid_payload"
		logger.Errorf(ctx, "Failed to unmarshal question generation payload: %v", err)
		return nil // Don't retry on unmarshal error
	}

	logger.Infof(ctx, "Processing question generation for knowledge: %s", payload.KnowledgeID)

	// Set tenant context
	ctx = context.WithValue(ctx, types.TenantIDContextKey, payload.TenantID)
	if payload.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, payload.Language)
	}

	if strings.TrimSpace(s.config.Conversation.GenerateQuestionsPrompt) == "" {
		exitStatus = "prompt_not_configured"
		logger.Errorf(ctx, "GenerateQuestionsPrompt is empty: configure conversation.generate_questions_prompt_id")
		return fmt.Errorf("generate questions prompt not configured")
	}

	// Get knowledge base
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		exitStatus = "kb_not_found"
		logger.Errorf(ctx, "Failed to get knowledge base: %v", err)
		return nil
	}

	// Get knowledge
	knowledge, err := s.repo.GetKnowledgeByID(ctx, payload.TenantID, payload.KnowledgeID)
	if err != nil {
		exitStatus = "knowledge_not_found"
		logger.Errorf(ctx, "Failed to get knowledge: %v", err)
		return nil
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

	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
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

		llmCallAttempts++
		questions, err := s.generateQuestionsWithContext(ctx, chatModel, enrichContent(chunk), prevContent, nextContent, knowledge.Title, questionCount)
		if err != nil {
			llmCallFailed++
			logger.Warnf(ctx, "Failed to generate questions for chunk %s: %v", chunk.ID, err)
			continue
		}

		if len(questions) == 0 {
			llmCallEmpty++
			continue
		}
		llmCallSuccess++
		generatedQuestionsTotal += len(questions)

		// Update chunk metadata with unique IDs for each question
		generatedQuestions := make([]types.GeneratedQuestion, len(questions))
		for j, question := range questions {
			questionID := fmt.Sprintf("q%d", time.Now().UnixNano()+int64(j))
			generatedQuestions[j] = types.GeneratedQuestion{
				ID:       questionID,
				Question: question,
			}
		}
		meta := &types.DocumentChunkMetadata{
			GeneratedQuestions: generatedQuestions,
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
			sourceID := fmt.Sprintf("%s-%s", chunk.ID, gq.ID)
			indexInfoList = append(indexInfoList, &types.IndexInfo{
				Content:         gq.Question,
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

// generateQuestionsWithContext generates questions for a chunk with surrounding context
func (s *knowledgeService) generateQuestionsWithContext(ctx context.Context,
	chatModel chat.Chat, content, prevContent, nextContent, docName string, questionCount int,
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

	thinking := false
	response, err := chatModel.Chat(ctx, []chat.Message{
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

// ReparseKnowledge deletes existing document content and re-parses the knowledge asynchronously.
// This method reuses the logic from UpdateManualKnowledge for resource cleanup and async parsing.
func (s *knowledgeService) ReparseKnowledge(ctx context.Context, knowledgeID string) (*types.Knowledge, error) {
	logger.Info(ctx, "Start re-parsing knowledge")

	tenantID := ctx.Value(types.TenantIDContextKey).(uint64)
	existing, err := s.repo.GetKnowledgeByID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.Errorf(ctx, "Failed to load knowledge: %v", err)
		return nil, err
	}

	// Get knowledge base configuration
	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, existing.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "Failed to get knowledge base for reparse: %v", err)
		return nil, err
	}

	// Keep wiki's pending queue consistent across both manual and non-manual
	// paths. The destructive work (swapping old wiki contributions for new)
	// happens asynchronously inside mapOneDocument — see its oldPageSlugs
	// handling — once post-process re-enqueues wiki ingest. All we need to
	// do here is stop any stale pending ingest op from firing against the
	// pre-reparse chunk set.
	if kb != nil && kb.IsWikiEnabled() {
		s.prepareWikiForReparse(ctx, existing)
	}

	// For manual knowledge, use async manual processing (cleanup + re-indexing in worker)
	if existing.IsManual() {
		meta, metaErr := existing.ManualMetadata()
		if metaErr != nil || meta == nil {
			logger.Errorf(ctx, "Failed to get manual metadata for reparse: %v", metaErr)
			return nil, werrors.NewBadRequestError("无法获取手工知识内容")
		}

		existing.ParseStatus = "pending"
		existing.EnableStatus = "disabled"
		existing.Description = ""
		existing.ProcessedAt = nil
		existing.EmbeddingModelID = kb.EmbeddingModelID

		if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
			logger.Errorf(ctx, "Failed to update knowledge status before reparse: %v", err)
			return nil, err
		}

		if err := s.enqueueManualProcessing(ctx, existing, meta.Content, true); err != nil {
			logger.Errorf(ctx, "Failed to enqueue manual reparse task: %v", err)
			existing.ParseStatus = "failed"
			existing.ErrorMessage = "Failed to enqueue processing task"
			s.repo.UpdateKnowledge(ctx, existing)
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
	existing.ParseStatus = "pending"
	existing.EnableStatus = "disabled"
	existing.Description = ""
	existing.ProcessedAt = nil
	existing.EmbeddingModelID = kb.EmbeddingModelID

	if err := s.repo.UpdateKnowledge(ctx, existing); err != nil {
		logger.Errorf(ctx, "Failed to update knowledge status before reparse: %v", err)
		return nil, err
	}

	// Step 3: Trigger async re-parsing based on knowledge type
	logger.Infof(ctx, "Knowledge status updated, scheduling async reparse, ID: %s, Type: %s", existing.ID, existing.Type)

	// For file-based knowledge, enqueue document processing task
	if existing.FilePath != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		// Determine multimodal setting
		enableMultimodel := kb.IsMultimodalEnabled()

		// Check question generation config
		enableQuestionGeneration := false
		questionCount := 3 // default
		if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
			enableQuestionGeneration = true
			if kb.QuestionGenerationConfig.QuestionCount > 0 {
				questionCount = kb.QuestionGenerationConfig.QuestionCount
			}
		}

		lang, _ := types.LanguageFromContext(ctx)
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
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal reparse task payload: %v", err)
			return existing, nil
		}

		task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue reparse task: %v", err)
			return existing, nil
		}
		logger.Infof(ctx, "Enqueued reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)

		// For data tables (csv, xlsx, xls), also enqueue summary task
		if slices.Contains([]string{"csv", "xlsx", "xls"}, getFileType(existing.FileName)) {
			NewDataTableSummaryTask(ctx, s.task, tenantID, existing.ID, kb.SummaryModelID, kb.EmbeddingModelID)
		}

		return existing, nil
	}

	// For file-URL-based knowledge, enqueue document processing task with FileURL field
	if existing.Type == "file_url" && existing.Source != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := kb.IsMultimodalEnabled()

		// Check question generation config
		enableQuestionGeneration := false
		questionCount := 3
		if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
			enableQuestionGeneration = true
			if kb.QuestionGenerationConfig.QuestionCount > 0 {
				questionCount = kb.QuestionGenerationConfig.QuestionCount
			}
		}

		lang, _ := types.LanguageFromContext(ctx)
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
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal file URL reparse task payload: %v", err)
			return existing, nil
		}

		task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"))
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue file URL reparse task: %v", err)
			return existing, nil
		}
		logger.Infof(ctx, "Enqueued file URL reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)

		return existing, nil
	}

	// For URL-based knowledge, enqueue URL processing task
	if existing.Type == "url" && existing.Source != "" {
		tenantID := ctx.Value(types.TenantIDContextKey).(uint64)

		enableMultimodel := kb.IsMultimodalEnabled()

		// Check question generation config
		enableQuestionGeneration := false
		questionCount := 3
		if kb.QuestionGenerationConfig != nil && kb.QuestionGenerationConfig.Enabled {
			enableQuestionGeneration = true
			if kb.QuestionGenerationConfig.QuestionCount > 0 {
				questionCount = kb.QuestionGenerationConfig.QuestionCount
			}
		}

		lang, _ := types.LanguageFromContext(ctx)
		taskPayload := types.DocumentProcessPayload{
			TenantID:                 tenantID,
			KnowledgeID:              existing.ID,
			KnowledgeBaseID:          existing.KnowledgeBaseID,
			URL:                      existing.Source,
			EnableMultimodel:         enableMultimodel,
			EnableQuestionGeneration: enableQuestionGeneration,
			QuestionCount:            questionCount,
			Language:                 lang,
		}

		langfuse.InjectTracing(ctx, &taskPayload)
		payloadBytes, err := json.Marshal(taskPayload)
		if err != nil {
			logger.Errorf(ctx, "Failed to marshal URL reparse task payload: %v", err)
			return existing, nil
		}

		task := asynq.NewTask(types.TypeDocumentProcess, payloadBytes, asynq.Queue("default"), asynq.MaxRetry(3))
		info, err := s.task.Enqueue(task)
		if err != nil {
			logger.Errorf(ctx, "Failed to enqueue URL reparse task: %v", err)
			return existing, nil
		}
		logger.Infof(ctx, "Enqueued URL reparse task: id=%s queue=%s knowledge_id=%s", info.ID, info.Queue, existing.ID)

		return existing, nil
	}

	logger.Warnf(ctx, "Knowledge %s has no parseable content (no file, URL, or manual content)", knowledgeID)
	return existing, nil
}

func (s *knowledgeService) updateChunkVector(ctx context.Context, kbID string, chunks []*types.Chunk) error {
	// Get embedding model from knowledge base
	sourceKB, err := s.kbService.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return err
	}
	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, sourceKB.EmbeddingModelID)
	if err != nil {
		return err
	}

	// Initialize composite retrieve engine from tenant configuration
	indexInfo := make([]*types.IndexInfo, 0, len(chunks))
	ids := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.KnowledgeBaseID != kbID {
			logger.Warnf(ctx, "Knowledge base ID mismatch: %s != %s", chunk.KnowledgeBaseID, kbID)
			continue
		}
		indexInfo = append(indexInfo, &types.IndexInfo{
			Content:         chunk.Content,
			SourceID:        chunk.ID,
			SourceType:      types.ChunkSourceType,
			ChunkID:         chunk.ID,
			KnowledgeID:     chunk.KnowledgeID,
			KnowledgeBaseID: chunk.KnowledgeBaseID,
			IsEnabled:       true,
		})
		ids = append(ids, chunk.ID)
	}

	tenantInfo := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	retrieveEngine, err := retriever.NewCompositeRetrieveEngine(s.retrieveEngine, tenantInfo.GetEffectiveEngines())
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

	kb, err := s.kbService.GetKnowledgeBaseByID(ctx, payload.KnowledgeBaseID)
	if err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to get knowledge base: %v", err)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = fmt.Sprintf("failed to get knowledge base: %v", err)
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil
	}

	// Update status to processing
	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "ProcessManualUpdate: failed to update status to processing: %v", err)
		return nil
	}

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
	s.triggerManualProcessing(ctx, kb, knowledge, payload.Content, true)
	return nil
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

	// 检查是否正在删除 - 如果是则直接退出，避免与删除操作冲突
	if knowledge.ParseStatus == types.ParseStatusDeleting {
		logger.Infof(ctx, "Knowledge is being deleted, aborting processing: %s", payload.KnowledgeID)
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

	knowledge.ParseStatus = "processing"
	knowledge.UpdatedAt = time.Now()
	if err := s.repo.UpdateKnowledge(ctx, knowledge); err != nil {
		logger.Errorf(ctx, "failed to update knowledge status to processing: %v", err)
		return nil
	}

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
	if payload.FilePath != "" && IsAudioType(payload.FileType) && !kb.ASRConfig.IsASREnabled() {
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

		if resolvedFileType != "" && !allowedFileURLExtensions[strings.ToLower(resolvedFileType)] {
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
		convertResult, err = s.convert(ctx, payload, kb, knowledge, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
	} else if payload.URL != "" {
		// URL import
		convertResult, err = s.convert(ctx, payload, kb, knowledge, isLastRetry)
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
		s.processChunks(ctx, kb, knowledge, passageChunks, passageOpts)
		return nil
	} else {
		// File import
		convertResult, err = s.convert(ctx, payload, kb, knowledge, isLastRetry)
		if err != nil {
			return err
		}
		if convertResult == nil {
			return nil
		}
	}

	// Step 1.5: ASR transcription for audio files
	if convertResult != nil && convertResult.IsAudio && len(convertResult.AudioData) > 0 {
		if !kb.ASRConfig.IsASREnabled() {
			logger.Error(ctx, "Audio file detected but ASR is not configured")
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "ASR model is not configured for audio transcription"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil
		}

		logger.Infof(ctx, "[ASR] Starting audio transcription for knowledge %s, audio size=%d bytes",
			knowledge.ID, len(convertResult.AudioData))

		asrModel, err := s.modelService.GetASRModel(ctx, kb.ASRConfig.ModelID)
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

	// Step 3: Split into chunks using Go chunker
	chunkCfg := buildSplitterConfig(kb)

	processOpts := ProcessChunksOptions{
		EnableQuestionGeneration: payload.EnableQuestionGeneration,
		QuestionCount:            payload.QuestionCount,
		EnableMultimodel:         payload.EnableMultimodel,
		StoredImages:             storedImages,
	}

	if convertResult != nil {
		processOpts.Metadata = convertResult.Metadata
	}

	if kb.ChunkingConfig.EnableParentChild {
		parentCfg, childCfg := buildParentChildConfigs(kb.ChunkingConfig, chunkCfg)
		pcResult := chunker.SplitTextParentChild(convertResult.MarkdownContent, parentCfg, childCfg)
		chunks = make([]types.ParsedChunk, len(pcResult.Children))
		for i, c := range pcResult.Children {
			chunks[i] = types.ParsedChunk{
				Content:     c.Content,
				Seq:         c.Seq,
				Start:       c.Start,
				End:         c.End,
				ParentIndex: c.ParentIndex,
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
		splitChunks := chunker.SplitText(convertResult.MarkdownContent, chunkCfg)
		chunks = make([]types.ParsedChunk, len(splitChunks))
		for i, c := range splitChunks {
			chunks[i] = types.ParsedChunk{
				Content: c.Content,
				Seq:     c.Seq,
				Start:   c.Start,
				End:     c.End,
			}
		}
		logger.Infof(ctx, "Split document into %d chunks for knowledge %s", len(chunks), knowledge.ID)
	}

	// Step 4: Process chunks (vectorize + index + enqueue async tasks)
	s.processChunks(ctx, kb, knowledge, chunks, processOpts)

	return nil
}

// convert handles both file and URL reading using a unified ReadRequest.
func (s *knowledgeService) convert(
	ctx context.Context,
	payload types.DocumentProcessPayload,
	kb *types.KnowledgeBase,
	knowledge *types.Knowledge,
	isLastRetry bool,
) (*types.ReadResult, error) {
	isURL := payload.URL != ""
	fileType := payload.FileType
	overrides := s.getParserEngineOverridesFromContext(ctx)

	if isURL {
		if err := secutils.ValidateURLForSSRF(payload.URL); err != nil {
			logger.Errorf(ctx, "URL rejected for SSRF protection: %s, err: %v", payload.URL, err)
			knowledge.ParseStatus = "failed"
			knowledge.ErrorMessage = "URL is not allowed for security reasons"
			knowledge.UpdatedAt = time.Now()
			s.repo.UpdateKnowledge(ctx, knowledge)
			return nil, nil
		}
	}

	parserEngine := kb.ChunkingConfig.ResolveParserEngine(fileType)
	if isURL {
		parserEngine = kb.ChunkingConfig.ResolveParserEngine("url")
	}

	logger.Infof(ctx, "[convert] kb=%s fileType=%s isURL=%v engine=%q rules=%+v",
		kb.ID, fileType, isURL, parserEngine, kb.ChunkingConfig.ParserEngineRules)

	var reader interfaces.DocReader = s.resolveDocReader(ctx, parserEngine, fileType, isURL, overrides)
	if reader == nil {
		logger.Errorf(ctx, "[convert] no doc reader for kb=%s knowledge=%s fileType=%s engine=%q isURL=%v",
			kb.ID, knowledge.ID, fileType, parserEngine, isURL)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = "Document parsing service is not configured. Please use text/paragraph import or set DOCREADER_ADDR."
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil, nil
	}

	req := &types.ReadRequest{
		URL:                   payload.URL,
		Title:                 knowledge.Title,
		ParserEngine:          parserEngine,
		RequestID:             payload.RequestId,
		ParserEngineOverrides: overrides,
	}

	if !isURL {
		fileReader, err := s.resolveFileServiceForPath(ctx, kb, payload.FilePath).GetFile(ctx, payload.FilePath)
		if err != nil {
			return s.failKnowledge(ctx, knowledge, isLastRetry, "failed to get file: %v", err)
		}
		defer fileReader.Close()
		contentBytes, err := io.ReadAll(fileReader)
		if err != nil {
			return s.failKnowledge(ctx, knowledge, isLastRetry, "failed to read file: %v", err)
		}
		req.FileContent = contentBytes
		req.FileName = payload.FileName
		req.FileType = fileType
	}

	result, err := reader.Read(ctx, req)
	if err != nil {
		return s.failKnowledge(ctx, knowledge, isLastRetry, "document read failed: %v", err)
	}
	if result.Error != "" {
		logger.Errorf(ctx, "[convert] parser returned error kb=%s knowledge=%s file=%q type=%s engine=%q: %s",
			kb.ID, knowledge.ID, req.FileName, fileType, parserEngine, result.Error)
		knowledge.ParseStatus = "failed"
		knowledge.ErrorMessage = result.Error
		knowledge.UpdatedAt = time.Now()
		s.repo.UpdateKnowledge(ctx, knowledge)
		return nil, nil
	}
	return result, nil
}

// resolveDocReader returns the appropriate DocReader for the given engine.
// Returns nil when the required service is unavailable.
func (s *knowledgeService) resolveDocReader(ctx context.Context, engine, fileType string, isURL bool, overrides map[string]string) interfaces.DocReader {
	switch engine {
	case docparser.SimpleEngineName:
		return &docparser.SimpleFormatReader{}
	case docparser.WeKnoraCloudEngineName:
		creds := s.tenantService.GetWeKnoraCloudCredentials(ctx)
		if creds == nil {
			logger.Warnf(ctx, "[resolveDocReader] WeKnoraCloud: no tenant credentials (fileType=%s)", fileType)
			return nil
		}
		reader, err := docparser.NewWeKnoraCloudSignedDocumentReader(creds.AppID, creds.AppSecret)
		if err != nil {
			logger.Errorf(ctx, "[resolveDocReader] WeKnoraCloud reader init failed: %v", err)
			return nil
		}
		return reader
	case "mineru":
		return docparser.NewMinerUReader(overrides)
	case "mineru_cloud":
		return docparser.NewMinerUCloudReader(overrides)
	case "builtin":
		// 明确指定使用 builtin 引擎（docreader），不使用 simple format 兜底
		return s.documentReader
	default:
		// 未指定引擎时的兜底逻辑：simple format 使用 Go 原生处理，其他使用 docreader
		if !isURL && docparser.IsSimpleFormat(fileType) {
			return &docparser.SimpleFormatReader{}
		}
		return s.documentReader
	}
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

	redisKey := fmt.Sprintf("multimodal:pending:%s", knowledge.ID)
	if s.redisClient != nil {
		if err := s.redisClient.Set(ctx, redisKey, len(images), 24*time.Hour).Err(); err != nil {
			logger.Warnf(ctx, "Failed to set multimodal pending count for %s: %v", knowledge.ID, err)
		}
	}

	for _, img := range images {
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

		lang, _ := types.LanguageFromContext(ctx)
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
		}

		langfuse.InjectTracing(ctx, &payload)
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			logger.Warnf(ctx, "Failed to marshal image multimodal payload: %v", err)
			continue
		}

		task := asynq.NewTask(types.TypeImageMultimodal, payloadBytes)
		if _, err := s.task.Enqueue(task); err != nil {
			logger.Warnf(ctx, "Failed to enqueue image multimodal task for %s: %v", img.ServingURL, err)
		} else {
			logger.Infof(ctx, "Enqueued image:multimodal task for %s", img.ServingURL)
		}
	}
}
