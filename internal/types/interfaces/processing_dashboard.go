package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type ProcessingQueueSnapshotReader interface {
	Snapshot(ctx context.Context) (*types.ProcessingQueueSnapshot, error)
}

type ProcessingDashboardService interface {
	GetDashboard(ctx context.Context, filter types.ProcessingDashboardFilter) (*types.ProcessingDashboardResponse, error)
	ListStageItems(ctx context.Context, filter types.ProcessingDashboardFilter, stage types.ProcessingLogicalStage, state types.ProcessingStageState, cursor string, pageSize int) (*types.ProcessingStageItemsResponse, error)
	GetKnowledgeProcessingDetail(ctx context.Context, knowledgeID string, attempt int) (*types.ProcessingKnowledgeDetailResponse, error)
}
