package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type resolverTestModelService struct {
	interfaces.ModelService

	sameTenantModelID  string
	crossTenantModelID string
	crossTenantID      uint64
}

func (s *resolverTestModelService) GetEmbeddingModel(
	_ context.Context,
	modelID string,
) (embedding.Embedder, error) {
	s.sameTenantModelID = modelID
	return resolverTestEmbedder{}, nil
}

func (s *resolverTestModelService) GetEmbeddingModelForTenant(
	_ context.Context,
	modelID string,
	tenantID uint64,
) (embedding.Embedder, error) {
	s.crossTenantModelID = modelID
	s.crossTenantID = tenantID
	return resolverTestEmbedder{}, nil
}

type resolverTestEmbedder struct{}

func (resolverTestEmbedder) Embed(context.Context, string) ([]float32, error) { return nil, nil }
func (resolverTestEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (resolverTestEmbedder) BatchEmbedWithPool(
	context.Context,
	embedding.Embedder,
	[]string,
) ([][]float32, error) {
	return nil, nil
}
func (resolverTestEmbedder) GetModelName() string { return "test" }
func (resolverTestEmbedder) GetDimensions() int   { return 3 }
func (resolverTestEmbedder) GetModelID() string   { return "model" }

func TestGetEmbeddingModelForKBUsesOwnerTenantForCrossTenant(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10001))
	modelSvc := &resolverTestModelService{}
	kb := &types.KnowledgeBase{TenantID: 10000, EmbeddingModelID: "embed-owner"}

	if _, err := getEmbeddingModelForKB(ctx, modelSvc, kb); err != nil {
		t.Fatalf("getEmbeddingModelForKB returned error: %v", err)
	}

	if modelSvc.crossTenantModelID != "embed-owner" {
		t.Fatalf("cross-tenant model ID = %q, want embed-owner", modelSvc.crossTenantModelID)
	}
	if modelSvc.crossTenantID != 10000 {
		t.Fatalf("cross-tenant ID = %d, want 10000", modelSvc.crossTenantID)
	}
	if modelSvc.sameTenantModelID != "" {
		t.Fatalf("same-tenant lookup was called for cross-tenant KB")
	}
}

func TestGetEmbeddingModelForKBUsesContextTenantForSameTenant(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(10000))
	modelSvc := &resolverTestModelService{}
	kb := &types.KnowledgeBase{TenantID: 10000, EmbeddingModelID: "embed-owner"}

	if _, err := getEmbeddingModelForKB(ctx, modelSvc, kb); err != nil {
		t.Fatalf("getEmbeddingModelForKB returned error: %v", err)
	}

	if modelSvc.sameTenantModelID != "embed-owner" {
		t.Fatalf("same-tenant model ID = %q, want embed-owner", modelSvc.sameTenantModelID)
	}
	if modelSvc.crossTenantModelID != "" {
		t.Fatalf("cross-tenant lookup was called for same-tenant KB")
	}
}
