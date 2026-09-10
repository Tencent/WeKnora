package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type browserOwnershipStub struct{ interfaces.SessionService }

func (browserOwnershipStub) GetOwnedSession(context.Context, string) (*types.Session, error) {
	return nil, errors.New("not owned")
}

func TestBrowserCommandChecksOwnershipBeforeAccessingSandbox(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{sessionService: browserOwnershipStub{}}
	r := gin.New()
	r.POST("/sessions/:session_id/sandbox/browser", h.SandboxBrowserCommand)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/sessions/another-users-session/sandbox/browser",
		strings.NewReader(`{"action":"frame"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

type ownedBrowserSessionStub struct{ interfaces.SessionService }

func (ownedBrowserSessionStub) GetOwnedSession(context.Context, string) (*types.Session, error) {
	return &types.Session{}, nil
}

func TestBrowserPreviewReportsUnstartedSandboxWithoutAnHTTPError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{
		sessionService:  ownedBrowserSessionStub{},
		terminalService: service.NewSandboxTerminalService(nil, nil, nil, nil),
	}
	r := gin.New()
	r.POST("/sessions/:session_id/sandbox/browser", h.SandboxBrowserCommand)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/sessions/s-1/sandbox/browser",
		strings.NewReader(`{"action":"frame"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"success":true,"data":{"ok":false,"state":"not_bound"}}`, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}

func TestBrowserCapabilitiesChecksSessionOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{sessionService: browserOwnershipStub{}}
	r := gin.New()
	r.GET("/sessions/:id/sandbox/browser/capabilities", h.SandboxBrowserCapabilities)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/not-owned/sandbox/browser/capabilities", nil))
	require.Equal(t, http.StatusNotFound, w.Code)
}

type browserCapabilitySessionStub struct{ ownedBrowserSessionStub }

func (browserCapabilitySessionStub) SessionBrowserAvailable(context.Context, uint64, string, string) (bool, error) {
	return false, nil
}

func TestBrowserCapabilitiesReportsUnavailableWithoutStartingSandbox(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{sessionService: browserCapabilitySessionStub{}}
	r := gin.New()
	r.GET("/sessions/:id/sandbox/browser/capabilities", h.SandboxBrowserCapabilities)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/owned/sandbox/browser/capabilities", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.JSONEq(t, `{"success":true,"data":{"available":false}}`, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
