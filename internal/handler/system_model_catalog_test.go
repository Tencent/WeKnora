package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSystemModelCatalogPreviewAndPublish(t *testing.T) {
	gin.SetMode(gin.TestMode)
	snapshot := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(snapshot) })
	t.Setenv("MODELS_CONFIG", filepath.Join(t.TempDir(), "absent.json"))
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "catalog.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	ddl, err := os.ReadFile("../../migrations/sqlite/000031_model_catalog_config.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(ddl)).Error)
	h := &SystemHandler{modelCatalogSvc: service.NewModelCatalogService(repository.NewModelCatalogRepository(db), nil)}
	r := gin.New()
	r.GET("/catalog", h.GetModelCatalog)
	r.POST("/preview", h.PreviewModelCatalog)
	r.PUT("/catalog", h.PublishModelCatalog)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	w := request(http.MethodGet, "/catalog", "")
	require.Equal(t, 200, w.Code)
	var initial service.CatalogState
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &initial))
	payload, _ := json.Marshal(service.CatalogUpdate{
		Version: initial.Version, Baseline: initial.Baseline,
		Overlay: json.RawMessage(`{"providers":{"openai":{"models":[{"id":"gpt-5","context_window":64000}]}}}`),
	})
	w = request(http.MethodPost, "/preview", string(payload))
	require.Equal(t, 200, w.Code)
	var preview service.CatalogState
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &preview))
	require.Zero(t, preview.Version)
	w = request(http.MethodPut, "/catalog", string(payload))
	require.Equal(t, 200, w.Code)
	w = request(http.MethodPut, "/catalog", string(payload))
	require.Equal(t, 409, w.Code)
	invalid, _ := json.Marshal(service.CatalogUpdate{
		Version: 1, Baseline: initial.Baseline,
		Overlay: json.RawMessage(`{"providers":{"openai":{"api_key":"secret"}}}`),
	})
	w = request(http.MethodPost, "/preview", string(invalid))
	require.Equal(t, 400, w.Code)
	require.NotContains(t, w.Body.String(), "\"secret\"")
}
