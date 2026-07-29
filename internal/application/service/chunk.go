// Package service provides business logic implementations for WeKnora application
// This package contains service layer implementations that coordinate between
// repositories and handlers, applying business rules and transaction management
package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/searchutil"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

var ErrChunkRevisionConflict = repository.ErrChunkRevisionConflict

// chunkService implements the ChunkService interface
// It provides operations for managing document chunks in the knowledge base
// Chunks are segments of documents that have been processed and prepared for indexing
type chunkService struct {
	chunkRepository interfaces.ChunkRepository // Repository for chunk data persistence
	knowledgeRepo   interfaces.KnowledgeRepository
	kbRepository    interfaces.KnowledgeBaseRepository
	modelService    interfaces.ModelService
	retrieveEngine  interfaces.RetrieveEngineRegistry
	ownership       retriever.TenantStoreOwnership
	task            interfaces.TaskEnqueuer
	pendingRepo     interfaces.TaskPendingOpsRepository
	spanTracker     SpanTracker
}

// NewChunkService creates a new chunk service
// It initializes a service with the provided chunk repository
// Parameters:
//   - chunkRepository: Repository for chunk operations
//
// Returns:
//   - interfaces.ChunkService: Initialized chunk service implementation
func NewChunkService(
	chunkRepository interfaces.ChunkRepository,
	knowledgeRepo interfaces.KnowledgeRepository,
	kbRepository interfaces.KnowledgeBaseRepository,
	modelService interfaces.ModelService,
	retrieveEngine interfaces.RetrieveEngineRegistry,
	ownership retriever.TenantStoreOwnership,
	task interfaces.TaskEnqueuer,
	pendingRepo interfaces.TaskPendingOpsRepository,
	spanTracker SpanTracker,
) interfaces.ChunkService {
	return &chunkService{
		chunkRepository: chunkRepository,
		knowledgeRepo:   knowledgeRepo,
		kbRepository:    kbRepository,
		modelService:    modelService,
		retrieveEngine:  retrieveEngine,
		ownership:       ownership,
		task:            task,
		pendingRepo:     pendingRepo,
		spanTracker:     spanTracker,
	}
}

const maxEditableChunkLength = 200000

// GetRepository gets the chunk repository
// Parameters:
//   - ctx: Context with authentication and request information
//
// Returns:
//   - interfaces.ChunkRepository: Chunk repository
func (s *chunkService) GetRepository() interfaces.ChunkRepository {
	return s.chunkRepository
}

// CreateChunks creates multiple chunks
// This method persists a batch of document chunks to the repository
// Parameters:
//   - ctx: Context with authentication and request information
//   - chunks: Slice of document chunks to create
//
// Returns:
//   - error: Any error encountered during chunk creation
func (s *chunkService) CreateChunks(ctx context.Context, chunks []*types.Chunk) error {
	err := s.chunkRepository.CreateChunks(ctx, chunks)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_count": len(chunks),
		})
		return err
	}

	logger.Infof(ctx, "Add %d chunks successfully", len(chunks))
	return nil
}

// GetChunkByID retrieves a chunk by its ID
// This method fetches a specific chunk using its ID and validates tenant access
// Parameters:
//   - ctx: Context with authentication and request information
//   - knowledgeID: ID of the knowledge document containing the chunk
//   - id: ID of the chunk to retrieve
//
// Returns:
//   - *types.Chunk: Retrieved chunk if found
//   - error: Any error encountered during retrieval
func (s *chunkService) GetChunkByID(ctx context.Context, id string) (*types.Chunk, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Getting chunk by ID, ID: %s, tenant ID: %d", id, tenantID)
	chunk, err := s.chunkRepository.GetChunkByID(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return nil, err
	}

	logger.Info(ctx, "Chunk retrieved successfully")
	return chunk, nil
}

// GetChunkByIDOnly retrieves a chunk by ID without tenant filter (for permission resolution).
func (s *chunkService) GetChunkByIDOnly(ctx context.Context, id string) (*types.Chunk, error) {
	chunk, err := s.chunkRepository.GetChunkByIDOnly(ctx, id)
	if err != nil {
		// errors.Is (not string equality) so the sentinel survives wrapping.
		// ErrChunkNotFound aliases the repo sentinel, so this matches directly.
		if errors.Is(err, ErrChunkNotFound) {
			return nil, ErrChunkNotFound
		}
		logger.ErrorWithFields(ctx, err, map[string]interface{}{"chunk_id": id})
		return nil, err
	}
	return chunk, nil
}

// ListChunksByKnowledgeID lists all chunks for a knowledge ID
// This method retrieves all chunks belonging to a specific knowledge document
// Parameters:
//   - ctx: Context with authentication and request information
//   - knowledgeID: ID of the knowledge document
//
// Returns:
//   - []*types.Chunk: List of chunks belonging to the knowledge document
//   - error: Any error encountered during retrieval
func (s *chunkService) ListChunksByKnowledgeID(ctx context.Context, knowledgeID string) ([]*types.Chunk, error) {
	logger.Info(ctx, "Start listing chunks by knowledge ID")
	logger.Infof(ctx, "Knowledge ID: %s", knowledgeID)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	chunks, err := s.chunkRepository.ListChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": knowledgeID,
			"tenant_id":    tenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d chunks successfully", len(chunks))
	return chunks, nil
}

// ListPagedChunksByKnowledgeID lists chunks for a knowledge ID with pagination
// This method retrieves chunks with pagination support for better performance with large datasets
// Parameters:
//   - ctx: Context with authentication and request information
//   - knowledgeID: ID of the knowledge document
//   - page: Pagination parameters including page number and page size
//
// Returns:
//   - *types.PageResult: Paginated result containing chunks and pagination metadata
//   - error: Any error encountered during retrieval
func (s *chunkService) ListPagedChunksByKnowledgeID(ctx context.Context,
	knowledgeID string, page *types.Pagination, chunkType []types.ChunkType,
) (*types.PageResult, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	chunks, total, err := s.chunkRepository.ListPagedChunksByKnowledgeID(
		ctx,
		tenantID,
		knowledgeID,
		page,
		chunkType,
		nil,
		"",
		"",
		"",
		"",
	)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": knowledgeID,
			"tenant_id":    tenantID,
		})
		return nil, err
	}

	logger.Infof(ctx, "Retrieved %d chunks out of %d total chunks", len(chunks), total)
	return types.NewPageResult(total, page, chunks), nil
}

// updateChunk updates a chunk
// This method updates an existing chunk in the repository
// Parameters:
//   - ctx: Context with authentication and request information
//   - chunk: Chunk with updated fields
//
// Returns:
//   - error: Any error encountered during update
//
// This method handles the actual update logic for a chunk, including updating the vector database representation
func (s *chunkService) UpdateChunk(ctx context.Context, chunk *types.Chunk) error {
	logger.Infof(ctx, "Updating chunk, ID: %s, knowledge ID: %s", chunk.ID, chunk.KnowledgeID)

	// Update the chunk in the repository
	err := s.chunkRepository.UpdateChunk(ctx, chunk)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id":     chunk.ID,
			"knowledge_id": chunk.KnowledgeID,
		})
		return err
	}

	logger.Info(ctx, "Chunk updated successfully")
	return nil
}

// UpdateChunks updates chunks in batch
func (s *chunkService) UpdateChunks(ctx context.Context, chunks []*types.Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	logger.Infof(ctx, "Updating %d chunks in batch", len(chunks))

	// Update the chunks in the repository
	err := s.chunkRepository.UpdateChunks(ctx, chunks)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_count": len(chunks),
		})
		return err
	}

	logger.Infof(ctx, "Successfully updated %d chunks", len(chunks))
	return nil
}

// DeleteChunk deletes a chunk by ID
// This method removes a specific chunk from the repository
// Parameters:
//   - ctx: Context with authentication and request information
//   - id: ID of the chunk to delete
//
// Returns:
//   - error: Any error encountered during deletion
func (s *chunkService) DeleteChunk(ctx context.Context, id string) error {
	tenantID := types.MustTenantIDFromContext(ctx)
	err := s.chunkRepository.DeleteChunk(ctx, tenantID, id)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"tenant_id": tenantID,
		})
		return err
	}
	logger.Info(ctx, "Chunk deleted successfully")
	return nil
}

// DeleteChunks deletes chunks by IDs in batch
// This method removes multiple chunks from the repository in a single operation
// Parameters:
//   - ctx: Context with authentication and request information
//   - ids: Slice of chunk IDs to delete
//
// Returns:
//   - error: Any error encountered during batch deletion
func (s *chunkService) DeleteChunks(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	logger.Info(ctx, "Start deleting chunks in batch")
	logger.Infof(ctx, "Deleting %d chunks", len(ids))

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	err := s.chunkRepository.DeleteChunks(ctx, tenantID, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_ids": ids,
			"tenant_id": tenantID,
		})
		return err
	}

	logger.Infof(ctx, "Successfully deleted %d chunks", len(ids))
	return nil
}

// DeleteChunksByKnowledgeID deletes all chunks for a knowledge ID
// This method removes all chunks belonging to a specific knowledge document
// Parameters:
//   - ctx: Context with authentication and request information
//   - knowledgeID: ID of the knowledge document
//
// Returns:
//   - error: Any error encountered during bulk deletion
func (s *chunkService) DeleteChunksByKnowledgeID(ctx context.Context, knowledgeID string) error {
	logger.Info(ctx, "Start deleting all chunks by knowledge ID")
	logger.Infof(ctx, "Knowledge ID: %s", knowledgeID)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	err := s.chunkRepository.DeleteChunksByKnowledgeID(ctx, tenantID, knowledgeID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": knowledgeID,
			"tenant_id":    tenantID,
		})
		return err
	}

	logger.Info(ctx, "All chunks under knowledge deleted successfully")
	return nil
}

func (s *chunkService) DeleteByKnowledgeList(ctx context.Context, ids []string) error {
	logger.Info(ctx, "Start deleting all chunks by knowledge IDs")
	logger.Infof(ctx, "Knowledge IDs: %v", ids)

	tenantID := types.MustTenantIDFromContext(ctx)
	logger.Infof(ctx, "Tenant ID: %d", tenantID)

	err := s.chunkRepository.DeleteByKnowledgeList(ctx, tenantID, ids)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_id": ids,
			"tenant_id":    tenantID,
		})
		return err
	}

	logger.Info(ctx, "All chunks under knowledge deleted successfully")
	return nil
}

func (s *chunkService) ListChunkByParentID(
	ctx context.Context,
	tenantID uint64,
	parentID string,
) ([]*types.Chunk, error) {
	logger.Info(ctx, "Start listing chunk by parent ID")
	logger.Infof(ctx, "Parent ID: %s", parentID)

	chunks, err := s.chunkRepository.ListChunkByParentID(ctx, tenantID, parentID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"parent_id": parentID,
			"tenant_id": tenantID,
		})
		return nil, err
	}

	logger.Info(ctx, "Chunk listed successfully")
	return chunks, nil
}

// UpdateDocumentChunk applies an optimistic, versioned edit. Generated
// retrieval questions remain indexed across content edits; the UI marks them as
// stale when their revision no longer matches the current body. The current row
// remains saved when reindexing fails and exposes index_status=failed so the
// UI never presents a false success state.
func (s *chunkService) UpdateDocumentChunk(
	ctx context.Context, chunkID string, content *string, isEnabled *bool, expectedRevision *int,
) (*types.Chunk, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	chunk, err := s.chunkRepository.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		return nil, err
	}
	if chunk.ChunkType != types.ChunkTypeText {
		return nil, fmt.Errorf("only text chunks can be edited")
	}
	if expectedRevision != nil && *expectedRevision != chunk.ContentRevision {
		return nil, ErrChunkRevisionConflict
	}

	newContent := chunk.Content
	if content != nil {
		newContent = strings.TrimSpace(*content)
		if newContent == "" {
			return nil, fmt.Errorf("chunk content cannot be empty")
		}
		if len(newContent) > maxEditableChunkLength {
			return nil, fmt.Errorf("chunk content exceeds %d bytes", maxEditableChunkLength)
		}
	}
	newEnabled := chunk.IsEnabled
	if isEnabled != nil {
		newEnabled = *isEnabled
	}
	if newContent == chunk.Content && newEnabled == chunk.IsEnabled {
		if chunk.IndexStatus == "failed" {
			if chunk.ParentChunkID != "" && chunk.ContentRevision > 0 {
				if err := s.rebuildParentContent(ctx, chunk); err != nil {
					logger.Warnf(ctx, "Failed to rebuild parent chunk while retrying edit: %v", err)
					return chunk, nil
				}
			}
			chunk.IndexStatus = "processing"
			_ = s.chunkRepository.UpdateChunk(ctx, chunk)
			if err := s.syncChunkIndex(ctx, chunk); err != nil {
				chunk.IndexStatus = "failed"
				_ = s.chunkRepository.UpdateChunk(ctx, chunk)
				return chunk, nil
			}
			chunk.IndexStatus = "ready"
			if err := s.chunkRepository.UpdateChunk(ctx, chunk); err != nil {
				return nil, err
			}
		}
		return chunk, nil
	}
	if content != nil {
		sourceContent := chunk.SourceContent
		if sourceContent == "" {
			sourceContent = chunk.Content
		}
		if err := validateEditedChunkImages(sourceContent, newContent); err != nil {
			return nil, err
		}
	}

	actorID, _ := types.UserIDFromContext(ctx)
	now := time.Now()
	oldRevision := chunk.ContentRevision
	revision := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: chunk.TenantID,
		KnowledgeBaseID: chunk.KnowledgeBaseID, KnowledgeID: chunk.KnowledgeID,
		ChunkID: chunk.ID, Revision: oldRevision, Content: chunk.Content,
		IsEnabled: chunk.IsEnabled, EditorID: chunk.LastEditorID,
		EditSource: "user", EditedAt: chunk.UpdatedAt, CreatedAt: now,
	}
	if chunk.SourceContent == "" {
		chunk.SourceContent = chunk.Content
	}
	bodyChanged := newContent != chunk.Content
	chunk.Content = newContent
	chunk.IsEnabled = newEnabled
	chunk.ContentRevision++
	chunk.LastEditorID = actorID
	chunk.IndexStatus = "processing"
	chunk.UpdatedAt = now
	wikiAttempt := 0
	if s.spanTracker != nil {
		wikiAttempt = s.spanTracker.LatestAttempt(ctx, chunk.KnowledgeID)
	}
	wikiPendingOp, wikiEnabled, err := s.chunkWikiPendingOp(ctx, chunk, wikiAttempt)
	if err != nil {
		return nil, err
	}
	outboxPersisted := false
	if wikiPendingOp != nil {
		if outboxRepo, ok := s.chunkRepository.(interfaces.ChunkRevisionOutboxRepository); ok {
			err = outboxRepo.SaveChunkRevisionWithPendingOp(
				ctx, chunk, revision, oldRevision, wikiPendingOp,
			)
			outboxPersisted = err == nil
		} else {
			err = s.chunkRepository.SaveChunkRevision(ctx, chunk, revision, oldRevision)
		}
	} else {
		err = s.chunkRepository.SaveChunkRevision(ctx, chunk, revision, oldRevision)
	}
	if err != nil {
		return nil, err
	}
	// The chunk row is authoritative from this point onward. Queue the Wiki
	// rebuild on every subsequent exit path (including parent/image/vector
	// synchronization failures) so a successfully persisted edit can never
	// leave published paragraph sources pointing at the previous revision.
	// Production repositories commit the durable Wiki op in the same DB
	// transaction as the chunk revision. Test doubles/alternate repositories
	// retain the previous post-commit enqueue path as a compatibility fallback.
	if outboxPersisted {
		lang, _ := types.LanguageFromContext(ctx)
		if triggerErr := scheduleWikiIngestTrigger(
			ctx, s.task, chunk.TenantID, chunk.KnowledgeBaseID, lang, wikiIngestDelay,
		); triggerErr != nil {
			logger.Warnf(ctx,
				"Chunk %s saved with durable Wiki refresh row, but trigger enqueue failed: %v",
				chunk.ID, triggerErr,
			)
		}
	} else if wikiEnabled {
		defer func() {
			refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			EnqueueWikiIngest(
				refreshCtx, s.task, s.pendingRepo, chunk.TenantID,
				chunk.KnowledgeBaseID, chunk.KnowledgeID, wikiAttempt,
			)
		}()
	}

	if bodyChanged && chunk.ParentChunkID != "" {
		if err := s.rebuildParentContent(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to rebuild parent chunk after edit: %v", err)
			chunk.IndexStatus = "failed"
			_ = s.chunkRepository.UpdateChunk(ctx, chunk)
			return chunk, nil
		}
	}
	if bodyChanged || newEnabled != revision.IsEnabled {
		if err := s.syncEditedChunkImages(ctx, chunk); err != nil {
			logger.Warnf(ctx, "Failed to synchronize image children after chunk edit: %v", err)
			chunk.IndexStatus = "failed"
			_ = s.chunkRepository.UpdateChunk(ctx, chunk)
			return chunk, nil
		}
	}
	if bodyChanged || newEnabled != revision.IsEnabled {
		knowledge, getErr := s.knowledgeRepo.GetKnowledgeByID(ctx, tenantID, chunk.KnowledgeID)
		if getErr == nil {
			if err := enqueueSummaryRefresh(
				ctx, s.knowledgeRepo, s.task, s.kbRepository, s.spanTracker, knowledge,
			); err != nil {
				logger.Warnf(ctx, "Chunk saved but summary refresh enqueue failed for %s: %v", knowledge.ID, err)
			}
		}
	}
	if err := s.syncChunkIndex(ctx, chunk); err != nil {
		chunk.IndexStatus = "failed"
		_ = s.chunkRepository.UpdateChunk(ctx, chunk)
		logger.Errorf(ctx, "Chunk %s saved but reindex failed: %v", chunk.ID, err)
		return chunk, nil
	}
	chunk.IndexStatus = "ready"
	if err := s.chunkRepository.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}
	return chunk, nil
}

func (s *chunkService) chunkWikiPendingOp(
	ctx context.Context, chunk *types.Chunk, attempt int,
) (*types.TaskPendingOp, bool, error) {
	if chunk == nil || chunk.KnowledgeBaseID == "" || chunk.KnowledgeID == "" || s.kbRepository == nil {
		return nil, false, nil
	}
	kb, err := s.kbRepository.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		return nil, false, fmt.Errorf("load knowledge base before chunk Wiki outbox: %w", err)
	}
	if !kb.IsWikiEnabled() {
		return nil, false, nil
	}
	op, err := newWikiIngestPendingRow(ctx, chunk.TenantID, chunk.KnowledgeBaseID, chunk.KnowledgeID, attempt)
	if err != nil {
		return nil, true, fmt.Errorf("build chunk Wiki outbox operation: %w", err)
	}
	return op, true, nil
}

// enqueueChunkWikiRefresh reuses the durable per-KB Wiki ingest lane after a
// manual chunk mutation. It is deliberately best-effort because the chunk
// revision has already committed and returning an error would falsely imply
// that the edit itself was rolled back. EnqueueWikiIngest persists a
// per-knowledge op and its consumer coalesces repeated edits.
func (s *chunkService) enqueueChunkWikiRefresh(ctx context.Context, chunk *types.Chunk, attempt int) {
	if chunk == nil || chunk.KnowledgeBaseID == "" || chunk.KnowledgeID == "" {
		return
	}
	if s.kbRepository == nil {
		logger.Warnf(ctx, "Chunk %s saved but wiki refresh KB repository is unavailable", chunk.ID)
		return
	}
	kb, err := s.kbRepository.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		logger.Warnf(ctx, "Chunk %s saved but wiki refresh KB lookup failed: %v", chunk.ID, err)
		return
	}
	if !kb.IsWikiEnabled() {
		return
	}
	if s.task == nil || s.pendingRepo == nil {
		logger.Warnf(ctx, "Chunk %s saved but wiki refresh queue dependencies are unavailable", chunk.ID)
		return
	}
	EnqueueWikiIngest(
		ctx, s.task, s.pendingRepo, chunk.TenantID,
		chunk.KnowledgeBaseID, chunk.KnowledgeID, attempt,
	)
}

func validateEditedChunkImages(sourceContent, editedContent string) error {
	allowed := searchutil.ImageURLsInContent(sourceContent)
	for url := range searchutil.ImageURLsInContent(editedContent) {
		if !allowed[url] {
			return fmt.Errorf("adding images to an existing chunk is not supported: %s", url)
		}
	}
	return nil
}

func imageChildMatchesContent(child *types.Chunk, contentURLs map[string]bool) bool {
	for url := range searchutil.ImageURLsFromInfo(child.ImageInfo) {
		if contentURLs[url] {
			return true
		}
	}
	return false
}

// syncEditedChunkImages removes image OCR/caption children from retrieval
// when their Markdown image was deleted. Rows are disabled rather than hard
// deleted so reverting to a historical chunk revision can re-enable them.
func (s *chunkService) syncEditedChunkImages(ctx context.Context, chunk *types.Chunk) error {
	children, err := s.chunkRepository.ListChunkByParentID(ctx, chunk.TenantID, chunk.ID)
	if err != nil {
		return err
	}
	contentURLs := searchutil.ImageURLsInContent(chunk.Content)
	for _, child := range children {
		if child.ChunkType != types.ChunkTypeImageOCR && child.ChunkType != types.ChunkTypeImageCaption {
			continue
		}
		desiredEnabled := chunk.IsEnabled && imageChildMatchesContent(child, contentURLs)
		if child.IsEnabled == desiredEnabled && child.IndexStatus == "ready" {
			continue
		}
		child.IsEnabled = desiredEnabled
		child.IndexStatus = "processing"
		child.UpdatedAt = time.Now()
		if err := s.chunkRepository.UpdateChunk(ctx, child); err != nil {
			return err
		}
		if err := s.syncChunkIndex(ctx, child); err != nil {
			child.IndexStatus = "failed"
			_ = s.chunkRepository.UpdateChunk(ctx, child)
			return err
		}
		child.IndexStatus = "ready"
		if err := s.chunkRepository.UpdateChunk(ctx, child); err != nil {
			return err
		}
	}
	return nil
}

func (s *chunkService) ListChunkRevisions(ctx context.Context, chunkID string) ([]*types.ChunkRevision, error) {
	return s.chunkRepository.ListChunkRevisions(ctx, types.MustTenantIDFromContext(ctx), chunkID)
}

func (s *chunkService) RevertDocumentChunk(
	ctx context.Context, chunkID string, revision int, expectedRevision *int,
) (*types.Chunk, error) {
	item, err := s.chunkRepository.GetChunkRevision(ctx, types.MustTenantIDFromContext(ctx), chunkID, revision)
	if err != nil {
		return nil, err
	}
	content := item.Content
	enabled := item.IsEnabled
	return s.UpdateDocumentChunk(ctx, chunkID, &content, &enabled, expectedRevision)
}

// rebuildParentContent overlays manually edited child ranges on the immutable
// parent source. Applying replacements in reverse offset order preserves the
// parser coordinate system even when edited text changes length.
func (s *chunkService) rebuildParentContent(ctx context.Context, edited *types.Chunk) error {
	parent, err := s.chunkRepository.GetChunkByID(ctx, edited.TenantID, edited.ParentChunkID)
	if err != nil {
		return err
	}
	children, err := s.chunkRepository.ListChunkByParentID(ctx, edited.TenantID, parent.ID)
	if err != nil {
		return err
	}
	base := parent.SourceContent
	if base == "" {
		base = parent.Content
		parent.SourceContent = base
	}
	baseRunes := []rune(base)
	type replacement struct {
		start, end int
		content    string
		updatedAt  time.Time
	}
	replacements := make([]replacement, 0)
	for _, child := range children {
		if child.ContentRevision == 0 {
			continue
		}
		start, end := child.StartAt-parent.StartAt, child.EndAt-parent.StartAt
		if start >= 0 && end >= start && end <= len(baseRunes) {
			replacements = append(replacements, replacement{start, end, child.Content, child.UpdatedAt})
		}
	}
	// Overlapping arbitrary replacements cannot both occupy the same source
	// interval. Keep the latest edit in place, then append every conflicting
	// current body that is not already represented. Retrieval must prefer a
	// small amount of duplication over silently losing an accepted edit.
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].updatedAt.After(replacements[j].updatedAt) })
	selected := make([]replacement, 0, len(replacements))
	conflicts := make([]replacement, 0)
	for _, candidate := range replacements {
		overlaps := false
		for _, existing := range selected {
			if candidate.start < existing.end && candidate.end > existing.start {
				overlaps = true
				break
			}
		}
		if !overlaps {
			selected = append(selected, candidate)
		} else {
			conflicts = append(conflicts, candidate)
		}
	}
	replacements = selected
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, repl := range replacements {
		baseRunes = append(append(append([]rune{}, baseRunes[:repl.start]...), []rune(repl.content)...), baseRunes[repl.end:]...)
	}
	parent.Content = string(baseRunes)
	for _, conflict := range conflicts {
		parent.Content = searchutil.JoinChunkContent(parent.Content, conflict.content, "\n\n")
	}
	parent.UpdatedAt = time.Now()
	return s.chunkRepository.UpdateChunk(ctx, parent)
}

func (s *chunkService) syncChunkIndex(ctx context.Context, chunk *types.Chunk) error {
	kb, err := s.kbRepository.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		return err
	}
	if !kb.NeedsEmbeddingModel() {
		return nil
	}
	embedder, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		return err
	}
	engine, err := retriever.CreateRetrieveEngineForKB(ctx, s.retrieveEngine, s.ownership, chunk.TenantID, kb.VectorStoreID)
	if err != nil {
		return err
	}
	if err := engine.DeleteByChunkIDList(ctx, []string{chunk.ID}, embedder.GetDimensions(), kb.Type); err != nil {
		return err
	}
	if !chunk.IsEnabled {
		return nil
	}
	knowledge, err := s.knowledgeRepo.GetKnowledgeByID(ctx, chunk.TenantID, chunk.KnowledgeID)
	if err != nil {
		return err
	}
	items := []*types.IndexInfo{{
		Content: buildKnowledgeIndexContent(knowledge, chunk.EmbeddingContent()), SourceID: chunk.ID,
		SourceType: types.ChunkSourceType, ChunkID: chunk.ID,
		KnowledgeID: chunk.KnowledgeID, KnowledgeBaseID: chunk.KnowledgeBaseID,
		KnowledgeType: kb.Type, IsEnabled: true,
	}}
	meta, err := chunk.DocumentMetadata()
	if err != nil {
		return err
	}
	if meta != nil {
		for _, question := range meta.GeneratedQuestions {
			if strings.TrimSpace(question.Question) == "" {
				continue
			}
			items = append(items, &types.IndexInfo{
				Content: buildKnowledgeIndexContent(knowledge, question.Question), SourceID: types.GeneratedQuestionSourceID(chunk.ID, question.ID),
				SourceType: types.ChunkSourceType, ChunkID: chunk.ID,
				KnowledgeID: chunk.KnowledgeID, KnowledgeBaseID: chunk.KnowledgeBaseID,
				KnowledgeType: kb.Type, IsEnabled: true,
			})
		}
	}
	return engine.BatchIndex(ctx, embedder, items)
}

func (s *chunkService) UpsertGeneratedQuestion(
	ctx context.Context, chunkID string, questionID string, question string,
) (*types.GeneratedQuestion, error) {
	question = strings.TrimSpace(question)
	if question == "" {
		return nil, fmt.Errorf("question cannot be empty")
	}
	chunk, err := s.chunkRepository.GetChunkByID(ctx, types.MustTenantIDFromContext(ctx), chunkID)
	if err != nil {
		return nil, err
	}
	meta, err := chunk.DocumentMetadata()
	if err != nil {
		return nil, err
	}
	if meta == nil {
		meta = &types.DocumentChunkMetadata{}
	}
	currentRevision := chunk.ContentRevision
	if questionID == "" {
		questionID = uuid.NewString()
		meta.GeneratedQuestions = append(meta.GeneratedQuestions, types.GeneratedQuestion{
			ID: questionID, Question: question, ContentRevision: &currentRevision,
		})
	} else {
		found := false
		for i := range meta.GeneratedQuestions {
			if meta.GeneratedQuestions[i].ID == questionID {
				meta.GeneratedQuestions[i].Question = question
				meta.GeneratedQuestions[i].ContentRevision = &currentRevision
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("question not found")
		}
	}
	if err := chunk.SetDocumentMetadata(meta); err != nil {
		return nil, err
	}
	if err := s.chunkRepository.UpdateChunk(ctx, chunk); err != nil {
		return nil, err
	}
	if err := s.syncChunkIndex(ctx, chunk); err != nil {
		return nil, err
	}
	for i := range meta.GeneratedQuestions {
		if meta.GeneratedQuestions[i].ID == questionID {
			return &meta.GeneratedQuestions[i], nil
		}
	}
	return nil, fmt.Errorf("question not found")
}

// DeleteGeneratedQuestion deletes a single generated question from a chunk by question ID
// This updates the chunk metadata and removes the corresponding vector index
func (s *chunkService) DeleteGeneratedQuestion(ctx context.Context, chunkID string, questionID string) error {
	logger.Infof(ctx, "Deleting generated question, chunk ID: %s, question ID: %s", chunkID, questionID)

	tenantID := types.MustTenantIDFromContext(ctx)

	// 1. Get the chunk
	chunk, err := s.chunkRepository.GetChunkByID(ctx, tenantID, chunkID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id":  chunkID,
			"tenant_id": tenantID,
		})
		return fmt.Errorf("failed to get chunk: %w", err)
	}

	// 2. Parse the metadata
	meta, err := chunk.DocumentMetadata()
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id": chunkID,
		})
		return fmt.Errorf("failed to parse chunk metadata: %w", err)
	}

	if meta == nil || len(meta.GeneratedQuestions) == 0 {
		return fmt.Errorf("no generated questions found for chunk %s", chunkID)
	}

	// 3. Find the question by ID
	questionIndex := -1
	for i, q := range meta.GeneratedQuestions {
		if q.ID == questionID {
			questionIndex = i
			break
		}
	}

	if questionIndex == -1 {
		return fmt.Errorf("question with ID %s not found in chunk %s", questionID, chunkID)
	}

	// 4. Get knowledge base to get embedding model
	kb, err := s.kbRepository.GetKnowledgeBaseByID(ctx, chunk.KnowledgeBaseID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"knowledge_base_id": chunk.KnowledgeBaseID,
		})
		return fmt.Errorf("failed to get knowledge base: %w", err)
	}

	// 5. Delete the vector index for this question
	// The source_id format is: {chunk_id}-{question_id}
	sourceID := types.GeneratedQuestionSourceID(chunkID, questionID)

	retrieveEngine, err := retriever.CreateRetrieveEngineForKB(
		ctx, s.retrieveEngine, s.ownership, tenantID, kb.VectorStoreID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id": chunkID,
		})
		return fmt.Errorf("failed to create retrieve engine: %w", err)
	}

	embeddingModel, err := s.modelService.GetEmbeddingModel(ctx, kb.EmbeddingModelID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"embedding_model_id": kb.EmbeddingModelID,
		})
		return fmt.Errorf("failed to get embedding model: %w", err)
	}

	// Delete the vector index by source ID
	if err := retrieveEngine.DeleteBySourceIDList(ctx, []string{sourceID}, embeddingModel.GetDimensions(), kb.Type); err != nil {
		logger.Warnf(ctx, "Failed to delete vector index for question (may not exist): %v", err)
		// Continue even if vector deletion fails - the question might not have been indexed
	}

	// 6. Remove the question from metadata
	newQuestions := make([]types.GeneratedQuestion, 0, len(meta.GeneratedQuestions)-1)
	for i, q := range meta.GeneratedQuestions {
		if i != questionIndex {
			newQuestions = append(newQuestions, q)
		}
	}

	// 7. Update chunk metadata
	meta.GeneratedQuestions = newQuestions
	if err := chunk.SetDocumentMetadata(meta); err != nil {
		return fmt.Errorf("failed to set chunk metadata: %w", err)
	}

	if err := s.chunkRepository.UpdateChunk(ctx, chunk); err != nil {
		logger.ErrorWithFields(ctx, err, map[string]interface{}{
			"chunk_id": chunkID,
		})
		return fmt.Errorf("failed to update chunk: %w", err)
	}

	logger.Infof(ctx, "Successfully deleted generated question %s from chunk %s", questionID, chunkID)
	return nil
}
