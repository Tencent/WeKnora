package interfaces

import (
	"context"
	"io"
	"mime/multipart"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

// KnowledgeService defines the interface for knowledge services.
type KnowledgeService interface {
	// CreateKnowledgeFromFile creates knowledge from a file.
	CreateKnowledgeFromFile(
		ctx context.Context,
		kbID string,
		file *multipart.FileHeader,
		metadata map[string]string,
		enableMultimodel *bool,
		customFileName string,
	) (*types.Knowledge, error)
	// CreateKnowledgeFromURL creates knowledge from a URL.
	CreateKnowledgeFromURL(
		ctx context.Context,
		kbID string,
		url string,
		enableMultimodel *bool,
		title string,
	) (*types.Knowledge, error)
	// CreateKnowledgeFromPassage creates knowledge from text passages.
	CreateKnowledgeFromPassage(ctx context.Context, kbID string, passage []string) (*types.Knowledge, error)
	// CreateKnowledgeFromPassageSync creates knowledge from text passages and waits until chunks are indexed.
	CreateKnowledgeFromPassageSync(ctx context.Context, kbID string, passage []string) (*types.Knowledge, error)
	// CreateKnowledgeFromManual creates or saves manual Markdown knowledge content.
	CreateKnowledgeFromManual(
		ctx context.Context,
		kbID string,
		payload *types.ManualKnowledgePayload,
	) (*types.Knowledge, error)
	// GetKnowledgeByID retrieves knowledge by ID.
	GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error)
	// GetKnowledgeBatch retrieves a batch of knowledge by IDs.
	GetKnowledgeBatch(ctx context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error)
	// ListKnowledgeByKnowledgeBaseID lists all knowledge under a knowledge base.
	ListKnowledgeByKnowledgeBaseID(ctx context.Context, kbID string) ([]*types.Knowledge, error)
	// ListPagedKnowledgeByKnowledgeBaseID lists all knowledge under a knowledge base with pagination.
	// When tagID is non-empty, results are filtered by tag_id.
	// When keyword is non-empty, results are filtered by file_name.
	// When fileType is non-empty, results are filtered by file_type or type.
	ListPagedKnowledgeByKnowledgeBaseID(
		ctx context.Context,
		kbID string,
		page *types.Pagination,
		tagID string,
		keyword string,
		fileType string,
	) (*types.PageResult, error)
	// DeleteKnowledge deletes knowledge by ID.
	DeleteKnowledge(ctx context.Context, id string) error
	// GetKnowledgeFile retrieves the file associated with the knowledge.
	GetKnowledgeFile(ctx context.Context, id string) (io.ReadCloser, string, error)
	// UpdateKnowledge updates knowledge information.
	UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error
	// UpdateManualKnowledge updates manual Markdown knowledge content.
	UpdateManualKnowledge(
		ctx context.Context,
		knowledgeID string,
		payload *types.ManualKnowledgePayload,
	) (*types.Knowledge, error)
	// CloneKnowledgeBase clones knowledge to another knowledge base.
	CloneKnowledgeBase(ctx context.Context, srcID, dstID string) error
	// UpdateImageInfo updates image information for a knowledge chunk.
	UpdateImageInfo(ctx context.Context, knowledgeID string, chunkID string, imageInfo string) error
	// ListFAQEntries lists FAQ entries under a FAQ knowledge base.
	// When tagID is non-empty, results are filtered by tag_id on FAQ chunks.
	// searchField: specifies which field to search in ("standard_question", "similar_questions", "answers", "" for all)
	// sortOrder: "asc" for time ascending (updated_at ASC), default is time descending (updated_at DESC)
	ListFAQEntries(
		ctx context.Context,
		kbID string,
		page *types.Pagination,
		tagID string,
		keyword string,
		searchField string,
		sortOrder string,
	) (*types.PageResult, error)
	// UpsertFAQEntries imports or appends FAQ entries asynchronously.
	// Returns task ID (Knowledge ID) for tracking import progress.
	UpsertFAQEntries(ctx context.Context, kbID string, payload *types.FAQBatchUpsertPayload) (string, error)
	// CreateFAQEntry creates a single FAQ entry synchronously.
	CreateFAQEntry(ctx context.Context, kbID string, payload *types.FAQEntryPayload) (*types.FAQEntry, error)
	// GetFAQEntry retrieves a single FAQ entry by ID.
	GetFAQEntry(ctx context.Context, kbID string, entryID string) (*types.FAQEntry, error)
	// UpdateFAQEntry updates a single FAQ entry.
	UpdateFAQEntry(ctx context.Context, kbID string, entryID string, payload *types.FAQEntryPayload) error
	// UpdateFAQEntryFieldsBatch updates multiple fields for FAQ entries in batch.
	// Supports updating is_enabled, is_recommended, tag_id, and other fields in a single call.
	UpdateFAQEntryFieldsBatch(ctx context.Context, kbID string, req *types.FAQEntryFieldsBatchUpdate) error
	// DeleteFAQEntries deletes FAQ entries in batch.
	DeleteFAQEntries(ctx context.Context, kbID string, entryIDs []string) error
	// SearchFAQEntries searches FAQ entries using hybrid search.
	SearchFAQEntries(ctx context.Context, kbID string, req *types.FAQSearchRequest) ([]*types.FAQEntry, error)
	// ExportFAQEntries exports all FAQ entries for a knowledge base as CSV data.
	ExportFAQEntries(ctx context.Context, kbID string) ([]byte, error)
	// UpdateKnowledgeTagBatch updates tag for document knowledge items in batch.
	UpdateKnowledgeTagBatch(ctx context.Context, updates map[string]*string) error
	// UpdateFAQEntryTagBatch updates tag for FAQ entries in batch.
	UpdateFAQEntryTagBatch(ctx context.Context, kbID string, updates map[string]*string) error
	// GetRepository gets the knowledge repository
	GetRepository() KnowledgeRepository
	// ProcessDocument handles Asynq document processing tasks
	ProcessDocument(ctx context.Context, t *asynq.Task) error
	// ProcessFAQImport handles Asynq FAQ import tasks
	ProcessFAQImport(ctx context.Context, t *asynq.Task) error
	// ProcessQuestionGeneration handles Asynq question generation tasks
	ProcessQuestionGeneration(ctx context.Context, t *asynq.Task) error
	// ProcessSummaryGeneration handles Asynq summary generation tasks
	ProcessSummaryGeneration(ctx context.Context, t *asynq.Task) error
	// ProcessKBClone handles Asynq knowledge base clone tasks
	ProcessKBClone(ctx context.Context, t *asynq.Task) error
	// GetKBCloneProgress retrieves the progress of a knowledge base clone task
	GetKBCloneProgress(ctx context.Context, taskID string) (*types.KBCloneProgress, error)
	// SaveKBCloneProgress saves the progress of a knowledge base clone task
	SaveKBCloneProgress(ctx context.Context, progress *types.KBCloneProgress) error
	// GetFAQImportProgress retrieves the progress of an FAQ import task
	GetFAQImportProgress(ctx context.Context, taskID string) (*types.FAQImportProgress, error)
	// SearchKnowledge searches knowledge items by keyword across the tenant.
	// fileTypes: optional list of file extensions to filter by (e.g., ["csv", "xlsx"])
	SearchKnowledge(ctx context.Context, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error)
}

// KnowledgeRepository defines the interface for knowledge repositories.
type KnowledgeRepository interface {
	CreateKnowledge(ctx context.Context, knowledge *types.Knowledge) error
	GetKnowledgeByID(ctx context.Context, tenantID uint64, id string) (*types.Knowledge, error)
	ListKnowledgeByKnowledgeBaseID(ctx context.Context, tenantID uint64, kbID string) ([]*types.Knowledge, error)
	// ListPagedKnowledgeByKnowledgeBaseID lists all knowledge in a knowledge base with pagination.
	// When tagID is non-empty, results are filtered by tag_id.
	// When keyword is non-empty, results are filtered by file_name.
	// When fileType is non-empty, results are filtered by file_type or type.
	ListPagedKnowledgeByKnowledgeBaseID(ctx context.Context,
		tenantID uint64, kbID string, page *types.Pagination, tagID string, keyword string, fileType string,
	) ([]*types.Knowledge, int64, error)
	UpdateKnowledge(ctx context.Context, knowledge *types.Knowledge) error
	// UpdateKnowledgeBatch updates knowledge items in batch
	UpdateKnowledgeBatch(ctx context.Context, knowledgeList []*types.Knowledge) error
	DeleteKnowledge(ctx context.Context, tenantID uint64, id string) error
	DeleteKnowledgeList(ctx context.Context, tenantID uint64, ids []string) error
	GetKnowledgeBatch(ctx context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error)
	// CheckKnowledgeExists checks if knowledge already exists.
	// For file types, check by fileHash or (fileName+fileSize).
	// For URL types, check by URL.
	// Returns whether it exists, the existing knowledge object (if any), and possible error.
	CheckKnowledgeExists(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		params *types.KnowledgeCheckParams,
	) (bool, *types.Knowledge, error)
	// AminusB returns the difference set of A and B.
	AminusB(ctx context.Context, Atenant uint64, A string, Btenant uint64, B string) ([]string, error)
	UpdateKnowledgeColumn(ctx context.Context, id string, column string, value interface{}) error
	// CountKnowledgeByKnowledgeBaseID counts the number of knowledge items in a knowledge base.
	CountKnowledgeByKnowledgeBaseID(ctx context.Context, tenantID uint64, kbID string) (int64, error)
	// CountKnowledgeByStatus counts the number of knowledge items with the specified parse status.
	CountKnowledgeByStatus(ctx context.Context, tenantID uint64, kbID string, parseStatuses []string) (int64, error)
	// SearchKnowledge searches knowledge items by keyword across the tenant.
	// fileTypes: optional list of file extensions to filter by (e.g., ["csv", "xlsx"])
	SearchKnowledge(ctx context.Context, tenantID uint64, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error)
}
