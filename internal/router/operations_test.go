package router

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/backup"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type recordingManualBackupCreator struct {
	result backup.Result
	err    error
	reason string
}

func (c *recordingManualBackupCreator) CreateManual(_ context.Context, reason string) (backup.Result, error) {
	c.reason = reason
	return c.result, c.err
}

type recordingBackupAuditService struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
}

func (s *recordingBackupAuditService) Log(_ context.Context, entry *types.AuditLog) error {
	s.entries = append(s.entries, entry)
	return nil
}

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

func TestManualBackupRequiresReasonAndAuditsSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	creator := &recordingManualBackupCreator{result: backup.Result{
		BackupID: "bkp-1", CreatedAt: time.Now(), ArchiveFile: "bkp-1.sql.gz", ManifestFile: "bkp-1.manifest.json", SizeBytes: 12, SHA256: "checksum",
	}}
	audit := &recordingBackupAuditService{}
	observer := newOperationsObserver(nil, nil)
	observer.backupManager = creator
	observer.auditService = audit
	r := gin.New()
	v1 := r.Group("/api/v1")
	RegisterOperationsAdminRoutes(v1, observer, &rbacGuards{})

	invalid := httptest.NewRequest(http.MethodPost, "/api/v1/admin/operations/backups", strings.NewReader(`{"reason":""}`))
	invalid.Header.Set("Content-Type", "application/json")
	invalid = invalid.WithContext(context.WithValue(invalid.Context(), types.SystemAdminContextKey, true))
	invalidRecorder := httptest.NewRecorder()
	r.ServeHTTP(invalidRecorder, invalid)
	if invalidRecorder.Code != http.StatusBadRequest || creator.reason != "" {
		t.Fatalf("invalid manual backup = %d reason=%q", invalidRecorder.Code, creator.reason)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/operations/backups", strings.NewReader(`{"reason":"before upgrade"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), types.SystemAdminContextKey, true))
	request = request.WithContext(context.WithValue(request.Context(), types.UserIDContextKey, "admin-user"))
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusCreated || creator.reason != "before upgrade" {
		t.Fatalf("manual backup = %d reason=%q body=%s", recorder.Code, creator.reason, recorder.Body.String())
	}
	if len(audit.entries) != 1 || audit.entries[0].Action != types.AuditActionSystemBackupCreated || audit.entries[0].ActorUserID != "admin-user" || strings.Contains(string(audit.entries[0].Details), "password") {
		t.Fatalf("unexpected backup audit: %#v", audit.entries)
	}
}

func TestManualBackupReturnsSafeInProgressConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	creator := &recordingManualBackupCreator{err: &backup.Error{Kind: backup.ErrorInProgress}}
	observer := newOperationsObserver(nil, nil)
	observer.backupManager = creator
	r := gin.New()
	v1 := r.Group("/api/v1")
	RegisterOperationsAdminRoutes(v1, observer, &rbacGuards{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/operations/backups", strings.NewReader(`{"reason":"before upgrade"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), types.SystemAdminContextKey, true))
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict || !strings.Contains(recorder.Body.String(), `"code":"in_progress"`) || strings.Contains(recorder.Body.String(), "mysqldump") {
		t.Fatalf("unexpected in-progress response: %d %s", recorder.Code, recorder.Body.String())
	}

	creator.err = errors.New("unexpected internal failure")
	request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/operations/backups", strings.NewReader(`{"reason":"before upgrade"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), types.SystemAdminContextKey, true))
	recorder = httptest.NewRecorder()
	r.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "unexpected internal failure") {
		t.Fatalf("unexpected internal failure response: %d %s", recorder.Code, recorder.Body.String())
	}
}
