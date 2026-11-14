package session

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCreateSessionKind(t *testing.T) {
	for _, tc := range []struct {
		body, kind string
		status     int
	}{
		{`{}`, "", http.StatusCreated},
		{`{"kind":"assistant"}`, types.SessionKindAssistant, http.StatusCreated},
		{`{"kind":""}`, "", http.StatusCreated},
		{`{"kind":"other"}`, "", http.StatusBadRequest},
		{`{"kind":"Assistant"}`, "", http.StatusBadRequest},
		{`{"kind":" assistant "}`, "", http.StatusBadRequest},
	} {
		t.Run(tc.body, func(t *testing.T) {
			h := handlerWithApprovedDirs()
			response := postSession(t, h, tc.body)
			require.Equal(t, tc.status, response.Code)
			if tc.status == http.StatusCreated {
				require.Equal(t, tc.kind, h.created.Kind)
			} else {
				require.Nil(t, h.created)
			}
		})
	}
}

func TestListSessionsRejectsInvalidKind(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.GET("/sessions", (&Handler{}).GetSessionsByTenant)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sessions?kind=other", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
}
