package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateModelPreservesOmittedIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name       string
		identity   map[string]any
		wantSource types.ModelSource
		wantType   types.ModelType
	}{
		{
			name:       "SDK request omits both fields",
			wantSource: types.ModelSourceRemote,
			wantType:   types.ModelTypeKnowledgeQA,
		},
		{
			name:       "source update preserves type",
			identity:   map[string]any{"source": "local"},
			wantSource: types.ModelSourceLocal,
			wantType:   types.ModelTypeKnowledgeQA,
		},
		{
			name:       "type update preserves source",
			identity:   map[string]any{"type": "Embedding"},
			wantSource: types.ModelSourceRemote,
			wantType:   types.ModelTypeEmbedding,
		},
		{
			name:       "explicit updates still apply",
			identity:   map[string]any{"source": "local", "type": "Embedding"},
			wantSource: types.ModelSourceLocal,
			wantType:   types.ModelTypeEmbedding,
		},
		{
			name:       "empty fields preserve identity",
			identity:   map[string]any{"source": "", "type": ""},
			wantSource: types.ModelSourceRemote,
			wantType:   types.ModelTypeKnowledgeQA,
		},
		{
			name:       "null fields preserve identity",
			identity:   map[string]any{"source": nil, "type": nil},
			wantSource: types.ModelSourceRemote,
			wantType:   types.ModelTypeKnowledgeQA,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubUpdateModelService{stored: &types.Model{
				ID: "m1", Name: "custom-model", DisplayName: "Old name",
				Source: types.ModelSourceRemote, Type: types.ModelTypeKnowledgeQA,
			}}
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.PUT("/api/v1/models/:id", NewModelHandler(stub).UpdateModel)

			// The CLI uses this SDK request, which has no source or type fields.
			body := []byte(`{
				"name":"custom-model", "display_name":"Renamed", "description":"Updated description",
				"parameters":{}, "is_default":false
			}`)
			if tt.identity != nil {
				var payload map[string]any
				require.NoError(t, json.Unmarshal(body, &payload))
				for key, value := range tt.identity {
					payload[key] = value
				}
				var err error
				body, err = json.Marshal(payload)
				require.NoError(t, err)
			}

			req := httptest.NewRequest(http.MethodPut, "/api/v1/models/m1", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.NotNil(t, stub.updated)
			assert.Equal(t, tt.wantSource, stub.updated.Source)
			assert.Equal(t, tt.wantType, stub.updated.Type)
			assert.Equal(t, "Renamed", stub.updated.DisplayName)
			assert.Equal(t, "Updated description", stub.updated.Description)

			var response struct {
				Success bool        `json:"success"`
				Data    types.Model `json:"data"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
			assert.True(t, response.Success)
			assert.Equal(t, tt.wantSource, response.Data.Source)
			assert.Equal(t, tt.wantType, response.Data.Type)
		})
	}
}
