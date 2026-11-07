package storageurl

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type resolverResourceCatalog struct {
	interfaces.ResourceCatalog
	resources map[string]*types.StoredResource
}

func (c *resolverResourceCatalog) Resolve(_ context.Context, ref string) (*types.StoredResource, error) {
	resource := c.resources[ref]
	if resource == nil {
		return nil, errors.New("resource not found")
	}
	return resource, nil
}

func (c *resolverResourceCatalog) ResolvePath(ctx context.Context, ref string) (string, *types.StoredResource, error) {
	if _, ok := types.ParseResourcePath(ref); !ok {
		return ref, nil, nil
	}
	resource, err := c.Resolve(ctx, ref)
	if err != nil {
		return "", nil, err
	}
	return resource.PhysicalPath, resource, nil
}

func (c *resolverResourceCatalog) CreateAccessGrant(context.Context, string, time.Duration) (string, error) {
	return "resource-grant", nil
}

type resolverTenantService struct {
	interfaces.TenantService
	get func(uint64) (*types.Tenant, error)
}

func (s *resolverTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return s.get(id)
}

type resolverStorageService struct {
	interfaces.StorageBackendResolver
	resolve func(context.Context, *types.Tenant, string, string) (interfaces.FileService, string, error)
}

func (s *resolverStorageService) ResolveFileService(
	ctx context.Context,
	tenant *types.Tenant,
	backendID, provider, _ string,
) (interfaces.FileService, string, error) {
	return s.resolve(ctx, tenant, backendID, provider)
}

func TestResourceURLsUseBoundStorageDespiteGlobalDefault(t *testing.T) {
	for _, globalType := range []string{"local", "minio", "oss"} {
		t.Run(globalType, func(t *testing.T) {
			t.Setenv("STORAGE_TYPE", globalType)
			t.Setenv("APP_EXTERNAL_URL", "")
			catalog := &resolverResourceCatalog{resources: map[string]*types.StoredResource{}}
			for i, backendID := range []string{"oss-a", "oss-b", "oss-a"} {
				ref := types.BuildResourcePath(fmt.Sprintf("Handle%016d", i))
				catalog.resources[ref] = &types.StoredResource{
					TenantID: 7, StorageBackendID: backendID, Provider: "oss",
					PhysicalPath: fmt.Sprintf("storage://%s/oss://bucket-%s/7/exports/%d.png", backendID, backendID, i),
				}
			}
			global := &stubFileService{}
			lookups := map[string]int{}
			storage := &resolverStorageService{resolve: func(
				_ context.Context,
				tenant *types.Tenant,
				backendID, provider string,
			) (interfaces.FileService, string, error) {
				require.Equal(t, uint64(7), tenant.ID)
				require.Equal(t, "oss", provider)
				lookups[backendID]++
				driver := &stubFileService{getFileURL: func(_ context.Context, path string) (string, error) {
					require.Contains(t, path, "oss://bucket-"+backendID+"/")
					return "https://" + backendID + ".example/image.png", nil
				}}
				scoped := filesvc.NewBackendScopedFileService(backendID, driver)
				return filesvc.NewResourceCatalogFileService(scoped, catalog), provider, nil
			}}
			resolver := NewFileServiceResolver(&types.Tenant{ID: 7}, global, storage).WithResources(catalog, nil)
			w := NewRewriter(resolver, "TEST")
			for i, backendID := range []string{"oss-a", "oss-b", "oss-a"} {
				ref := types.BuildResourcePath(fmt.Sprintf("Handle%016d", i))
				require.Equal(t, "![image](https://"+backendID+".example/image.png)",
					w.String(context.Background(), "![image]("+ref+")"))
			}
			require.Zero(t, global.calls, "bound resources must never use the global driver")
			require.Equal(t, map[string]int{"oss-a": 1, "oss-b": 1}, lookups,
				"cache by storage instance, not just provider")
		})
	}
}

func TestResourceURLsUseOwnerTenantAndPreserveShortLinks(t *testing.T) {
	for _, externalURL := range []string{"", "https://weknora.example"} {
		t.Run(externalURL, func(t *testing.T) {
			t.Setenv("APP_EXTERNAL_URL", externalURL)
			const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
			catalog := &resolverResourceCatalog{resources: map[string]*types.StoredResource{
				ref: {
					TenantID: 9, StorageBackendID: "owner-oss", Provider: "oss",
					PhysicalPath: "storage://owner-oss/oss://bucket/9/image.png",
				},
			}}
			owner := &types.Tenant{ID: 9}
			tenants := &resolverTenantService{get: func(id uint64) (*types.Tenant, error) {
				require.Equal(t, owner.ID, id)
				return owner, nil
			}}
			global := &stubFileService{}
			storage := &resolverStorageService{resolve: func(
				_ context.Context,
				tenant *types.Tenant,
				backendID, provider string,
			) (interfaces.FileService, string, error) {
				require.Same(t, owner, tenant)
				require.Equal(t, "owner-oss", backendID)
				require.Equal(t, "oss", provider)
				driver := &stubFileService{getFileURL: func(context.Context, string) (string, error) {
					return "https://owner-oss.example/image.png", nil
				}}
				return filesvc.NewResourceCatalogFileService(driver, catalog), provider, nil
			}}
			resolver := NewFileServiceResolver(&types.Tenant{ID: 7}, global, storage).WithResources(catalog, tenants)
			want := "https://owner-oss.example/image.png"
			if externalURL != "" {
				want = externalURL + "/r/resource-grant"
			}
			require.Equal(t, want, NewRewriter(resolver, "TEST").Ref(context.Background(), ref))
			require.Zero(t, global.calls)
		})
	}
}

func TestResourceURLResolutionFailureDoesNotFallBackToGlobal(t *testing.T) {
	const ref = "resource://AbCdEfGhIjKlMnOpQrStUv"
	failures := []string{"resource missing", "owner missing", "backend missing", "backend disabled", "resolver missing"}
	for _, failure := range failures {
		t.Run(failure, func(t *testing.T) {
			t.Setenv("APP_EXTERNAL_URL", "")
			catalog := &resolverResourceCatalog{resources: map[string]*types.StoredResource{}}
			if failure != "resource missing" {
				catalog.resources[ref] = &types.StoredResource{
					TenantID: 9, StorageBackendID: "bound-oss", Provider: "oss",
					PhysicalPath: "storage://bound-oss/oss://bucket/9/image.png",
				}
			}
			tenants := &resolverTenantService{get: func(id uint64) (*types.Tenant, error) {
				if failure == "owner missing" {
					return nil, errors.New(failure)
				}
				return &types.Tenant{ID: id}, nil
			}}
			storage := &resolverStorageService{resolve: func(
				context.Context, *types.Tenant, string, string,
			) (interfaces.FileService, string, error) {
				return nil, "", errors.New(failure)
			}}
			global := &stubFileService{}
			var backends interfaces.StorageBackendResolver = storage
			if failure == "resolver missing" {
				backends = nil
			}
			resolver := NewFileServiceResolver(&types.Tenant{ID: 7}, global, backends).WithResources(catalog, tenants)
			require.Equal(t, ref, NewRewriter(resolver, "TEST").Ref(context.Background(), ref))
			require.Zero(t, global.calls)
		})
	}
}

func TestExplicitStorageBackendFailureDoesNotFallBackToGlobal(t *testing.T) {
	global := &stubFileService{}
	storage := &resolverStorageService{resolve: func(
		context.Context, *types.Tenant, string, string,
	) (interfaces.FileService, string, error) {
		return nil, "", errors.New("backend unavailable")
	}}
	resolver := NewFileServiceResolver(&types.Tenant{ID: 7}, global, storage)
	const ref = "storage://bound-oss/oss://bucket/7/image.png"
	require.Equal(t, ref, NewRewriter(resolver, "TEST").Ref(context.Background(), ref))
	require.Zero(t, global.calls)
}
