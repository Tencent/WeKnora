package session

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type capturingSessionService struct {
	interfaces.SessionService
	owner *hostCreateHandler
}

func (s *capturingSessionService) CreateSession(_ context.Context, session *types.Session) (*types.Session, error) {
	copied := *session
	if copied.ID == "" {
		copied.ID = "sess-test"
	}
	s.owner.created = &copied
	return s.owner.created, nil
}

type hostCreateHandler struct {
	*Handler
	created *types.Session
}

func handlerWithApprovedDirs(dirs ...string) *hostCreateHandler {
	copied := append([]string(nil), dirs...)
	h := &hostCreateHandler{}
	svc := &capturingSessionService{owner: h}
	h.Handler = &Handler{
		sessionService: svc,
		approvedProjectDirs: func() []string {
			return copied
		},
	}
	return h
}

func postSession(t *testing.T, h *hostCreateHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	r.POST("/sessions", h.CreateSession)
	req := httptest.NewRequest(http.MethodPost, "/sessions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

// The workspace directory must come from the user-approved list. Accepting an
// arbitrary path would let anything that can reach this endpoint point the
// sandbox at the filesystem root.
func TestCreateSessionRejectsUnapprovedProjectDir(t *testing.T) {
	h := handlerWithApprovedDirs("/Users/dev/My Project")
	w := postSession(t, h, `{"project_dir":"/"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateSessionRejectsProjectDirWhenNoneApproved(t *testing.T) {
	h := handlerWithApprovedDirs()
	w := postSession(t, h, `{"project_dir":"/Users/dev/My Project"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateSessionStoresApprovedProjectDir(t *testing.T) {
	h := handlerWithApprovedDirs("/Users/dev/My Project")
	w := postSession(t, h, `{"project_dir":"/Users/dev/My Project"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "/Users/dev/My Project", h.created.HostWorkspaceDir)
}

func TestCreateSessionStoresCleanedApprovedProjectDir(t *testing.T) {
	h := handlerWithApprovedDirs("/Users/dev/My Project")
	w := postSession(t, h, `{"project_dir":"/Users/dev/My Project/"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	require.Equal(t, "/Users/dev/My Project", h.created.HostWorkspaceDir)
}

func TestCreateSessionRejectsRelativeProjectDir(t *testing.T) {
	h := handlerWithApprovedDirs("/Users/dev/My Project")
	w := postSession(t, h, `{"project_dir":"My Project"}`)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// Omitting it is the normal path: the session gets an auto workspace.
func TestCreateSessionWithoutProjectDirLeavesItEmpty(t *testing.T) {
	h := handlerWithApprovedDirs("/Users/dev/My Project")
	w := postSession(t, h, `{"title":"hello"}`)
	require.Equal(t, http.StatusCreated, w.Code)
	require.NotNil(t, h.created)
	require.Empty(t, h.created.HostWorkspaceDir)
}
