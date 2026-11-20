package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func checkASRAgainst(t *testing.T, status int, body string) (bool, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })

	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	reqBody, _ := json.Marshal(map[string]string{
		"modelName": "qwen3-asr", "baseUrl": srv.URL + "/v1", "provider": "generic",
	})
	c.Request = httptest.NewRequest(http.MethodPost, "/initialization/asr/check", bytes.NewReader(reqBody))
	c.Request.Header.Set("Content-Type", "application/json")

	(&InitializationHandler{}).CheckASRModel(c)

	require.Empty(t, c.Errors)
	var resp struct {
		Data struct {
			Available bool   `json:"available"`
			Message   string `json:"message"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.Data.Available, resp.Data.Message
}

// A security agent or proxy answering 403 means the endpoint was never reached.
func TestCheckASRModelReportsForbiddenAsUnavailable(t *testing.T) {
	available, message := checkASRAgainst(t, http.StatusForbidden,
		`{"code":190001,"status":"403 Forbidden","desc":"blocked"}`)
	require.False(t, available)
	require.Contains(t, message, "访问被拒绝")
}

func TestCheckASRModelSucceedsWithTranscript(t *testing.T) {
	available, message := checkASRAgainst(t, http.StatusOK, `{"text":"Hello. This is a test."}`)
	require.True(t, available)
	require.Contains(t, message, "Hello. This is a test.")
}
