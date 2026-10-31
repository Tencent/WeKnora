package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDocumentProgressHTTPValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &SystemHandler{}
	r := gin.New()
	r.GET("/documents", h.ListDocumentProgress)
	r.GET("/documents/:knowledge_id", h.GetDocumentProgress)
	r.POST("/wiki-start", h.StartDocumentWiki)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/documents?page=0", "", http.StatusBadRequest},
		{http.MethodGet, "/documents?page_size=101", "", http.StatusBadRequest},
		{http.MethodGet, "/documents?status=unknown", "", http.StatusBadRequest},
		{http.MethodGet, "/documents?search=" + strings.Repeat("a", 201), "", http.StatusBadRequest},
		{http.MethodGet, "/documents/" + strings.Repeat("a", 65), "", http.StatusBadRequest},
		{http.MethodPost, "/wiki-start", `{}`, http.StatusBadRequest},
		{http.MethodPost, "/wiki-start", `{"count":101}`, http.StatusBadRequest},
		{http.MethodPost, "/wiki-start", `{"count":2,"knowledge_id":"doc"}`, http.StatusBadRequest},
		{http.MethodPost, "/wiki-start", `{"count":1,"knowledge_id":" doc "}`, http.StatusBadRequest},
		{http.MethodPost, "/wiki-start", `{"count":1.5}`, http.StatusBadRequest},
		{http.MethodPost, "/wiki-start", `{"count":`, http.StatusBadRequest},
		{http.MethodGet, "/documents", "", http.StatusServiceUnavailable},
		{http.MethodGet, "/documents/doc", "", http.StatusServiceUnavailable},
		{http.MethodPost, "/wiki-start", `{"count":1}`, http.StatusServiceUnavailable},
	} {
		t.Run(tc.method+tc.path+tc.body, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
		})
	}
}
