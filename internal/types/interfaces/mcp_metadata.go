package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPMetadataRepository persists principal-scoped directory snapshots.
type MCPMetadataRepository interface {
	GetMetadata(context.Context, uint64, string, string) (*types.MCPMetadata, error)
	ListMetadataSummaries(context.Context, uint64, []string) ([]*types.MCPMetadataSummary, error)
	SaveMetadata(context.Context, *types.MCPMetadata) error
}

// MCPMetadataService reads and explicitly refreshes directory snapshots.
type MCPMetadataService interface {
	// GetMCPMetadata reads only persisted metadata; nil means never synchronized.
	GetMCPMetadata(context.Context, uint64, string) (*types.MCPMetadata, error)
	// ListMCPMetadataSummaries returns persisted directory counts for the list UI.
	ListMCPMetadataSummaries(context.Context, uint64, []*types.MCPService) (map[string]*types.MCPMetadataSummary, error)
	// PersistMCPMetadata stores a complete directory already listed on an authorized connection.
	PersistMCPMetadata(context.Context, uint64, string, []*types.MCPTool, string) error
	// RefreshMCPMetadata explicitly connects and atomically replaces a complete snapshot.
	RefreshMCPMetadata(context.Context, uint64, string) (*types.MCPMetadata, error)
}
