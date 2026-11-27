package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEnvironmentOSSRejectsUnknownAuthWithoutStaticFallback(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	t.Setenv("OSS_AUTH_TYPE", "typo")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for key, value := range map[string]string{
		"STORAGE_TYPE": "oss", "OSS_ENDPOINT": server.URL, "OSS_REGION": "cn-hangzhou",
		"OSS_BUCKET_NAME": "bucket", "OSS_ACCESS_KEY": "source-key", "OSS_SECRET_KEY": "source-secret",
	} {
		t.Setenv(key, value)
	}
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.StorageBackend{}))
	require.NoError(t, db.Create(&types.Tenant{ID: 7, Name: "tenant"}).Error)
	repo := repository.NewStorageBackendRepository(db)
	resolver := service.NewStorageBackendService(repo, db)
	tenant := &types.Tenant{ID: 7}
	backend := types.StorageBackendFromEnvironment(7)
	require.NoError(t, repo.Create(context.Background(), backend))
	for _, backendID := range []string{backend.ID, ""} {
		_, _, err := resolver.ResolveFileService(context.Background(), tenant, backendID, "", t.TempDir())
		require.ErrorContains(t, err, "OSS_AUTH_TYPE")
	}
	require.ErrorContains(t, resolver.Test(context.Background(), backend), "OSS_AUTH_TYPE")
	// The same endpoint with a workspace-owned key must ignore deployment auth.
	backend.Source = types.StorageBackendSourceUser
	require.NoError(t, resolver.Test(context.Background(), backend))
}
