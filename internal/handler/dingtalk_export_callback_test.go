package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDingTalkExportCallbackHandlerAcceptsValidEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubDingTalkExportService{}
	t.Setenv("DINGTALK_EXPORT_CALLBACK_TOKEN", "secret-token")
	handler := NewDingTalkExportCallbackHandler(svc)

	r := gin.New()
	r.POST("/callback", handler.HandleExportFinish)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback?token=secret-token", strings.NewReader(`{"EventType":"dingdoc_export_finish"}`))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 1 || string(svc.payload) != `{"EventType":"dingdoc_export_finish"}` {
		t.Fatalf("service was not called with raw payload: calls=%d payload=%s", svc.calls, string(svc.payload))
	}
}

func TestDingTalkExportCallbackHandlerRequiresConfiguredToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubDingTalkExportService{}
	t.Setenv("DINGTALK_EXPORT_CALLBACK_TOKEN", "")
	handler := NewDingTalkExportCallbackHandler(svc)

	r := gin.New()
	r.POST("/callback", handler.HandleExportFinish)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback", strings.NewReader(`{"EventType":"dingdoc_export_finish"}`))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("service should not be called when token is not configured")
	}
}

func TestDingTalkExportCallbackHandlerRejectsInvalidToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubDingTalkExportService{}
	t.Setenv("DINGTALK_EXPORT_CALLBACK_TOKEN", "secret-token")
	handler := NewDingTalkExportCallbackHandler(svc)

	r := gin.New()
	r.POST("/callback", handler.HandleExportFinish)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/callback?token=wrong", strings.NewReader(`{}`))
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
	}
	if svc.calls != 0 {
		t.Fatalf("service should not be called when token is invalid")
	}
}

type stubDingTalkExportService struct {
	calls   int
	payload []byte
	err     error
}

func (s *stubDingTalkExportService) HandleExportFinishEvent(_ context.Context, payload []byte) error {
	s.calls++
	s.payload = payload
	return s.err
}
