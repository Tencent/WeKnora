package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type skillHandlerServiceStub struct {
	uploadAvailable bool
	visible         []*types.SkillSummary
	listCalls       int
	getError        error
	statusResults   []types.SkillStatusResult
	uploadCalls     int
}

func (s *skillHandlerServiceStub) ListPreloadedSkills(context.Context) ([]*skills.SkillMetadata, error) {
	return []*skills.SkillMetadata{{Name: "builtin", Description: "built in"}}, nil
}
func (s *skillHandlerServiceStub) GetSkillByName(context.Context, string) (*skills.Skill, error) {
	return nil, nil
}
func (s *skillHandlerServiceStub) TenantUploadAvailable() bool    { return s.uploadAvailable }
func (s *skillHandlerServiceStub) ScriptExecutionAvailable() bool { return false }
func (s *skillHandlerServiceStub) Upload(context.Context, uint64, string, io.Reader, int64) (*types.TenantSkill, error) {
	s.uploadCalls++
	return &types.TenantSkill{ID: "skill-1", Name: "uploaded"}, nil
}
func (s *skillHandlerServiceStub) UpdatePackage(context.Context, uint64, string, string, io.Reader, int64, int64) (*types.SkillDetail, error) {
	return nil, nil
}
func (s *skillHandlerServiceStub) ListVisible(context.Context, uint64, bool) ([]*types.SkillSummary, error) {
	s.listCalls++
	return s.visible, nil
}
func (s *skillHandlerServiceStub) GetVisible(context.Context, uint64, types.SkillReference, bool) (*types.SkillDetail, error) {
	return nil, s.getError
}
func (s *skillHandlerServiceStub) SetStatuses(context.Context, uint64, []types.SkillStatusUpdate) []types.SkillStatusResult {
	return s.statusResults
}

func TestGetSkillUsesSameNotFoundForMissingAndCrossTenant(t *testing.T) {
	for _, name := range []string{"missing", "cross-tenant"} {
		t.Run(name, func(t *testing.T) {
			service := &skillHandlerServiceStub{getError: repository.ErrTenantSkillNotFound}
			router := gin.New()
			router.GET("/skills/:id", withSkillContext(types.TenantRoleViewer), NewSkillHandler(service).GetSkill)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/skills/"+name, nil))
			if response.Code != http.StatusNotFound || response.Body.Len() != 0 {
				t.Fatalf("expected indistinguishable empty 404, got %d %q", response.Code, response.Body.String())
			}
		})
	}
}

func TestBatchStatusReturnsMultiStatusWithPerItemResults(t *testing.T) {
	service := &skillHandlerServiceStub{statusResults: []types.SkillStatusResult{
		{SkillID: "ok", Success: true}, {SkillID: "missing", Code: "not_found"},
	}}
	router := gin.New()
	router.PATCH("/skills/status/batch", withSkillContext(types.TenantRoleAdmin), NewSkillHandler(service).SetSkillStatuses)
	request := httptest.NewRequest(http.MethodPatch, "/skills/status/batch", strings.NewReader(`{"updates":[{"skill_id":"ok","status":"enabled"},{"skill_id":"missing","status":"disabled"}]}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusMultiStatus || !strings.Contains(response.Body.String(), `"not_found"`) {
		t.Fatalf("expected 207 with per-item result, got %d %s", response.Code, response.Body.String())
	}
}

func TestRunnerOfflineDoesNotDisableUpload(t *testing.T) {
	service := &skillHandlerServiceStub{uploadAvailable: true}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "skill.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("zip fixture delegated to service"))
	_ = writer.Close()
	router := gin.New()
	handler := NewSkillHandler(service)
	router.POST("/skills/upload", withSkillContext(types.TenantRoleAdmin), handler.UploadSkill)
	request := httptest.NewRequest(http.MethodPost, "/skills/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || service.uploadCalls != 1 || service.ScriptExecutionAvailable() {
		t.Fatalf("upload must remain available while runner is offline: status=%d calls=%d", response.Code, service.uploadCalls)
	}
}
func (s *skillHandlerServiceStub) Delete(context.Context, uint64, string, string) error { return nil }

func TestListSkillsReturnsUploadCapabilityAndVisibleSkills(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &skillHandlerServiceStub{
		uploadAvailable: true,
		visible: []*types.SkillSummary{{
			Source: types.SkillSourceTenant, SkillID: "skill-1", Name: "uploaded",
		}},
	}
	router := gin.New()
	router.GET("/skills", withSkillContext(types.TenantRoleAdmin), NewSkillHandler(service).ListSkills)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/skills", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Data                  []*types.SkillSummary `json:"data"`
		TenantUploadAvailable bool                  `json:"tenant_upload_available"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.TenantUploadAvailable || len(body.Data) != 1 || service.listCalls != 1 {
		t.Fatalf("unexpected response: %+v, list calls=%d", body, service.listCalls)
	}
}

func TestUploadSkillUnavailableReturnsNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &skillHandlerServiceStub{uploadAvailable: false}
	router := gin.New()
	router.POST("/skills/upload", withSkillContext(types.TenantRoleAdmin), NewSkillHandler(service).UploadSkill)

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/skills/upload", bytes.NewReader(nil)))

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", response.Code, response.Body.String())
	}
}

func withSkillContext(role types.TenantRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
