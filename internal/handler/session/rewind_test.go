package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubRewinder struct {
	result *service.RewindResult
	err    error
	gotMsg string
}

func (s *stubRewinder) Rewind(
	_ context.Context, _ uint64, _, _, messageID string,
) (*service.RewindResult, error) {
	s.gotMsg = messageID
	return s.result, s.err
}

func performRewind(t *testing.T, rewinder sessionRewinder, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{rewindService: rewinder}

	w := httptest.NewRecorder()
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/sessions/:session_id/rewind", h.RewindSession)
	req := httptest.NewRequest(http.MethodPost, "/sessions/src/rewind", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestRewindSessionReturnsCounts(t *testing.T) {
	rewinder := &stubRewinder{result: &service.RewindResult{
		DeletedMessages: 4, WorkspaceReset: true,
	}}

	w := performRewind(t, rewinder, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "u-2", rewinder.gotMsg)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, float64(4), data["deleted_messages"])
	require.Equal(t, true, data["workspace_reset"])
	require.Equal(t, "", data["reason"])
}

func TestRewindSessionSurfacesSkipReason(t *testing.T) {
	rewinder := &stubRewinder{result: &service.RewindResult{
		DeletedMessages: 2, WorkspaceReset: false, Reason: service.RewindSkipNoCheckpoint,
	}}

	w := performRewind(t, rewinder, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, false, data["workspace_reset"])
	require.Equal(t, "NO_CHECKPOINT", data["reason"])
}

func TestRewindSessionRejectsMissingMessageID(t *testing.T) {
	rewinder := &stubRewinder{result: &service.RewindResult{DeletedMessages: 1}}

	w := performRewind(t, rewinder, `{}`)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRewindSessionMapsUnsupportedRoleTo400(t *testing.T) {
	rewinder := &stubRewinder{err: service.ErrRewindMessageRole}

	w := performRewind(t, rewinder, `{"message_id":"sys-1"}`)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRewindSessionMapsMissingSessionTo404(t *testing.T) {
	rewinder := &stubRewinder{err: service.ErrRewindSessionNotFound}

	w := performRewind(t, rewinder, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestRewindSessionMapsBusySourceTo409(t *testing.T) {
	rewinder := &stubRewinder{err: service.ErrRewindSourceBusy}

	w := performRewind(t, rewinder, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusConflict, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "REWIND_SOURCE_BUSY", body["code"])
}

func TestRewindSessionMapsReplacedSandboxTo409(t *testing.T) {
	rewinder := &stubRewinder{err: service.ErrRewindSandboxReplaced}

	w := performRewind(t, rewinder, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusConflict, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "REWIND_SANDBOX_REPLACED", body["code"])
}

func TestRewindSessionUnavailableWhenUnwired(t *testing.T) {
	w := performRewind(t, nil, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestRewindSessionMapsResetFailureTo500(t *testing.T) {
	rewinder := &stubRewinder{err: errors.New("workspace reset: git reset exec: sandbox unreachable")}

	w := performRewind(t, rewinder, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}
