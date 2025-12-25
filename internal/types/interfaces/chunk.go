package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkRepository defines the interface for chunk repository operations
type ChunkRepository interface {
	// CreateChunks creates chunks
	CreateChunks(ctx context.Context, chunks []*types.Chunk) error
	// GetChunkByID gets a chunk by id
	GetChunkByID(ctx context.Context, tenantID uint64, id string) (*types.Chunk, error)
	// ListChunksByID lists chunks by ids
	ListChunksByID(ctx context.Context, tenantID uint64, ids []string) ([]*types.Chunk, error)
	// ListChunksByKnowledgeID lists chunks by knowledge id
	ListChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) ([]*types.Chunk, error)
	// ListPagedChunksByKnowledgeID lists paged chunks by knowledge id.
	// When tagID is non-empty, results are filtered by tag_id.
	// sortOrder: "asc" for time ascending (updated_at ASC), default is time descending (updated_at DESC)
	// searchField: specifies which field to search in (for FAQ: "standard_question", "similar_questions", "answers", "" for all)
	ListPagedChunksByKnowledgeID(
		ctx context.Context,
		tenantID uint64,
		knowledgeID string,
		page *types.Pagination,
		chunkType []types.ChunkType,
		tagID string,
		keyword string,
		searchField string,
		sortOrder string,
	) ([]*types.Chunk, int64, error)
	ListChunkByParentID(ctx context.Context, tenantID uint64, parentID string) ([]*types.Chunk, error)
	// UpdateChunk updates a chunk
	UpdateChunk(ctx context.Context, chunk *types.Chunk) error
	// UpdateChunks updates chunks in batch
	UpdateChunks(ctx context.Context, chunks []*types.Chunk) error
	// DeleteChunk deletes a chunk
	DeleteChunk(ctx context.Context, tenantID uint64, id string) error
	// DeleteChunks deletes chunks by IDs in batch
	DeleteChunks(ctx context.Context, tenantID uint64, ids []string) error
	// DeleteChunksByKnowledgeID deletes chunks by knowledge id
	DeleteChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) error
	// DeleteByKnowledgeList deletes all chunks for a knowledge list
	DeleteByKnowledgeList(ctx context.Context, tenantID uint64, knowledgeIDs []string) error
	// DeleteChunksByTagID deletes all chunks with the specified tag ID
	// Returns the IDs of deleted chunks for index cleanup
	DeleteChunksByTagID(ctx context.Context, tenantID uint64, kbID string, tagID string, excludeIDs []string) ([]string, error)
	// CountChunksByKnowledgeBaseID counts the number of chunks in a knowledge base.
	CountChunksByKnowledgeBaseID(ctx context.Context, tenantID uint64, kbID string) (int64, error)
	// DeleteUnindexedChunks deletes unindexed chunks by knowledge id and chunk index range
	DeleteUnindexedChunks(ctx context.Context, tenantID uint64, knowledgeID string) ([]*types.Chunk, error)
	// ListAllFAQChunksByKnowledgeID lists all FAQ chunks for a knowledge ID
	// only ID and ContentHash fields for efficiency
	ListAllFAQChunksByKnowledgeID(ctx context.Context, tenantID uint64, knowledgeID string) ([]*types.Chunk, error)
	// ListAllFAQChunksWithMetadataByKnowledgeBaseID lists all FAQ chunks for a knowledge base ID
	// returns ID and Metadata fields for duplicate question checking
	ListAllFAQChunksWithMetadataByKnowledgeBaseID(ctx context.Context, tenantID uint64, kbID string) ([]*types.Chunk, error)
	// ListAllFAQChunksForExport lists all FAQ chunks for export with full metadata, tag_id, is_enabled, and flags
	ListAllFAQChunksForExport(ctx context.Context, tenantID uint64, knowledgeID string) ([]*types.Chunk, error)
	// UpdateChunkFlagsBatch updates flags for multiple chunks in batch using a single SQL statement.
	// setFlags: map of chunk ID to flags to set (OR operation)
	// clearFlags: map of chunk ID to flags to clear (AND NOT operation)
	UpdateChunkFlagsBatch(ctx context.Context, tenantID uint64, kbID string, setFlags map[string]types.ChunkFlags, clearFlags map[string]types.ChunkFlags) error
	// UpdateChunkFieldsByTagID updates fields for all chunks with the specified tag ID.
	// Supports updating is_enabled, flags, and tag_id fields.
	// newTagID: if not nil, updates tag_id to this value (empty string means uncategorized)
	UpdateChunkFieldsByTagID(ctx context.Context, tenantID uint64, kbID string, tagID string, isEnabled *bool, setFlags types.ChunkFlags, clearFlags types.ChunkFlags, newTagID *string, excludeIDs []string) ([]string, error)
	// FAQChunkDiff compares FAQ chunks between two knowledge bases and returns the differences.
	// Returns: chunksToAdd (content_hash in src but not in dst), chunksToDelete (content_hash in dst but not in src)
	FAQChunkDiff(ctx context.Context, srcTenantID uint64, srcKBID string, dstTenantID uint64, dstKBID string) (chunksToAdd []string, chunksToDelete []string, err error)
}

// ChunkService defines the interface for chunk service operations
type ChunkService interface {
	// CreateChunks creates chunks
	CreateChunks(ctx context.Context, chunks []*types.Chunk) error
	// GetChunkByID gets a chunk by id
	GetChunkByID(ctx context.Context, id string) (*types.Chunk, error)
	// ListChunksByKnowledgeID lists chunks by knowledge id
	ListChunksByKnowledgeID(ctx context.Context, knowledgeID string) ([]*types.Chunk, error)
	// ListPagedChunksByKnowledgeID lists paged chunks by knowledge id
	ListPagedChunksByKnowledgeID(
		ctx context.Context,
		knowledgeID string,
		page *types.Pagination,
		chunkType []types.ChunkType,
	) (*types.PageResult, error)
	// UpdateChunk updates a chunk
	UpdateChunk(ctx context.Context, chunk *types.Chunk) error
	// UpdateChunks updates chunks in batch
	UpdateChunks(ctx context.Context, chunks []*types.Chunk) error
	// DeleteChunk deletes a chunk
	DeleteChunk(ctx context.Context, id string) error
	// DeleteChunks deletes chunks by IDs in batch
	DeleteChunks(ctx context.Context, ids []string) error
	// DeleteChunksByKnowledgeID deletes chunks by knowledge id
	DeleteChunksByKnowledgeID(ctx context.Context, knowledgeID string) error
	// DeleteByKnowledgeList deletes all chunks for a knowledge list
	DeleteByKnowledgeList(ctx context.Context, ids []string) error
	// ListChunkByParentID lists chunks by parent id
	ListChunkByParentID(ctx context.Context, tenantID uint64, parentID string) ([]*types.Chunk, error)
	// GetRepository gets the chunk repository
	GetRepository() ChunkRepository
	// DeleteGeneratedQuestion deletes a single generated question from a chunk by question ID
	// This updates the chunk metadata and removes the corresponding vector index
	DeleteGeneratedQuestion(ctx context.Context, chunkID string, questionID string) error
}
