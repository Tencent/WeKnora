package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestOperationsMetricsUsesOnlyLowCardinalityHTTPLabels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	observer := newOperationsObserver(nil, nil)
	r := gin.New()
	r.Use(observer.httpMetricsMiddleware())
	r.GET("/resources/:id", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	registerMetricsRoute(r, observer)

	performRequest(r, "/resources/first-user-content")
	performRequest(r, "/resources/second-user-content")
	recorder := performRequest(r, "/metrics")

	if recorder.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `weknora_http_requests_total{method="GET",status_class="2xx"} 2`) {
		t.Fatalf("metrics response missing expected request counter: %s", body)
	}
	if strings.Contains(body, "first-user-content") || strings.Contains(body, "second-user-content") || strings.Contains(body, "route=") {
		t.Fatalf("metrics response contains high-cardinality request data: %s", body)
	}
}

func TestOperationsStatusReportsSanitizedRuntimeState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open returned error: %v", err)
	}

	observer := newOperationsObserver(db, nil)
	r := gin.New()
	r.GET("/status", observer.status)
	recorder := performRequest(r, "/status")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response operationsStatusResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if response.Status != "ready" || response.Dependencies["database"] != "ok" || response.Dependencies["redis"] != "disabled" {
		t.Fatalf("unexpected status response: %#v", response)
	}
	if strings.Contains(recorder.Body.String(), "file_path") || strings.Contains(recorder.Body.String(), "password") {
		t.Fatalf("status response exposed sensitive runtime data: %s", recorder.Body.String())
	}
}

func TestOperationsStatusRequiresSystemAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	observer := newOperationsObserver(nil, nil)
	r := gin.New()
	v1 := r.Group("/api/v1")
	RegisterOperationsAdminRoutes(v1, observer, &rbacGuards{})

	denied := performRequest(r, "/api/v1/admin/operations/status")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated status = %d, want %d", denied.Code, http.StatusForbidden)
	}

	allowedRecorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/operations/status", nil)
	request = request.WithContext(context.WithValue(request.Context(), types.SystemAdminContextKey, true))
	r.ServeHTTP(allowedRecorder, request)
	if allowedRecorder.Code != http.StatusOK {
		t.Fatalf("system-admin status = %d, want %d", allowedRecorder.Code, http.StatusOK)
	}
}
