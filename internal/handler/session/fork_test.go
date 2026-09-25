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

type stubForker struct {
	result *service.ForkResult
	err    error
	gotMsg string
	gotTit string
}

func (s *stubForker) Fork(
	_ context.Context, _ uint64, _, _, messageID, title string,
) (*service.ForkResult, error) {
	s.gotMsg = messageID
	s.gotTit = title
	return s.result, s.err
}

func performFork(t *testing.T, forker sessionForker, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{forkService: forker}

	w := httptest.NewRecorder()
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.POST("/sessions/:session_id/fork", h.ForkSession)
	req := httptest.NewRequest(http.MethodPost, "/sessions/src/fork", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w
}

func TestForkSessionReturnsNewSessionID(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{SessionID: "new-1"}}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "u-2", forker.gotMsg)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, "new-1", data["session_id"])
	require.Equal(t, false, data["degraded"])
}

func TestForkSessionSurfacesDegradeReason(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{
		SessionID: "new-1", Degraded: true, Reason: service.ForkDegradeSandboxGone,
	}}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, true, data["degraded"])
	require.Equal(t, "SANDBOX_GONE", data["reason"])
}

func TestForkSessionMapsBusySourceTo409(t *testing.T) {
	forker := &stubForker{err: service.ErrForkSourceBusy}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusConflict, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "FORK_SOURCE_BUSY", body["code"])
}

func TestForkSessionMapsMissingSessionTo404(t *testing.T) {
	forker := &stubForker{err: service.ErrForkSessionNotFound}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestForkSessionMapsMissingMessageTo404(t *testing.T) {
	forker := &stubForker{err: service.ErrForkMessageNotFound}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestForkSessionMapsNonUserForkPointTo400(t *testing.T) {
	forker := &stubForker{err: service.ErrForkMessageNotUser}

	w := performFork(t, forker, `{"message_id":"a-1"}`)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestForkSessionRejectsMissingMessageID(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{SessionID: "new-1"}}

	w := performFork(t, forker, `{}`)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestForkSessionMapsUnknownErrorTo500(t *testing.T) {
	forker := &stubForker{err: errors.New("boom")}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestForkSessionPassesTitleThrough(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{SessionID: "new-1"}}

	performFork(t, forker, `{"message_id":"u-2","title":"我的分支"}`)

	require.Equal(t, "我的分支", forker.gotTit)
}
