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
	// tagID is optional - when provided, the file will be assigned to the specified tag/category.
	CreateKnowledgeFromFile(
		ctx context.Context,
		kbID string,
		file *multipart.FileHeader,
		metadata map[string]string,
		enableMultimodel *bool,
		customFileName string,
		tagID string,
	) (*types.Knowledge, error)
	// CreateKnowledgeFromURL creates knowledge from a URL.
	// tagID is optional - when provided, the knowledge will be assigned to the specified tag/category.
	CreateKnowledgeFromURL(
		ctx context.Context,
		kbID string,
		url string,
		enableMultimodel *bool,
		title string,
		tagID string,
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
	// GetKnowledgeByID retrieves knowledge by ID (uses tenant from context).
	GetKnowledgeByID(ctx context.Context, id string) (*types.Knowledge, error)
	// GetKnowledgeByIDOnly retrieves knowledge by ID without tenant filter (for permission resolution).
	GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error)
	// GetKnowledgeBatch retrieves a batch of knowledge by IDs.
	GetKnowledgeBatch(ctx context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error)
	// GetKnowledgeBatchWithSharedAccess retrieves knowledge by IDs including items from shared KBs the user has access to.
	GetKnowledgeBatchWithSharedAccess(ctx context.Context, tenantID uint64, ids []string) ([]*types.Knowledge, error)
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
	// DeleteKnowledgeList deletes multiple knowledge entries by IDs.
	DeleteKnowledgeList(ctx context.Context, ids []string) error
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
	// ReparseKnowledge deletes existing document content and re-parses the knowledge asynchronously.
	ReparseKnowledge(ctx context.Context, knowledgeID string) (*types.Knowledge, error)
	// CloneKnowledgeBase clones knowledge to another knowledge base.
	CloneKnowledgeBase(ctx context.Context, srcID, dstID string) error
	// UpdateImageInfo updates image information for a knowledge chunk.
	UpdateImageInfo(ctx context.Context, knowledgeID string, chunkID string, imageInfo string) error
	// ListFAQEntries lists FAQ entries under a FAQ knowledge base.
	// When tagSeqID is non-zero, results are filtered by tag seq_id on FAQ chunks.
	// searchField: specifies which field to search in ("standard_question", "similar_questions", "answers", "" for all)
	// sortOrder: "asc" for time ascending (updated_at ASC), default is time descending (updated_at DESC)
	ListFAQEntries(
		ctx context.Context,
		kbID string,
		page *types.Pagination,
		tagSeqID int64,
		keyword string,
		searchField string,
		sortOrder string,
	) (*types.PageResult, error)
	// UpsertFAQEntries imports or appends FAQ entries asynchronously.
	// When DryRun is true, only validates entries without actually importing.
	// Returns task ID (Knowledge ID) for tracking import progress.
	UpsertFAQEntries(ctx context.Context, kbID string, payload *types.FAQBatchUpsertPayload) (string, error)
	// CreateFAQEntry creates a single FAQ entry synchronously.
	CreateFAQEntry(ctx context.Context, kbID string, payload *types.FAQEntryPayload) (*types.FAQEntry, error)
	// GetFAQEntry retrieves a single FAQ entry by seq_id.
	GetFAQEntry(ctx context.Context, kbID string, entrySeqID int64) (*types.FAQEntry, error)
	// UpdateFAQEntry updates a single FAQ entry.
	UpdateFAQEntry(ctx context.Context, kbID string, entrySeqID int64, payload *types.FAQEntryPayload) (*types.FAQEntry, error)
	// AddSimilarQuestions adds similar questions to a FAQ entry.
	AddSimilarQuestions(ctx context.Context, kbID string, entrySeqID int64, questions []string) (*types.FAQEntry, error)
	// UpdateFAQEntryFieldsBatch updates multiple fields for FAQ entries in batch.
	// Supports updating is_enabled, is_recommended, tag_id, and other fields in a single call.
	UpdateFAQEntryFieldsBatch(ctx context.Context, kbID string, req *types.FAQEntryFieldsBatchUpdate) error
	// DeleteFAQEntries deletes FAQ entries in batch by seq_id.
	DeleteFAQEntries(ctx context.Context, kbID string, entrySeqIDs []int64) error
	// SearchFAQEntries searches FAQ entries using hybrid search.
	SearchFAQEntries(ctx context.Context, kbID string, req *types.FAQSearchRequest) ([]*types.FAQEntry, error)
	// ExportFAQEntries exports all FAQ entries for a knowledge base as CSV data.
	ExportFAQEntries(ctx context.Context, kbID string) ([]byte, error)
	// UpdateKnowledgeTagBatch updates tag for document knowledge items in batch.
	UpdateKnowledgeTagBatch(ctx context.Context, updates map[string]*string) error
	// UpdateFAQEntryTagBatch updates tag for FAQ entries in batch.
	// Key: entry seq_id, Value: tag seq_id (nil to remove tag)
	UpdateFAQEntryTagBatch(ctx context.Context, kbID string, updates map[int64]*int64) error
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
	// ProcessKnowledgeListDelete handles Asynq knowledge list delete tasks
	ProcessKnowledgeListDelete(ctx context.Context, t *asynq.Task) error
	// GetKBCloneProgress retrieves the progress of a knowledge base clone task
	GetKBCloneProgress(ctx context.Context, taskID string) (*types.KBCloneProgress, error)
	// SaveKBCloneProgress saves the progress of a knowledge base clone task
	SaveKBCloneProgress(ctx context.Context, progress *types.KBCloneProgress) error
	// GetFAQImportProgress retrieves the progress of an FAQ import task
	GetFAQImportProgress(ctx context.Context, taskID string) (*types.FAQImportProgress, error)
	// UpdateLastFAQImportResultDisplayStatus updates the display status of FAQ import result
	UpdateLastFAQImportResultDisplayStatus(ctx context.Context, kbID string, displayStatus string) error
	// SearchKnowledge searches knowledge items by keyword across the tenant.
	// fileTypes: optional list of file extensions to filter by (e.g., ["csv", "xlsx"])
	SearchKnowledge(ctx context.Context, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error)
	// SearchKnowledgeForScopes searches knowledge within the given (tenant_id, kb_id) scopes (e.g. for shared agent context).
	SearchKnowledgeForScopes(ctx context.Context, scopes []types.KnowledgeSearchScope, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error)
}

// KnowledgeRepository defines the interface for knowledge repositories.
type KnowledgeRepository interface {
	CreateKnowledge(ctx context.Context, knowledge *types.Knowledge) error
	GetKnowledgeByID(ctx context.Context, tenantID uint64, id string) (*types.Knowledge, error)
	// GetKnowledgeByIDOnly returns knowledge by ID without tenant filter (for permission resolution).
	GetKnowledgeByIDOnly(ctx context.Context, id string) (*types.Knowledge, error)
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
	// SearchKnowledgeInScopes searches knowledge items by keyword within the given (tenant_id, kb_id) scopes (own + shared).
	SearchKnowledgeInScopes(ctx context.Context, scopes []types.KnowledgeSearchScope, keyword string, offset, limit int, fileTypes []string) ([]*types.Knowledge, bool, error)
	// ListIDsByTagID returns all knowledge IDs that have the specified tag ID.
	ListIDsByTagID(ctx context.Context, tenantID uint64, kbID, tagID string) ([]string, error)
}
