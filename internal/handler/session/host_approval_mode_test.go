package session

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type modeSessionService struct {
	interfaces.SessionService
	owned    map[string]bool
	readOnly map[string]bool
}

func (s modeSessionService) GetSession(_ context.Context, id string) (*types.Session, error) {
	if !s.owned[id] && !s.readOnly[id] {
		return nil, apperrors.ErrSessionNotFound
	}
	return &types.Session{ID: id}, nil
}

func (s modeSessionService) GetOwnedSession(_ context.Context, id string) (*types.Session, error) {
	if !s.owned[id] {
		return nil, apperrors.ErrSessionNotFound
	}
	return &types.Session{ID: id}, nil
}

type fakeHostModes struct {
	modes map[string]string
	err   error
}

func (f *fakeHostModes) SessionApprovalMode(_ context.Context, id string) (string, string) {
	if m, ok := f.modes[id]; ok {
		return m, "ask"
	}
	return "ask", "ask"
}

func (f *fakeHostModes) SetSessionApprovalMode(_ context.Context, id, mode string) error {
	if f.err != nil {
		return f.err
	}
	f.modes[id] = mode
	return nil
}

func modeRequest(t *testing.T, h *Handler, method, id, body string) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, "/api/v1/sessions/"+id+"/host-approval-mode", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: id}}
	if method == http.MethodGet {
		h.GetHostApprovalMode(c)
	} else {
		h.SetHostApprovalMode(c)
	}
	return rec, c
}

func lastAppError(t *testing.T, c *gin.Context) *apperrors.AppError {
	t.Helper()
	require.NotEmpty(t, c.Errors)
	var appErr *apperrors.AppError
	require.True(t, stderrors.As(c.Errors.Last().Err, &appErr))
	return appErr
}

func TestHostApprovalModeRoundTrip(t *testing.T) {
	modes := &fakeHostModes{modes: map[string]string{}}
	h := &Handler{sessionService: modeSessionService{owned: map[string]bool{"s1": true}}, hostModes: modes}

	rec, c := modeRequest(t, h, http.MethodPut, "s1", `{"mode":"auto"}`)
	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, rec.Code)

	rec, c = modeRequest(t, h, http.MethodGet, "s1", "")
	require.Empty(t, c.Errors)
	var got struct {
		Data struct{ Mode, Default string } `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "auto", got.Data.Mode)
	require.Equal(t, "ask", got.Data.Default)
}

func TestHostApprovalModeChecksOwnership(t *testing.T) {
	h := &Handler{
		sessionService: modeSessionService{owned: map[string]bool{}},
		hostModes:      &fakeHostModes{modes: map[string]string{}},
	}
	_, c := modeRequest(t, h, http.MethodGet, "other", "")
	require.Equal(t, http.StatusNotFound, lastAppError(t, c).HTTPCode)
}

func TestSetHostApprovalModeRejectsASessionTheCallerCanOnlyRead(t *testing.T) {
	modes := &fakeHostModes{modes: map[string]string{}}
	h := &Handler{
		sessionService: modeSessionService{owned: map[string]bool{}, readOnly: map[string]bool{"channel": true}},
		hostModes:      modes,
	}
	_, c := modeRequest(t, h, http.MethodPut, "channel", `{"mode":"auto"}`)
	require.Equal(t, http.StatusNotFound, lastAppError(t, c).HTTPCode)
	require.Empty(t, modes.modes)

	rec, c := modeRequest(t, h, http.MethodGet, "channel", "")
	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHostApprovalModeValidatesTheMode(t *testing.T) {
	modes := &fakeHostModes{modes: map[string]string{}}
	h := &Handler{sessionService: modeSessionService{owned: map[string]bool{"s1": true}}, hostModes: modes}
	for _, body := range []string{`{"mode":"full"}`, `{"mode":"yolo"}`, `{}`} {
		_, c := modeRequest(t, h, http.MethodPut, "s1", body)
		require.Equal(t, http.StatusBadRequest, lastAppError(t, c).HTTPCode, body)
	}
	require.Empty(t, modes.modes)
}

func TestHostApprovalModeIsLiteOnly(t *testing.T) {
	h := &Handler{sessionService: modeSessionService{owned: map[string]bool{"s1": true}}}
	_, c := modeRequest(t, h, http.MethodGet, "s1", "")
	require.Equal(t, http.StatusNotFound, lastAppError(t, c).HTTPCode)
}

func TestHostApprovalModeRejectsAnOversizedBody(t *testing.T) {
	modes := &fakeHostModes{modes: map[string]string{}}
	h := &Handler{sessionService: modeSessionService{owned: map[string]bool{"s1": true}}, hostModes: modes}

	oversized := `{"mode":"auto","padding":"` + strings.Repeat("x", 8<<10) + `"}`
	_, c := modeRequest(t, h, http.MethodPut, "s1", oversized)

	appErr := lastAppError(t, c)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Contains(t, appErr.Error(), "too large")
	// The oversized body must not reach the mode store.
	require.Empty(t, modes.modes)
}
