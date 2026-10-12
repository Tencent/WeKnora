package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeBindCatalog records Bind calls and simulates the parts of the resource
// catalog bindStoredImages depends on. The interface is embedded so unrelated
// methods never run.
type fakeBindCatalog struct {
	interfaces.ResourceCatalog
	resources map[string]*types.StoredResource
	bindErr   map[string]error
	binds     []string // "ref|ownerType|ownerID|relation"
}

func (f *fakeBindCatalog) Resolve(_ context.Context, reference string) (*types.StoredResource, error) {
	resource, ok := f.resources[reference]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", reference)
	}
	return resource, nil
}

func (f *fakeBindCatalog) Bind(_ context.Context, reference, ownerType, ownerID, relation string) error {
	if err, ok := f.bindErr[reference]; ok {
		return err
	}
	f.binds = append(f.binds, reference+"|"+ownerType+"|"+ownerID+"|"+relation)
	return nil
}

// TestBindStoredImagesClaimsExtractedImagesForTheDocument pins the ingestion
// half of #3342: extracted images are registered as resources but were never
// claimed, and the file proxies authorize through exactly those claims.
func TestBindStoredImagesClaimsExtractedImagesForTheDocument(t *testing.T) {
	catalog := &fakeBindCatalog{
		resources: map[string]*types.StoredResource{
			"resource://ownImage":        {ID: "r1", TenantID: 7},
			"resource://secondOwnImage":  {ID: "r2", TenantID: 7},
			"resource://foreignImage":    {ID: "r3", TenantID: 99},
			"resource://bindFailsImage":  {ID: "r4", TenantID: 7},
			"local://images/physicalRef": {ID: "r5", TenantID: 7},
		},
		bindErr: map[string]error{
			"resource://bindFailsImage": fmt.Errorf("boom"),
		},
	}
	s := &knowledgeService{resourceCatalog: catalog}
	knowledge := &types.Knowledge{ID: "doc-1", TenantID: 7}

	s.bindStoredImages(context.Background(), knowledge, []docparser.StoredImage{
		{ServingURL: "resource://ownImage"},
		{ServingURL: "resource://secondOwnImage"},
		{ServingURL: "  "},                         // blank ref is skipped
		{ServingURL: "resource://unknownImage"},    // unresolvable is skipped
		{ServingURL: "resource://foreignImage"},    // cross-workspace is skipped
		{ServingURL: "resource://bindFailsImage"},  // bind error does not abort the loop
		{ServingURL: "local://images/physicalRef"}, // provider:// refs resolve and bind too
	})

	want := []string{
		"resource://ownImage|knowledge|doc-1|extracted_image",
		"resource://secondOwnImage|knowledge|doc-1|extracted_image",
		"local://images/physicalRef|knowledge|doc-1|extracted_image",
	}
	if len(catalog.binds) != len(want) {
		t.Fatalf("binds = %v, want exactly %v", catalog.binds, want)
	}
	for i := range want {
		if catalog.binds[i] != want[i] {
			t.Errorf("bind[%d] = %q, want %q", i, catalog.binds[i], want[i])
		}
	}
}

// TestBindStoredImagesNilCatalogAndEmptyInputs guards the no-op paths: a
// deployment without the resource catalog, or a parse without images, must
// not panic or log noise.
func TestBindStoredImagesNilCatalogAndEmptyInputs(t *testing.T) {
	s := &knowledgeService{}
	s.bindStoredImages(context.Background(), &types.Knowledge{ID: "d", TenantID: 1}, []docparser.StoredImage{
		{ServingURL: "resource://x"},
	})

	catalog := &fakeBindCatalog{resources: map[string]*types.StoredResource{}}
	s2 := &knowledgeService{resourceCatalog: catalog}
	s2.bindStoredImages(context.Background(), &types.Knowledge{ID: "d", TenantID: 1}, nil)
	s2.bindStoredImages(context.Background(), nil, []docparser.StoredImage{{ServingURL: "resource://x"}})
	if len(catalog.binds) != 0 {
		t.Fatalf("binds = %v, want none", catalog.binds)
	}
}
