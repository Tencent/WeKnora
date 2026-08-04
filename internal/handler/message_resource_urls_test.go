package handler

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// stubResourceFileService resolves any storage reference to one fixed public URL.
type stubResourceFileService struct {
	interfaces.FileService
	url string
}

func (s *stubResourceFileService) GetFileURL(context.Context, string) (string, error) {
	return s.url, nil
}

func (s *stubResourceFileService) SaveFile(
	context.Context, *multipart.FileHeader, uint64, string,
) (string, error) {
	return "", nil
}

func (s *stubResourceFileService) GetFile(context.Context, string) (io.ReadCloser, error) {
	return nil, nil
}

const testResourceHandle = "resource://xifDo7NTSL300Lp1goVutw"

func newResourceURLTestRouter(t *testing.T, messages []*types.Message) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	h := &MessageHandler{
		MessageService: &stubMessageService{
			getRecent: func(context.Context, string, int) ([]*types.Message, error) {
				return messages, nil
			},
		},
		FileService: &stubResourceFileService{url: "https://cdn.example.com/signed.png"},
	}
	r.GET("/messages/:session_id/load", h.LoadMessages)
	return r
}

func loadMessageContent(t *testing.T, router *gin.Engine, query string) string {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/messages/sess1/load"+query, nil))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var body struct {
		Data []struct {
			Content string `json:"content"`
			Images  []struct {
				URL string `json:"url"`
			} `json:"images"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data, 1)
	return body.Data[0].Content
}

// Without the parameter, history must keep returning handles so existing clients
// (including the WeKnora frontend, which proxies through /files) are unaffected.
func TestLoadMessages_DefaultsToHandles(t *testing.T) {
	router := newResourceURLTestRouter(t, []*types.Message{
		{Content: "see ![fig](" + testResourceHandle + ")"},
	})
	assert.Equal(t, "see ![fig]("+testResourceHandle+")", loadMessageContent(t, router, ""))
}

func TestLoadMessages_PublicModeReturnsLoadableURLs(t *testing.T) {
	router := newResourceURLTestRouter(t, []*types.Message{
		{
			Content: "see ![fig](" + testResourceHandle + ")",
			Images:  types.MessageImages{{URL: testResourceHandle}},
		},
	})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(
		http.MethodGet, "/messages/sess1/load?resource_urls=public", nil))
	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	body := w.Body.String()
	assert.Contains(t, body, "https://cdn.example.com/signed.png")
	assert.NotContains(t, body, "resource://")
}

// A typo must be reported rather than silently falling back to handles, so an
// integrator finds the mistake instead of debugging "broken images".
func TestLoadMessages_RejectsInvalidResourceURLMode(t *testing.T) {
	router := newResourceURLTestRouter(t, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(
		http.MethodGet, "/messages/sess1/load?resource_urls=signed", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "resource_urls")
}

// The deployment-wide default lets an operator switch every integration over
// without changing client code.
func TestLoadMessages_DeploymentDefaultAppliesWithoutParameter(t *testing.T) {
	t.Setenv("RESOURCE_URL_MODE", "public")
	router := newResourceURLTestRouter(t, []*types.Message{
		{Content: "see ![fig](" + testResourceHandle + ")"},
	})
	assert.Equal(t, "see ![fig](https://cdn.example.com/signed.png)",
		loadMessageContent(t, router, ""))
}

// An explicit parameter must still win over the deployment default.
func TestLoadMessages_ParameterOverridesDeploymentDefault(t *testing.T) {
	t.Setenv("RESOURCE_URL_MODE", "public")
	router := newResourceURLTestRouter(t, []*types.Message{
		{Content: "see ![fig](" + testResourceHandle + ")"},
	})
	assert.Equal(t, "see ![fig]("+testResourceHandle+")",
		loadMessageContent(t, router, "?resource_urls=handle"))
}
