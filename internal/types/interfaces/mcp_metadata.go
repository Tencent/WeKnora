package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// MCPMetadataRepository persists principal-scoped directory snapshots.
type MCPMetadataRepository interface {
	GetMetadata(context.Context, uint64, string, string) (*types.MCPMetadata, error)
	ListMetadataSummaries(context.Context, uint64, []string) ([]*types.MCPMetadataSummary, error)
	// SaveMetadata verifies the source connection and atomically returns the winning snapshot.
	SaveMetadata(ctx context.Context, snapshot *types.MCPMetadata, sourceEnabled bool) (*types.MCPMetadata, error)
}

// MCPMetadataService reads and explicitly refreshes directory snapshots.
type MCPMetadataService interface {
	// GetMCPMetadata reads only persisted metadata; nil means never synchronized.
	GetMCPMetadata(context.Context, uint64, string) (*types.MCPMetadata, error)
	// ListMCPMetadataSummaries returns persisted directory counts for the list UI.
	ListMCPMetadataSummaries(context.Context, uint64, []*types.MCPService) (map[string]*types.MCPMetadataSummary, error)
	// PersistMCPMetadata stores a complete directory already listed on an authorized
	// connection, using the service configuration that opened it and the time
	// the listing started.
	PersistMCPMetadata(
		context.Context, uint64, *types.MCPService, []*types.MCPTool, string, types.MCPServerInfo, time.Time,
	) error
	// RefreshMCPMetadata explicitly connects and atomically replaces a complete snapshot.
	RefreshMCPMetadata(context.Context, uint64, string) (*types.MCPMetadata, error)
}
