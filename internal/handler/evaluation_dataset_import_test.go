package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const validDatasetImportJSON = `{"request_id":"d64cd39f-6e20-48b6-991a-360b27cb6b62","name":"dataset",` +
	`"content":{"passages":[{"pid":"p1","content":"passage"}],` +
	`"questions":[{"qid":"q1","question":"question","answer":""}],` +
	`"relevance":[{"qid":"q1","pid":"p1","grade":0}]}}`

func TestEvaluationDatasetImportHandlerRejectsInvalidJSONBeforeService(t *testing.T) {
	cases := map[string]string{
		"truncated":        validDatasetImportJSON[:len(validDatasetImportJSON)-1],
		"trailing":         validDatasetImportJSON + ` {}`,
		"unknown":          strings.Replace(validDatasetImportJSON, `"name":`, `"extra":true,"name":`, 1),
		"case alias":       strings.Replace(validDatasetImportJSON, `"name":`, `"Name":`, 1),
		"duplicate":        strings.Replace(validDatasetImportJSON, `"name":`, `"name":"a","name":`, 1),
		"duplicate nested": strings.Replace(validDatasetImportJSON, `"grade":0`, `"grade":0,"grade":1`, 1),
		"unknown nested":   strings.Replace(validDatasetImportJSON, `"grade":0`, `"grade":0,"unexpected":1`, 1),
		"missing grade":    strings.Replace(validDatasetImportJSON, `,"grade":0`, ``, 1),
		"null grade":       strings.Replace(validDatasetImportJSON, `"grade":0`, `"grade":null`, 1),
		"fraction":         strings.Replace(validDatasetImportJSON, `"grade":0`, `"grade":0.5`, 1),
		"overflow":         strings.Replace(validDatasetImportJSON, `"grade":0`, `"grade":9223372036854775808`, 1),
		"string grade":     strings.Replace(validDatasetImportJSON, `"grade":0`, `"grade":"0"`, 1),
		"null content":     strings.Replace(validDatasetImportJSON, `"content":"passage"`, `"content":null`, 1),
		"NUL":              strings.Replace(validDatasetImportJSON, `"passage"`, `"passage\u0000"`, 1),
		"UTF8":             strings.Replace(validDatasetImportJSON, `passage"`, "passage\xff\"", 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			registry := &stubEvaluationDatasetRegistry{}
			engine := setupEvaluationDatasetRouter(registry, nil)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
				"/api/v1/evaluation/datasets/import", strings.NewReader(body)))
			assert.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			assert.Zero(t, registry.lastTenant, "invalid wire content must not reach the service")
			assert.Empty(t, registry.datasets)
		})
	}
}

func TestEvaluationDatasetImportHandlerSuccessLimitsAndConflict(t *testing.T) {
	for _, test := range []struct {
		name   string
		limit  int64
		err    error
		status int
	}{
		{"success", 4096, nil, http.StatusOK},
		{"body limit", 64, nil, http.StatusRequestEntityTooLarge},
		{"service limit", 4096, interfaces.ErrEvaluationDatasetLimitExceeded, http.StatusRequestEntityTooLarge},
		{"conflict", 4096, interfaces.ErrEvaluationDatasetVersionConflict, http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := &stubEvaluationDatasetRegistry{createErr: test.err}
			limits := config.DefaultEvaluationDatasetLimits()
			limits.MaxRequestBodyBytes = test.limit
			engine := setupEvaluationDatasetRouter(registry, &limits)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost,
				"/api/v1/evaluation/datasets/import", strings.NewReader(validDatasetImportJSON)))
			require.Equal(t, test.status, response.Code, response.Body.String())
			if test.status == http.StatusOK {
				var result struct {
					Data types.EvaluationDatasetImportResult `json:"data"`
				}
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
				require.NotNil(t, result.Data.Dataset)
				require.NotNil(t, result.Data.Version)
				assert.Equal(t, uint64(7), registry.lastTenant)
				assert.False(t, result.Data.Replayed)
			}
		})
	}
}

func TestEvaluationDatasetCatalogHandlerUsesConfiguredLimits(t *testing.T) {
	limits := config.DefaultEvaluationDatasetLimits()
	limits.MaxRequestBodyBytes = 123456
	limits.MaxQuestions = 17
	engine := setupEvaluationDatasetRouter(&stubEvaluationDatasetRegistry{}, &limits)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/evaluation/datasets/catalog", nil))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var result struct {
		Data struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			Limits evaluationDatasetCatalogLimits `json:"limits"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	assert.Equal(t, limits, result.Data.Limits.EvaluationDatasetLimits)
	assert.Equal(t, 255, result.Data.Limits.MaxNameChars)
	require.Len(t, result.Data.Items, 2)
	for _, item := range result.Data.Items {
		response = httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
			"/api/v1/evaluation/datasets/catalog/"+item.ID, nil))
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		assert.Contains(t, response.Body.String(), `"manifest":`)
		assert.Contains(t, response.Body.String(), `"content":`)
	}
	for _, id := range []string{"unknown", "weknora-docs", "https:example.com"} {
		response = httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
			"/api/v1/evaluation/datasets/catalog/"+id, nil))
		assert.Equal(t, http.StatusNotFound, response.Code)
	}
}
