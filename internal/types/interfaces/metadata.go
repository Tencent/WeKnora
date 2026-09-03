package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type MetadataAutoFillService interface {
	TaskHandler
	Enqueue(ctx context.Context, payload types.MetadataAutoFillPayload) (taskID string, err error)
}

type KnowledgeMetadataService interface {
	ReadSchema(ctx context.Context, knowledgeBaseID string) (*types.MetadataSchema, error)
	ConfigureDefinition(
		ctx context.Context,
		command types.ConfigureMetadataDefinition,
	) (*types.MetadataDefinition, error)
	ArchiveDefinition(ctx context.Context, knowledgeBaseID, definitionID string) error
	ConfigureAutoRule(
		ctx context.Context,
		command types.ConfigureMetadataAutoRule,
	) (*types.MetadataAutoRule, error)
	DeleteAutoRule(ctx context.Context, knowledgeBaseID, definitionID string) error
	ReadDocumentMetadata(ctx context.Context, knowledgeIDs []string) ([]*types.DocumentMetadata, error)
	ValidateDocumentMetadataChanges(
		ctx context.Context,
		knowledgeBaseID string,
		changes []types.MetadataValueChange,
	) error
	ChangeDocumentMetadata(
		ctx context.Context,
		command types.ChangeDocumentMetadata,
	) (*types.DocumentMetadata, error)
	ConfirmDocumentMetadata(
		ctx context.Context,
		command types.ConfirmDocumentMetadata,
	) (*types.DocumentMetadata, error)
	ApplyAutomaticResults(
		ctx context.Context,
		command types.ApplyAutomaticMetadataResults,
	) (*types.ApplyAutomaticMetadataReport, error)
	ResolveDocumentScope(ctx context.Context, query types.MetadataScopeQuery) (types.DocumentScope, error)
}

type KnowledgeMetadataRepository interface {
	CreateDefinition(ctx context.Context, definition *types.MetadataDefinition) error
	GetDefinition(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		definitionID string,
	) (*types.MetadataDefinition, error)
	ListDefinitions(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		includeArchived bool,
	) ([]*types.MetadataDefinition, error)
	UpdateDefinition(ctx context.Context, definition *types.MetadataDefinition) error
	ArchiveDefinition(ctx context.Context, tenantID uint64, knowledgeBaseID, definitionID string) error
	DefinitionHasUsage(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		definitionID string,
	) (bool, error)
	GetAutoRule(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		definitionID string,
		enabledOnly bool,
	) (*types.MetadataAutoRule, error)
	SaveAutoRule(ctx context.Context, rule *types.MetadataAutoRule) error
	CreateDocumentValue(ctx context.Context, value *types.MetadataValue) error
	GetDocumentValue(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		knowledgeID string,
		definitionID string,
	) (*types.MetadataValue, error)
	SaveDocumentValue(ctx context.Context, value *types.MetadataValue, expectedVersion *int) error
	WithTransaction(
		ctx context.Context,
		fn func(ctx context.Context, repo KnowledgeMetadataRepository) error,
	) error
	ListDocumentValues(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		knowledgeIDs []string,
	) ([]*types.MetadataValue, error)
	DeleteDocumentMetadata(ctx context.Context, tenantID uint64, knowledgeIDs []string) error
	DeleteKnowledgeBaseMetadata(ctx context.Context, tenantID uint64, knowledgeBaseID string) error
	ResolveDocumentScope(ctx context.Context, query types.MetadataScopeQuery) (types.DocumentScope, error)
}
