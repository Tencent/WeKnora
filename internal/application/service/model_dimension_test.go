package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestUpdateModel_DimensionChangeUsageLookupFailureDoesNotPersist(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	stored := &types.Model{
		ID:   "embedding-model",
		Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			Dimension: 768,
		}},
	}
	updated := false
	svc := NewModelService(
		&stubModelRepoForDelete{
			model: stored,
			update: func(*types.Model) error {
				updated = true
				return nil
			},
		},
		&stubKBRepoForModelDelete{usageErr: errors.New("database unavailable")},
		nil, nil, nil, nil,
	)

	err := svc.UpdateModel(ctx, &types.Model{
		ID: stored.ID,
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			Dimension: 1536,
		}},
	})
	require.ErrorContains(t, err, "check embedding model usage")
	require.False(t, updated, "a failed usage lookup must not allow a dimension change")
}

func TestUpdateModel_AllowsDimensionChangeWhenUnreferenced(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	stored := &types.Model{
		ID:   "embedding-model",
		Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			Dimension: 768,
		}},
	}
	updated := false
	svc := NewModelService(
		&stubModelRepoForDelete{
			model: stored,
			update: func(*types.Model) error {
				updated = true
				return nil
			},
		},
		&stubKBRepoForModelDelete{},
		nil, nil, nil, nil,
	)

	err := svc.UpdateModel(ctx, &types.Model{
		ID:   stored.ID,
		Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			Dimension: 1536,
		}},
	})
	require.NoError(t, err)
	require.True(t, updated)
}

func TestUpdateModel_RejectsDimensionChangeWhenReferenced(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	stored := &types.Model{
		ID:   "embedding-model",
		Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			Dimension: 768,
		}},
	}
	updated := false
	svc := NewModelService(
		&stubModelRepoForDelete{
			model: stored,
			update: func(*types.Model) error {
				updated = true
				return nil
			},
		},
		&stubKBRepoForModelDelete{usages: []types.ModelUsageResource{{
			ID: "kb-1", Name: "Knowledge base",
			Bindings: []types.ModelUsageBinding{types.ModelUsageBindingEmbeddingModel},
		}}},
		nil, nil, nil, nil,
	)

	err := svc.UpdateModel(ctx, &types.Model{
		ID:   stored.ID,
		Type: types.ModelTypeEmbedding,
		Parameters: types.ModelParameters{EmbeddingParameters: types.EmbeddingParameters{
			Dimension: 1536,
		}},
	})
	require.Error(t, err)
	require.False(t, updated, "dimension changes must be rejected before persisting")
}
