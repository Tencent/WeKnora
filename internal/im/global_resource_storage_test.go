package im

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	appsvc "github.com/Tencent/WeKnora/internal/application/service"
	filesvc "github.com/Tencent/WeKnora/internal/application/service/file"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

func configureIMGlobalOSS(t *testing.T, f *imResourceStorageFixture) interfaces.FileService {
	t.Helper()
	for name, value := range map[string]string{
		"STORAGE_TYPE": "oss", "OSS_ENDPOINT": f.baseURL, "OSS_REGION": "cn-hangzhou",
		"OSS_BUCKET_NAME": "global-artifacts", "OSS_ACCESS_KEY": "global-key", "OSS_SECRET_KEY": "test-secret",
		"OSS_PATH_PREFIX": "", "OSS_TEMP_BUCKET_NAME": "", "OSS_TEMP_REGION": "",
	} {
		t.Setenv(name, value)
	}
	repo := repository.NewStorageBackendRepository(f.db)
	f.catalog = appsvc.NewResourceCatalogWithStorage(repository.NewResourceRepository(f.db), repo)
	f.storage = appsvc.NewStorageBackendService(repo, f.db, f.catalog)
	driver, err := filesvc.NewOssFileService(f.baseURL, "cn-hangzhou", "global-key", "test-secret",
		"global-artifacts", "weknora/")
	require.NoError(t, err)
	f.global = filesvc.NewGlobalResourceCatalogFileService(driver, f.catalog)
	return driver
}

func TestIMGlobalResourcesKeepTheirActualStorage(t *testing.T) {
	for _, workspaceProvider := range []string{"local", "oss"} {
		for _, origin := range []string{"artifact", "temporary document", "legacy artifact"} {
			t.Run(workspaceProvider+"/"+origin, func(t *testing.T) {
				f := newIMResourceStorageFixture(t)
				driver := configureIMGlobalOSS(t, f)
				backend := &types.StorageBackend{
					ID: "workspace-default", TenantID: 7, Name: "Workspace default", Provider: workspaceProvider,
					LegacyAlias: true,
					Config: types.StorageBackendConfig{
						Endpoint: f.baseURL, Region: "cn-hangzhou", BucketName: "workspace-bucket",
						AccessKeyID: "workspace-key", SecretAccessKey: "workspace-secret",
					},
				}
				require.NoError(t, repository.NewStorageBackendRepository(f.db).Create(f.context(), backend))
				require.NoError(t, f.db.Model(&types.Tenant{}).Where("id = ?", 7).
					Update("default_storage_backend_id", backend.ID).Error)
				writer := f.global
				if origin == "legacy artifact" {
					writer = filesvc.NewResourceCatalogFileService(driver, f.catalog)
				}
				ref, err := writer.SaveBytes(f.context(), []byte("generated content"), 7, "report.png",
					origin == "temporary document")
				require.NoError(t, err)
				resource, err := f.catalog.Resolve(f.context(), ref)
				require.NoError(t, err)
				if origin == "legacy artifact" {
					require.Empty(t, resource.StorageBackendID)
				} else {
					require.NotEmpty(t, resource.StorageBackendID, "new writes must record the actual instance")
				}
				s, _, _, _ := newFullOutputHarness("")
				f.wire(s)
				content := "![report](" + ref + ")"
				check := func() {
					out := cleanIMContent(f.context(), content, s.newOutboundFileServiceResolver(f.tenant, nil, nil))
					require.NotContains(t, out, "resource://")
					u, err := url.Parse(strings.TrimSuffix(strings.TrimPrefix(out, "![report]("), ")"))
					require.NoError(t, err)
					require.Contains(t, u.Path, "global-artifacts/weknora/7/exports/")
					require.Contains(t, u.Query().Get("x-oss-credential"), "global-key/")
					require.NotEmpty(t, u.Query().Get("x-oss-signature"))
				}
				check()
				resource, err = f.catalog.Resolve(f.context(), ref)
				require.NoError(t, err)
				require.NotEmpty(t, resource.StorageBackendID, "legacy resources acquire a concrete binding")
				require.NotEqual(t, backend.ID, resource.StorageBackendID)

				// Simulate a restart with a different process-wide driver. The prior
				// file must retain the instance that actually stores its bytes.
				t.Setenv("STORAGE_TYPE", "local")
				repo := repository.NewStorageBackendRepository(f.db)
				f.catalog = appsvc.NewResourceCatalogWithStorage(repository.NewResourceRepository(f.db), repo)
				f.storage = appsvc.NewStorageBackendService(repo, f.db, f.catalog)
				f.global = filesvc.NewGlobalResourceCatalogFileService(
					filesvc.NewLocalFileService(t.TempDir(), ""), f.catalog)
				f.wire(s)
				check()
			})
		}
	}
}

func TestIMLegacyResourceDoesNotGuessBetweenEndpoints(t *testing.T) {
	f := newIMResourceStorageFixture(t)
	driver := configureIMGlobalOSS(t, f)
	legacy := filesvc.NewResourceCatalogFileService(driver, f.catalog)
	ref, err := legacy.SaveBytes(f.context(), []byte("legacy content"), 7, "image.png", false)
	require.NoError(t, err)
	require.NoError(t, f.db.Create(&types.StorageBackend{
		ID: "ambiguous", TenantID: 7, Name: "Same bucket elsewhere", Provider: "oss",
		Config: types.StorageBackendConfig{
			Endpoint: "https://another.example", Region: "cn-hangzhou", BucketName: "global-artifacts",
			AccessKeyID: "different-key", SecretAccessKey: "different-secret",
		},
	}).Error)
	s, _, _, _ := newFullOutputHarness("")
	f.wire(s)
	content := fmt.Sprintf("![legacy](%s)", ref)
	out := cleanIMContent(f.context(), content, s.newOutboundFileServiceResolver(f.tenant, nil, nil))
	require.Equal(t, content, out)
	resource, err := f.catalog.Resolve(f.context(), ref)
	require.NoError(t, err)
	require.Empty(t, resource.StorageBackendID)
}
