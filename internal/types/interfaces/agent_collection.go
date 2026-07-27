package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type AgentCollectionRepository interface {
	GetProfile(ctx context.Context, tenantID uint64, agentID, userID string) (*types.AgentCollectionProfile, error)
	GetProfileByID(ctx context.Context, profileID string) (*types.AgentCollectionProfile, error)
	ApplyChanges(ctx context.Context, input types.ApplyCollectionChangesInput) (*types.AgentCollectionProfile, error)
	ListProfiles(ctx context.Context, filter types.AgentCollectionProfileFilter) (*types.AgentCollectionProfilePage, error)
	ListProfilesForExport(ctx context.Context, filter types.AgentCollectionProfileFilter, limit int) ([]*types.AgentCollectionProfile, error)
	SummarizeProfiles(ctx context.Context, filter types.AgentCollectionProfileFilter) (*types.AgentCollectionSummary, error)
	ListHistory(ctx context.Context, profileID string, page, pageSize int) (*types.AgentCollectionHistoryPage, error)
	CreateExport(ctx context.Context, export *types.AgentCollectionExport) error
	UpdateExport(ctx context.Context, export *types.AgentCollectionExport) error
	GetExport(ctx context.Context, exportID string) (*types.AgentCollectionExport, error)
	SoftDeleteByAgent(ctx context.Context, agentID string) error
	SoftDeleteByUser(ctx context.Context, userID string) error
	PurgeProfile(ctx context.Context, profileID string) error
}

type AgentCollectionService interface {
	Prepare(ctx context.Context, input types.PrepareCollectionInput) (*types.PreparedCollection, error)
	ApplyStructuredAnswer(ctx context.Context, input types.StructuredCollectionAnswerInput) (*types.AgentCollectionProfile, error)
	ApplyExtractedValues(ctx context.Context, input types.ExtractedCollectionValuesInput) (*types.AgentCollectionProfile, error)
	UpdateAsSystemAdmin(ctx context.Context, input types.SystemAdminCollectionUpdateInput) (*types.AgentCollectionProfile, error)
	ListProfiles(ctx context.Context, filter types.AgentCollectionProfileFilter) (*types.AgentCollectionProfilePage, error)
	ListProfilesForExport(ctx context.Context, filter types.AgentCollectionProfileFilter, limit int) ([]*types.AgentCollectionProfile, error)
	SummarizeProfiles(ctx context.Context, filter types.AgentCollectionProfileFilter) (*types.AgentCollectionSummary, error)
	GetProfileByID(ctx context.Context, profileID string) (*types.AgentCollectionProfile, error)
	ListHistory(ctx context.Context, profileID string, page, pageSize int) (*types.AgentCollectionHistoryPage, error)
	PurgeProfile(ctx context.Context, profileID string) error
	CreateExport(ctx context.Context, export *types.AgentCollectionExport) error
	UpdateExport(ctx context.Context, export *types.AgentCollectionExport) error
	GetExport(ctx context.Context, exportID string) (*types.AgentCollectionExport, error)
}
