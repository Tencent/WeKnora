package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// rerankModelFake serves just the model lookups resolveRerankModelID needs on
// the no-request path: ListModels for the tenant-wide auto-detection.
type rerankModelFake struct {
	interfaces.ModelService
	models  []*types.Model
	listErr error
}

func (f *rerankModelFake) ListModels(context.Context) ([]*types.Model, error) {
	return f.models, f.listErr
}

func rerankFixtureModel(id string, isDefault bool) *types.Model {
	return &types.Model{
		ID:        id,
		Name:      id,
		Type:      types.ModelTypeRerank,
		Status:    types.ModelStatusActive,
		IsDefault: isDefault,
	}
}

func tenantInfoWithRerank(modelID string) *types.Tenant {
	return &types.Tenant{
		RetrievalConfig: &types.RetrievalConfig{RerankModelID: modelID},
	}
}

// The QA pipeline must resolve the rerank model the same way the retrieval
// API does: tenant RetrievalConfig first, then the tenant's first active
// rerank model (issue #1800 — the pipeline skipped with "empty_model_id"
// while the search API reranked the same tenant setting).
func TestResolveKnowledgeQARerankModel(t *testing.T) {
	svc := &sessionService{modelService: &rerankModelFake{models: []*types.Model{
		rerankFixtureModel("chat-1", false),
		rerankFixtureModel("rerank-2", true),
	}}}

	t.Run("tenant config wins", func(t *testing.T) {
		ctx := context.WithValue(context.Background(), types.TenantInfoContextKey, tenantInfoWithRerank("tenant-rerank"))
		require.Equal(t, "tenant-rerank", svc.resolveKnowledgeQARerankModel(ctx, ""))
	})

	t.Run("auto-detects first rerank model without tenant config", func(t *testing.T) {
		require.Equal(t, "rerank-2", svc.resolveKnowledgeQARerankModel(context.Background(), ""))
	})

	t.Run("existing value passes through untouched", func(t *testing.T) {
		// Agent overrides set the model; resolution must not second-guess.
		require.Equal(t, "agent-rerank", svc.resolveKnowledgeQARerankModel(context.Background(), "agent-rerank"))
	})

	t.Run("no rerank model stays empty", func(t *testing.T) {
		svc := &sessionService{modelService: &rerankModelFake{models: []*types.Model{
			rerankFixtureModel("chat-1", false),
		}}}
		require.Equal(t, "", svc.resolveKnowledgeQARerankModel(context.Background(), ""))
	})

	t.Run("list failure stays empty without erroring", func(t *testing.T) {
		svc := &sessionService{modelService: &rerankModelFake{listErr: errors.New("down")}}
		require.Equal(t, "", svc.resolveKnowledgeQARerankModel(context.Background(), ""))
	})
}
