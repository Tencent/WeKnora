// Package interfaces defines the interface contracts between different system components
// Through interface definitions, business logic can be decoupled from specific implementations,
// improving code testability and maintainability
// Knowledge base related interfaces are used to manage knowledge base resources and their contents
package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// KnowledgeBaseService defines the knowledge base service interface
// Provides high-level operations for knowledge base creation, querying, updating, deletion, and content searching
type KnowledgeBaseService interface {
	// CreateKnowledgeBase creates a new knowledge base
	// Parameters:
	//   - ctx: Context information, carrying request tracking, user identity, etc.
	//   - kb: Knowledge base object containing basic information
	// Returns:
	//   - Created knowledge base object (including automatically generated ID)
	//   - Possible errors such as insufficient permissions, duplicate names, etc.
	CreateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error)

	// GetKnowledgeBaseByID retrieves knowledge base information by ID
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the knowledge base
	// Returns:
	//   - Knowledge base object, if found
	//   - Possible errors such as not existing, insufficient permissions, etc.
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)

	// GetKnowledgeBaseByIDOnly retrieves knowledge base by ID without tenant filter
	// Used for cross-tenant shared KB access where permission is checked elsewhere
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the knowledge base
	// Returns:
	//   - Knowledge base object, if found
	//   - Possible errors such as not existing, etc.
	GetKnowledgeBaseByIDOnly(ctx context.Context, id string) (*types.KnowledgeBase, error)

	// GetKnowledgeBasesByIDsOnly retrieves knowledge bases by IDs without tenant filter (batch).
	GetKnowledgeBasesByIDsOnly(ctx context.Context, ids []string) ([]*types.KnowledgeBase, error)

	// FillKnowledgeBaseCounts fills KnowledgeCount, ChunkCount, IsProcessing, ProcessingCount for the given KB (uses kb.TenantID).
	FillKnowledgeBaseCounts(ctx context.Context, kb *types.KnowledgeBase) error

	// ListKnowledgeBases lists all knowledge bases under the current tenant
	// Parameters:
	//   - ctx: Context information, containing tenant information
	// Returns:
	//   - List of knowledge base objects
	//   - Possible errors such as insufficient permissions, etc.
	ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error)
	// ListKnowledgeBasesByTenantID lists all knowledge bases for a specific tenant (e.g. for shared agent context).
	ListKnowledgeBasesByTenantID(ctx context.Context, tenantID uint64) ([]*types.KnowledgeBase, error)

	// UpdateKnowledgeBase updates knowledge base information
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the knowledge base
	//   - name: New knowledge base name
	//   - description: New knowledge base description
	//   - config: Knowledge base configuration, including chunking strategy, vectorization settings, etc.
	// Returns:
	//   - Updated knowledge base object
	//   - Possible errors such as not existing, insufficient permissions, etc.
	UpdateKnowledgeBase(ctx context.Context,
		id string, name string, description string, config *types.KnowledgeBaseConfig,
	) (*types.KnowledgeBase, error)

	// DeleteKnowledgeBase deletes a knowledge base
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the knowledge base
	// Returns:
	//   - Possible errors such as not existing, insufficient permissions, etc.
	DeleteKnowledgeBase(ctx context.Context, id string) error

	// TogglePinKnowledgeBase toggles the pin status of a knowledge base
	TogglePinKnowledgeBase(ctx context.Context, id string) (*types.KnowledgeBase, error)

	// HybridSearch performs hybrid search (vector + keywords) in the knowledge base
	// Parameters:
	//   - ctx: Context information
	//   - id: Unique identifier of the knowledge base
	//   - params: Search parameters, including query text, thresholds, etc.
	// Returns:
	//   - List of search results, sorted by relevance
	//   - Possible errors such as not existing, insufficient permissions, search engine errors, etc.
	HybridSearch(ctx context.Context, id string, params types.SearchParams) ([]*types.SearchResult, error)

	// GetQueryEmbedding computes the query embedding using the embedding model
	// associated with the given knowledge base. This allows callers to pre-compute
	// and reuse embeddings across multiple KBs that share the same model.
	GetQueryEmbedding(ctx context.Context, kbID string, queryText string) ([]float32, error)

	// ResolveEmbeddingModelKeys resolves embedding model IDs to their actual
	// model identity key (name + endpoint). KBs using the same underlying model
	// across different tenants will share the same key, enabling optimal grouping.
	// Returns a map from KB ID to model identity key string.
	ResolveEmbeddingModelKeys(ctx context.Context, kbs []*types.KnowledgeBase) map[string]string

	// CopyKnowledgeBase copies a knowledge base
	// Parameters:
	//   - ctx: Context information
	//   - sourceID: Source knowledge base ID
	//   - targetID: Target knowledge base ID
	// Returns:
	//   - Copied knowledge base object
	//   - Possible errors such as not existing, insufficient permissions, etc.
	CopyKnowledgeBase(ctx context.Context, src string, dst string) (*types.KnowledgeBase, *types.KnowledgeBase, error)

	// GetRepository gets the knowledge base repository
	// Parameters:
	//   - ctx: Context with authentication and request information
	//
	// Returns:
	//   - interfaces.KnowledgeBaseRepository: Knowledge base repository
	GetRepository() KnowledgeBaseRepository

	// ProcessKBDelete handles async knowledge base deletion task
	// Parameters:
	//   - ctx: Context information
	//   - t: Asynq task containing KBDeletePayload
	// Returns:
	//   - Possible errors during deletion
	ProcessKBDelete(ctx context.Context, t *asynq.Task) error
}

// KnowledgeBaseRepository defines the knowledge base repository interface
// Responsible for knowledge base data persistence and retrieval,
// serving as a bridge between the service layer and data storage
type KnowledgeBaseRepository interface {
	// CreateKnowledgeBase creates a knowledge base record
	// Parameters:
	//   - ctx: Context information
	//   - kb: Knowledge base object
	// Returns:
	//   - Possible errors such as database connection failure, unique constraint conflicts, etc.
	CreateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) error

	// GetKnowledgeBaseByID queries a knowledge base by ID
	// Parameters:
	//   - ctx: Context information
	//   - id: Knowledge base ID
	// Returns:
	//   - Knowledge base object, if found
	//   - Possible errors such as record not existing, database errors, etc.
	GetKnowledgeBaseByID(ctx context.Context, id string) (*types.KnowledgeBase, error)

	// GetKnowledgeBaseByIDAndTenant queries a knowledge base by ID scoped to a tenant.
	// Returns ErrKnowledgeBaseNotFound if the KB does not exist or does not belong to the tenant.
	// Parameters:
	//   - ctx: Context information
	//   - id: Knowledge base ID
	//   - tenantID: Tenant ID (enforces tenant isolation)
	// Returns:
	//   - Knowledge base object, if found and owned by tenant
	//   - Possible errors such as record not existing or wrong tenant, database errors, etc.
	GetKnowledgeBaseByIDAndTenant(ctx context.Context, id string, tenantID uint64) (*types.KnowledgeBase, error)

	// GetKnowledgeBaseByIDs queries knowledge bases by multiple IDs
	// Parameters:
	//   - ctx: Context information
	//   - ids: List of knowledge base IDs
	// Returns:
	//   - List of knowledge base objects
	//   - Possible errors such as database errors, etc.
	GetKnowledgeBaseByIDs(ctx context.Context, ids []string) ([]*types.KnowledgeBase, error)

	// ListKnowledgeBases lists all knowledge bases in the system
	// Parameters:
	//   - ctx: Context information
	// Returns:
	//   - List of knowledge base objects
	//   - Possible errors such as database errors, etc.
	ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error)

	// ListKnowledgeBasesByTenantID lists all knowledge bases for a specific tenant
	// Parameters:
	//   - ctx: Context information
	//   - tenantID: Tenant ID
	// Returns:
	//   - List of knowledge base objects
	//   - Possible errors such as database errors, etc.
	ListKnowledgeBasesByTenantID(ctx context.Context, tenantID uint64) ([]*types.KnowledgeBase, error)

	// UpdateKnowledgeBase updates a knowledge base record
	// Parameters:
	//   - ctx: Context information
	//   - kb: Knowledge base object containing update information
	// Returns:
	//   - Possible errors such as record not existing, database errors, etc.
	UpdateKnowledgeBase(ctx context.Context, kb *types.KnowledgeBase) error

	// DeleteKnowledgeBase deletes a knowledge base record
	// Parameters:
	//   - ctx: Context information
	//   - id: Knowledge base ID
	// Returns:
	//   - Possible errors such as record not existing, database errors, etc.
	DeleteKnowledgeBase(ctx context.Context, id string) error

	// CountByVectorStoreID counts active KBs bound to the given vector store
	// within a tenant scope. Accepts a *gorm.DB handle so callers can share a
	// transaction (e.g., the VectorStore delete guard's row-lock context) or
	// run standalone (pass nil → uses the repository's default db).
	//
	// The soft-delete filter is applied automatically by the gorm.DeletedAt
	// scope on KnowledgeBase; implementations MUST NOT add an explicit
	// `deleted_at IS NULL` predicate (avoids divergence with the auto-scope).
	CountByVectorStoreID(ctx context.Context, db *gorm.DB, tenantID uint64, storeID string) (int64, error)

	// CountByModelID counts active KBs in the tenant that reference the given
	// model ID in any model-binding field (embedding, summary, VLM, ASR, etc.).
	CountByModelID(ctx context.Context, tenantID uint64, modelID string) (int64, error)
	// SetUserKBPin inserts or removes a row in user_kb_pins for the given
	// (tenant, user, kb) triple. Returns the resulting pinned_at (nil when
	// pinned=false) and an error. The tenant_id is captured to support
	// efficient "wipe a tenant" cleanups even though (user_id, kb_id)
	// alone would be unique in practice.
	SetUserKBPin(
		ctx context.Context, tenantID uint64, userID string, kbID string, pinned bool,
	) (pinnedAt *time.Time, err error)

	// ListUserKBPinIDs returns the kb_id → pinned_at map of every KB the
	// given user has personally pinned in this tenant. Used by the list
	// path to stamp KnowledgeBase.IsPinned / PinnedAt without a per-row
	// roundtrip.
	ListUserKBPinIDs(
		ctx context.Context, tenantID uint64, userID string,
	) (map[string]time.Time, error)
}
