package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubModelRepoForDefault struct {
	*stubModelRepoForDelete
	clearDefault func(tenantID uint64, modelType types.ModelType, excludeID string) error
}

func (s *stubModelRepoForDefault) ClearDefaultByType(
	_ context.Context,
	tenantID uint64,
	modelType types.ModelType,
	excludeID string,
) error {
	return s.clearDefault(tenantID, modelType, excludeID)
}

func TestUpdateModelClearsOtherDefaultsOfSameType(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	model := &types.Model{
		ID:        "chat-2",
		TenantID:  7,
		Name:      "chat-2",
		Type:      types.ModelTypeKnowledgeQA,
		Source:    types.ModelSourceRemote,
		IsDefault: true,
	}
	var cleared bool
	repo := &stubModelRepoForDefault{
		stubModelRepoForDelete: &stubModelRepoForDelete{model: model},
		clearDefault: func(tenantID uint64, modelType types.ModelType, excludeID string) error {
			cleared = true
			assert.Equal(t, uint64(7), tenantID)
			assert.Equal(t, types.ModelTypeKnowledgeQA, modelType)
			assert.Equal(t, model.ID, excludeID)
			return nil
		},
	}
	svc := NewModelService(repo, nil, nil, nil, nil, nil)

	require.NoError(t, svc.UpdateModel(ctx, model))
	assert.True(t, cleared)
}
