package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
)

// ModelService defines the model service interface
type ModelService interface {
	// CreateModel creates a model
	CreateModel(ctx context.Context, model *types.Model) error
	// GetModelByID gets a model by ID
	GetModelByID(ctx context.Context, id string) (*types.Model, error)
	// ListModels lists all models
	ListModels(ctx context.Context) ([]*types.Model, error)
	// UpdateModel updates a model
	UpdateModel(ctx context.Context, model *types.Model) error
	// DeleteModel deletes a model
	DeleteModel(ctx context.Context, id string) error
	// GetEmbeddingModel gets an embedding model
	GetEmbeddingModel(ctx context.Context, modelId string) (embedding.Embedder, error)
	// GetEmbeddingModelForTenant gets an embedding model for a specific tenant (for cross-tenant sharing)
	GetEmbeddingModelForTenant(ctx context.Context, modelId string, tenantID uint64) (embedding.Embedder, error)
	// GetRerankModel gets a rerank model
	GetRerankModel(ctx context.Context, modelId string) (rerank.Reranker, error)
	// GetChatModel gets a chat model
	GetChatModel(ctx context.Context, modelId string) (chat.Chat, error)
}

// ModelRepository defines the model repository interface
type ModelRepository interface {
	// Create creates a model
	Create(ctx context.Context, model *types.Model) error
	// GetByID gets a model by ID
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Model, error)
	// List lists all models
	List(
		ctx context.Context,
		tenantID uint64,
		modelType types.ModelType,
		source types.ModelSource,
	) ([]*types.Model, error)
	// Update updates a model
	Update(ctx context.Context, model *types.Model) error
	// Delete deletes a model
	Delete(ctx context.Context, tenantID uint64, id string) error
	// ClearDefaultByType clears the default flag for all models of a specific type
	// optionally excluding a specific model ID.
	ClearDefaultByType(ctx context.Context, tenantID uint, modelType types.ModelType, excludeID string) error
}
