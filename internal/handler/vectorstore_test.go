package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type vectorStoreConnectionRecorder struct {
	interfaces.VectorStoreService
	envCalls    int
	normalCalls int
	gotStore    types.VectorStore
}

func (s *vectorStoreConnectionRecorder) TestEnvConnection(
	_ context.Context,
	store types.VectorStore,
) (string, error) {
	s.envCalls++
	s.gotStore = store
	return "8.4.10", nil
}

func (s *vectorStoreConnectionRecorder) TestConnection(
	_ context.Context,
	_ types.RetrieverEngineType,
	_ types.ConnectionConfig,
) (string, error) {
	s.normalCalls++
	return "unexpected", nil
}

func TestVectorStoreHandlerTestStoreByIDUsesEnvConnectionPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("RETRIEVE_DRIVER", "mysql")
	t.Setenv("MYSQL_HOST", "mysql.example.test")
	t.Setenv("MYSQL_PORT", "3307")
	t.Setenv("MYSQL_DATABASE", "weknora")
	t.Setenv("MYSQL_USERNAME", "weknora")

	service := &vectorStoreConnectionRecorder{}
	handler := NewVectorStoreHandler(nil, service)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/v1/vector-stores/__env_mysql__/test", nil)
	ctx.Params = gin.Params{{Key: "id", Value: "__env_mysql__"}}
	ctx.Set(types.TenantIDContextKey.String(), uint64(1))

	handler.TestStoreByID(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, true, response["success"])
	assert.Equal(t, "8.4.10", response["version"])
	assert.Equal(t, 1, service.envCalls)
	assert.Zero(t, service.normalCalls)
	assert.Equal(t, "__env_mysql__", service.gotStore.ID)
	assert.Equal(t, types.MySQLRetrieverEngineType, service.gotStore.EngineType)
	assert.Equal(t, "mysql.example.test:3307", service.gotStore.ConnectionConfig.Addr)
}
