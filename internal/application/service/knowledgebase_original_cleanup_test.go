package service

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type originalCleanupRepo struct {
	interfaces.KnowledgeBaseRepository
	resource *types.StoredResource
	shared   bool
	marked   int
}

func (r *originalCleanupRepo) FindOriginalForCleanup(context.Context, uint64, string, string) (*types.StoredResource, bool, error) {
	return r.resource, r.shared, nil
}

func (r *originalCleanupRepo) MarkOriginalDeleted(context.Context, uint64, string) error {
	r.marked++
	r.resource.State = types.ResourceStateDeleted
	return nil
}

type originalCleanupBackend struct {
	interfaces.FileService
	deleted []string
	read    []string
	readErr error
}

func (f *originalCleanupBackend) DeleteFile(_ context.Context, path string) error {
	f.deleted = append(f.deleted, path)
	return nil
}

func (f *originalCleanupBackend) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	f.read = append(f.read, path)
	return nil, f.readErr
}

type originalCleanupResolver struct {
	interfaces.StorageBackendResolver
	backend interfaces.FileService
	ids     []string
}

func (r *originalCleanupResolver) ResolveFileService(_ context.Context, _ *types.Tenant, id, provider, _ string) (interfaces.FileService, string, error) {
	r.ids = append(r.ids, id)
	return r.backend, provider, nil
}

func TestCleanupOriginalUsesPinnedBackendAcrossRetries(t *testing.T) {
	path := types.BuildStorageBackendPath("bound", "cos://bucket/region/document.pdf")
	resource := &types.StoredResource{
		ID: "stored", TenantID: 7, StorageBackendID: "bound", Provider: "cos",
		PhysicalPath: path, State: types.ResourceStateActive,
	}
	repo := &originalCleanupRepo{resource: resource}
	backend := &originalCleanupBackend{readErr: errors.New("access denied")}
	resolver := &originalCleanupResolver{backend: backend}
	service := &knowledgeBaseService{repo: repo, storageResolver: resolver}
	tenant := &types.Tenant{ID: 7}
	document := &types.Knowledge{ID: "doc", FilePath: handleRef("a")}

	if err := service.cleanupOriginal(context.Background(), tenant, "kb", document); err == nil || repo.marked != 0 {
		t.Fatalf("unverified original was marked deleted: %v", err)
	}
	backend.readErr = fs.ErrNotExist
	for range 2 {
		if err := service.cleanupOriginal(context.Background(), tenant, "kb", document); err != nil {
			t.Fatalf("retry failed: %v", err)
		}
	}
	if repo.marked != 2 || len(resolver.ids) != 3 || len(backend.deleted) != 3 || len(backend.read) != 3 {
		t.Fatalf("unexpected retry behavior: marked=%d resolved=%v deleted=%v read=%v",
			repo.marked, resolver.ids, backend.deleted, backend.read)
	}
	for i, id := range resolver.ids {
		if id != "bound" || backend.deleted[i] != path || backend.read[i] != path {
			t.Fatalf("incorrect cleanup target: %s %s %s", id, backend.deleted[i], backend.read[i])
		}
	}
}

func TestCleanupOriginalKeepsSharedObject(t *testing.T) {
	ref := handleRef("b")
	repo := &originalCleanupRepo{
		resource: &types.StoredResource{ID: "shared", TenantID: 7, State: types.ResourceStateActive},
		shared:   true,
	}
	resolver := &originalCleanupResolver{}
	catalog := &fakeCatalog{releaseRemaining: map[string]int64{ref: 1}}
	service := &knowledgeBaseService{repo: repo, storageResolver: resolver, resourceCatalog: catalog}
	if err := service.cleanupOriginal(context.Background(), &types.Tenant{ID: 7}, "kb", &types.Knowledge{ID: "doc", FilePath: ref}); err != nil {
		t.Fatal(err)
	}
	if len(resolver.ids) != 0 || repo.marked != 0 || len(catalog.releases) != 1 {
		t.Fatalf("shared original not preserved: resolve=%v marked=%d releases=%v", resolver.ids, repo.marked, catalog.releases)
	}
}
