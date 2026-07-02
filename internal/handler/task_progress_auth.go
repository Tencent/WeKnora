package handler

import (
	"context"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// requireTaskProgressTenant ensures async task progress endpoints only
// return data for tasks created under the caller's tenant. Cross-tenant
// probes are hidden as not-found to avoid confirming task existence.
func requireTaskProgressTenant(ctx context.Context, taskID string) error {
	taskTenantID, err := utils.TaskTenantID(taskID)
	if err != nil {
		return apperrors.NewBadRequestError("invalid task ID")
	}
	callerTenantID, ok := types.TenantIDFromContext(ctx)
	if !ok || callerTenantID == 0 {
		return apperrors.NewUnauthorizedError("Unauthorized")
	}
	if taskTenantID != callerTenantID {
		return apperrors.NewNotFoundError("task not found")
	}
	return nil
}
