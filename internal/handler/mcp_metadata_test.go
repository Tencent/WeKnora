package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestRefreshMCPMetadataServiceLookup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.MCPService{}))

	repo := repository.NewMCPServiceRepository(db)
	svc := service.NewMCPServiceService(repo, nil, nil)
	h := NewMCPServiceHandler(svc, nil, nil, nil, nil)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
	})
	r.POST("/mcp-services/:id/metadata/refresh", h.RefreshMCPMetadata)
	refresh := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp-services/svc/metadata/refresh", nil))
		return rec
	}

	missing := refresh()
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())

	require.NoError(t, repo.Create(context.Background(), &types.MCPService{ID: "svc", Name: "Orders", TenantID: 1}))
	require.NoError(t, sqlDB.Close())
	unavailable := refresh()
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code, unavailable.Body.String())
	require.NotContains(t, unavailable.Body.String(), "database is closed")
}

func TestMCPMetadataAppErrorDoesNotLeakUpstreamDetails(t *testing.T) {
	notFound := mcpMetadataAppError(types.ErrMCPServiceNotFound, false)
	require.Equal(t, http.StatusNotFound, notFound.HTTPCode)
	require.Equal(t, "MCP service not found", notFound.Message)

	unauthorized := mcpMetadataAppError(types.ErrMCPOAuthPrincipalRequired, true)
	require.Equal(t, http.StatusUnauthorized, unauthorized.HTTPCode)

	forbidden := mcpMetadataAppError(types.ErrMCPSharedRefreshForbidden, true)
	require.Equal(t, http.StatusForbidden, forbidden.HTTPCode)

	conflict := mcpMetadataAppError(types.ErrMCPMetadataConnectionChanged, true)
	require.Equal(t, http.StatusConflict, conflict.HTTPCode)

	unavailable := mcpMetadataAppError(types.ErrMCPMetadataStorage, false)
	require.Equal(t, http.StatusServiceUnavailable, unavailable.HTTPCode)

	for _, refresh := range []bool{false, true} {
		err := errors.Join(types.ErrMCPMetadataConnectionUnverified, errors.New("private database diagnostic"))
		unverified := mcpMetadataAppError(err, refresh)
		require.Equal(t, http.StatusServiceUnavailable, unverified.HTTPCode)
		require.NotContains(t, unverified.Error(), "private database diagnostic")
	}

	refresh := mcpMetadataAppError(errors.New("dial tcp 10.1.2.3:443 https://secret.internal/mcp"), true)
	require.Equal(t, http.StatusBadRequest, refresh.HTTPCode)
	require.Equal(t, "Failed to refresh MCP tools. Check the connection and try again.", refresh.Message)
	require.NotContains(t, refresh.Error(), "secret.internal")
	require.NotContains(t, refresh.Error(), "10.1.2.3")
}
