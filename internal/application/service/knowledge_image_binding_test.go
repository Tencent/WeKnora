package service

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type knowledgeImageBindingCatalog struct {
	resources map[string]*types.StoredResource
	binds     []knowledgeImageBindCall
}

type knowledgeImageBindCall struct {
	ref       string
	ownerType string
	ownerID   string
	relation  string
}

func (c *knowledgeImageBindingCatalog) Register(context.Context, uint64, string, interfaces.ResourceRegistration) (string, error) {
	return "", nil
}

func (c *knowledgeImageBindingCatalog) Resolve(_ context.Context, reference string) (*types.StoredResource, error) {
	return c.resources[reference], nil
}

func (c *knowledgeImageBindingCatalog) ResolvePath(_ context.Context, value string) (string, *types.StoredResource, error) {
	return value, c.resources[value], nil
}

func (c *knowledgeImageBindingCatalog) Bind(_ context.Context, reference, ownerType, ownerID, relation string) error {
	c.binds = append(c.binds, knowledgeImageBindCall{reference, ownerType, ownerID, relation})
	return nil
}

func (c *knowledgeImageBindingCatalog) Release(context.Context, string, string, string) (int64, error) {
	return 0, nil
}

func (c *knowledgeImageBindingCatalog) MarkDeleted(context.Context, string) error { return nil }

func (c *knowledgeImageBindingCatalog) CreateAccessGrant(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (c *knowledgeImageBindingCatalog) ResolveAccessGrant(context.Context, string) (*types.StoredResource, error) {
	return nil, nil
}

func TestBindKnowledgeImageResourcesBindsOnlySameTenantResourceReferences(t *testing.T) {
	ctx := context.Background()
	imageRef := "resource://image-owned-by-knowledge"
	foreignRef := "resource://image-owned-by-other-tenant"
	catalog := &knowledgeImageBindingCatalog{resources: map[string]*types.StoredResource{
		imageRef:   {ID: "image-1", TenantID: 7},
		foreignRef: {ID: "image-2", TenantID: 8},
	}}

	bindKnowledgeImageResources(ctx, catalog, 7, "knowledge-1", []docparser.StoredImage{
		{ServingURL: imageRef},
		{ServingURL: foreignRef},
		{ServingURL: "local://7/exports/not-a-catalog-resource.png"},
	})

	if len(catalog.binds) != 1 {
		t.Fatalf("bind count = %d, want 1", len(catalog.binds))
	}
	got := catalog.binds[0]
	if got.ref != imageRef || got.ownerType != types.ResourceOwnerKnowledge || got.ownerID != "knowledge-1" || got.relation != types.ResourceRelationExtractedImage {
		t.Fatalf("unexpected bind: %#v", got)
	}
}
