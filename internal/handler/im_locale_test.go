package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestNormalizeIMLocale(t *testing.T) {
	for _, tt := range []struct {
		name    string
		locale  string
		want    string
		wantErr bool
	}{
		{name: "empty", locale: "", want: ""},
		{name: "supported", locale: "en-US", want: "en-US"},
		{name: "trimmed", locale: "  zh-CN ", want: "zh-CN"},
		{name: "unsupported", locale: "fr-FR", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeIMLocale(tt.locale)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeIMLocale(%q) error = %v, wantErr %v", tt.locale, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeIMLocale(%q) = %q, want %q", tt.locale, got, tt.want)
			}
		})
	}
}

func TestCreateIMChannelRejectsUnsupportedLocale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewIMHandler(nil)
	router.POST("/agents/:id/im-channels", handler.CreateIMChannel)

	req := httptest.NewRequest(
		http.MethodPost,
		"/agents/agent-1/im-channels",
		strings.NewReader(`{"platform":"feishu","locale":"fr-FR"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(42)))
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "locale must be one of") {
		t.Fatalf("body = %s, want locale validation error", recorder.Body.String())
	}
}
