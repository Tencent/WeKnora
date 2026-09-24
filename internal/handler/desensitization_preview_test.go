package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreviewDesensitizationMasksMobile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/desensitization/preview", PreviewDesensitization)

	body := map[string]any{
		"text": "请拨打13800138000",
		"desensitization_config": map[string]any{
			"entity_types": []string{"cn_mobile"},
			"mask_style":   "replace",
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/desensitization/preview", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &parsed))
	data := parsed["data"].(map[string]any)
	assert.Contains(t, data["text"], "<手机号>")
	assert.NotContains(t, data["text"], "13800138000")
}

func TestPreviewDesensitizationRejectsEmptyText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/desensitization/preview", PreviewDesensitization)
	req := httptest.NewRequest(http.MethodPost, "/desensitization/preview", bytes.NewReader([]byte(`{"text":"  "}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusOK, w.Code)
}
