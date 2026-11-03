package router

import (
 "net/http"
 "net/http/httptest"
 "testing"
 "github.com/Tencent/WeKnora/internal/config"
 "github.com/Tencent/WeKnora/internal/handler"
 "github.com/gin-gonic/gin"
 "github.com/stretchr/testify/require"
)

func TestPlatformInfrastructureMutationDeniedWithoutSystemAdmin(t *testing.T) {
 gin.SetMode(gin.TestMode)
 g := &rbacGuards{cfg: &config.Config{}}
 r := gin.New()
 v1 := r.Group("/api/v1")
 RegisterModelRoutes(v1, &handler.ModelHandler{}, &handler.ModelCredentialsHandler{}, g)
 RegisterStorageBackendRoutes(v1, &handler.StorageBackendHandler{}, g)
 RegisterVectorStoreRoutes(v1, &handler.VectorStoreHandler{}, g)
 for _, tc := range []struct{method,path string}{
  {http.MethodPost,"/api/v1/models"},
  {http.MethodPut,"/api/v1/models/example"},
  {http.MethodPost,"/api/v1/storage-backends"},
  {http.MethodPost,"/api/v1/vector-stores"},
 } {
  t.Run(tc.method+tc.path,func(t *testing.T){
   w:=httptest.NewRecorder()
   r.ServeHTTP(w,httptest.NewRequest(tc.method,tc.path,nil))
   require.Equal(t,http.StatusForbidden,w.Code)
  })
 }
}