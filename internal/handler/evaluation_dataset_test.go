package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubEvaluationDatasetRegistry struct {
	datasets      []*types.EvaluationDataset
	versions      map[string][]*types.EvaluationDatasetVersion
	createErr     error
	versionErr    error
	lastTenant    uint64
	lastDatasetID string
}

func (s *stubEvaluationDatasetRegistry) ImportDataset(
	ctx context.Context, tenantID uint64, input *types.EvaluationDatasetImportInput,
) (*types.EvaluationDatasetImportResult, error) {
	dataset, err := s.CreateDataset(ctx, tenantID, input.Name, input.Description)
	if err != nil {
		return nil, err
	}
	version, err := s.CreateVersion(ctx, tenantID, dataset.ID, input.Content)
	if err != nil {
		return nil, err
	}
	return &types.EvaluationDatasetImportResult{Dataset: dataset, Version: version}, nil
}

func (s *stubEvaluationDatasetRegistry) CreateDataset(
	_ context.Context, tenantID uint64, name, description string,
) (*types.EvaluationDataset, error) {
	s.lastTenant = tenantID
	if s.createErr != nil {
		return nil, s.createErr
	}
	dataset := &types.EvaluationDataset{
		ID: "dataset-stub", Scope: types.EvaluationDatasetScopeTenant,
		OwnerTenantID: &tenantID, Name: name, Description: description,
	}
	s.datasets = append(s.datasets, dataset)
	return dataset, nil
}

func (s *stubEvaluationDatasetRegistry) CreateVersion(
	_ context.Context, tenantID uint64, datasetID string, content *types.EvaluationDatasetVersionInput,
) (*types.EvaluationDatasetVersion, error) {
	s.lastTenant = tenantID
	s.lastDatasetID = datasetID
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	return &types.EvaluationDatasetVersion{
		ID:             "dataset-version-stub",
		DatasetID:      datasetID,
		VersionNumber:  1,
		SchemaVersion:  types.EvaluationDatasetSchemaVersion,
		ContentSHA256:  types.CanonicalEvaluationDatasetContentSHA256(content),
		PassageCount:   len(content.Passages),
		QuestionCount:  len(content.Questions),
		RelevanceCount: len(content.Relevance),
	}, nil
}

func (s *stubEvaluationDatasetRegistry) GetDataset(
	_ context.Context, tenantID uint64, datasetID string,
) (*types.EvaluationDataset, error) {
	s.lastTenant = tenantID
	s.lastDatasetID = datasetID
	for _, dataset := range s.datasets {
		if dataset.ID == datasetID {
			return dataset, nil
		}
	}
	return nil, interfaces.ErrEvaluationDatasetNotFound
}

func (s *stubEvaluationDatasetRegistry) ListDatasets(
	_ context.Context, tenantID uint64,
) ([]*types.EvaluationDataset, error) {
	s.lastTenant = tenantID
	return s.datasets, nil
}

func (s *stubEvaluationDatasetRegistry) ListVersions(
	_ context.Context, tenantID uint64, datasetID string,
) ([]*types.EvaluationDatasetVersion, error) {
	s.lastTenant = tenantID
	s.lastDatasetID = datasetID
	if s.versionErr != nil {
		return nil, s.versionErr
	}
	return s.versions[datasetID], nil
}

func (s *stubEvaluationDatasetRegistry) GetVersion(
	_ context.Context, _ uint64, datasetVersionID string,
) (*types.EvaluationDatasetVersion, error) {
	for _, versions := range s.versions {
		for _, version := range versions {
			if version.ID == datasetVersionID {
				return version, nil
			}
		}
	}
	return nil, interfaces.ErrEvaluationDatasetVersionNotFound
}

func (s *stubEvaluationDatasetRegistry) GetVersionContent(
	context.Context, uint64, string,
) (*types.EvaluationDatasetVersionContent, error) {
	return nil, interfaces.ErrEvaluationDatasetVersionNotFound
}

func (s *stubEvaluationDatasetRegistry) RegisterBuiltinDataset(
	context.Context, *types.EvaluationBuiltinDatasetRegistration,
) (*types.EvaluationDatasetVersion, error) {
	return nil, interfaces.ErrEvaluationDatasetInvalid
}

func setupEvaluationDatasetRouter(
	registry interfaces.EvaluationDatasetRegistryService,
	limits *config.EvaluationDatasetLimits,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	engine.Use(func(c *gin.Context) {
		c.Set(string(types.TenantIDContextKey), uint64(7))
		c.Next()
	})
	cfg := &config.Config{}
	if limits != nil {
		cfg.Evaluation = &config.EvaluationConfig{Dataset: limits}
	}
	h := NewEvaluationDatasetHandler(cfg, registry)
	engine.POST("/api/v1/evaluation/datasets/import", h.ImportDataset)
	engine.GET("/api/v1/evaluation/datasets/catalog", h.ListCatalog)
	engine.GET("/api/v1/evaluation/datasets/catalog/:id", h.GetCatalogItem)
	engine.POST("/api/v1/evaluation/datasets", h.CreateDataset)
	engine.GET("/api/v1/evaluation/datasets", h.ListDatasets)
	engine.POST("/api/v1/evaluation/datasets/:id/versions", h.CreateVersion)
	engine.GET("/api/v1/evaluation/datasets/:id/versions", h.ListVersions)
	return engine
}

func TestEvaluationDatasetHandlerCreateDataset(t *testing.T) {
	registry := &stubEvaluationDatasetRegistry{}
	engine := setupEvaluationDatasetRouter(registry, nil)

	body := strings.NewReader(`{"name":"Tenant dataset","description":"demo"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/datasets", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, uint64(7), registry.lastTenant)
	var envelope struct {
		Success bool                     `json:"success"`
		Data    *types.EvaluationDataset `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.True(t, envelope.Success)
	require.NotNil(t, envelope.Data)
	assert.Equal(t, "Tenant dataset", envelope.Data.Name)
}

func TestEvaluationDatasetHandlerCreateVersion(t *testing.T) {
	registry := &stubEvaluationDatasetRegistry{}
	engine := setupEvaluationDatasetRouter(registry, nil)

	body := strings.NewReader(`{
		"passages":[{"pid":"p1","content":"passage one"}],
		"questions":[{"qid":"q1","question":"question?","answer":"answer"}],
		"relevance":[{"qid":"q1","pid":"p1","grade":1}]
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/datasets/dataset-1/versions", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	assert.Equal(t, "dataset-1", registry.lastDatasetID)
	var envelope struct {
		Success bool                            `json:"success"`
		Data    *types.EvaluationDatasetVersion `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data)
	assert.Equal(t, "dataset-version-stub", envelope.Data.ID)
	assert.Len(t, envelope.Data.ContentSHA256, 64)
}

func TestEvaluationDatasetHandlerRejectsOversizedBody(t *testing.T) {
	registry := &stubEvaluationDatasetRegistry{}
	limits := config.DefaultEvaluationDatasetLimits()
	limits.MaxRequestBodyBytes = 64
	engine := setupEvaluationDatasetRouter(registry, &limits)

	body := strings.NewReader(fmt.Sprintf(`{"passages":[{"pid":"p1","content":"%s"}]}`,
		strings.Repeat("x", 256)))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/datasets/dataset-1/versions", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
}

func TestEvaluationDatasetHandlerMapsTypedErrors(t *testing.T) {
	cases := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{"limit", interfaces.ErrEvaluationDatasetLimitExceeded, http.StatusRequestEntityTooLarge},
		{"unknown dataset", interfaces.ErrEvaluationDatasetNotFound, http.StatusNotFound},
		{"conflict", interfaces.ErrEvaluationDatasetVersionConflict, http.StatusConflict},
		{"invalid", interfaces.ErrEvaluationDatasetInvalid, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := &stubEvaluationDatasetRegistry{versionErr: tc.serviceErr}
			engine := setupEvaluationDatasetRouter(registry, nil)

			body := bytes.NewReader([]byte(`{"passages":[],"questions":[],"relevance":[]}`))
			request := httptest.NewRequest(http.MethodPost, "/api/v1/evaluation/datasets/dataset-1/versions", body)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			assert.Equal(t, tc.wantStatus, response.Code)
		})
	}
}

func TestEvaluationDatasetHandlerListEndpoints(t *testing.T) {
	registry := &stubEvaluationDatasetRegistry{
		datasets: []*types.EvaluationDataset{{ID: "dataset-1", Scope: types.EvaluationDatasetScopeSystem, Name: "sys"}},
		versions: map[string][]*types.EvaluationDatasetVersion{
			"dataset-1": {{ID: "v1", DatasetID: "dataset-1", VersionNumber: 1}},
		},
	}
	engine := setupEvaluationDatasetRouter(registry, nil)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/datasets", nil))
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), "dataset-1")

	response = httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/evaluation/datasets/dataset-1/versions",
		nil,
	)
	engine.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	assert.Contains(t, response.Body.String(), `"v1"`)
}

func TestEvaluationDatasetHandlerRequiresTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	h := NewEvaluationDatasetHandler(&config.Config{}, &stubEvaluationDatasetRegistry{})
	engine.GET("/api/v1/evaluation/datasets", h.ListDatasets)

	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/datasets", nil))
	assert.Equal(t, http.StatusUnauthorized, response.Code)
}
