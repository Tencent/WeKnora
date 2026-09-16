package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeleteEvaluationSoftDeletesTerminalTask(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(95, "delete-terminal")
	entity.Status = types.EvaluationStatueSuccess
	endTime := entity.StartTime.Add(time.Hour)
	entity.EndTime = &endTime
	entity.LeaseExpiresAt = nil
	repository.register(entity)

	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: entity.OwnerID}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, entity.TenantID)

	require.NoError(t, service.DeleteEvaluation(ctx, entity.ID))
	_, err := repository.GetTask(ctx, entity.TenantID, entity.ID)
	require.ErrorIs(t, err, interfaces.ErrEvaluationTaskNotFound)

	// Repeated delete and missing tasks are idempotent.
	require.NoError(t, service.DeleteEvaluation(ctx, entity.ID))
	require.NoError(t, service.DeleteEvaluation(ctx, "missing-task"))
}

func TestDeleteEvaluationRejectsActiveTask(t *testing.T) {
	repository := newFakeEvaluationTaskRepository()
	entity := newPersistentLifecycleEntity(96, "delete-active")
	entity.Status = types.EvaluationStatueRunning
	repository.register(entity)

	service := &EvaluationService{evaluationTaskRepository: repository, ownerID: entity.OwnerID}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, entity.TenantID)

	err := service.DeleteEvaluation(ctx, entity.ID)
	require.ErrorIs(t, err, interfaces.ErrEvaluationTaskStateConflict)

	stored, getErr := repository.get(entity.TenantID, entity.ID)
	require.NoError(t, getErr)
	assert.False(t, stored.DeletedAt.Valid)
}
