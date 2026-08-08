package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// upsertKnowledgeServiceStub records which branch the create-or-update handler
// dispatched to and returns canned results.
type upsertKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	createCalls  int
	updateCalls  int
	upsertCalls  int
	lastUpsert   *types.KnowledgeFileCreateOrUpdateRequest
	lastUpdate   *types.KnowledgeFileUpdateRequest
	createResult *types.Knowledge
	createErr    error
	updateResult *types.KnowledgeFileUpsertResult
	updateErr    error
	upsertResult *types.KnowledgeFileUpsertResult
	upsertErr    error
}

type upsertKnowledgeBaseServiceStub struct {
	interfaces.KnowledgeBaseService
	tenantID uint64
}

func (s *upsertKnowledgeBaseServiceStub) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: s.tenantID}, nil
}

func (s *upsertKnowledgeServiceStub) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.createCalls++
	return s.createResult, s.createErr
}

func (s *upsertKnowledgeServiceStub) UpdateKnowledgeFile(
	_ context.Context,
	req *types.KnowledgeFileUpdateRequest,
) (*types.KnowledgeFileUpsertResult, error) {
	s.updateCalls++
	s.lastUpdate = req
	return s.updateResult, s.updateErr
}

func (s *upsertKnowledgeServiceStub) CreateOrUpdateKnowledgeFromFile(
	_ context.Context,
	req *types.KnowledgeFileCreateOrUpdateRequest,
) (*types.KnowledgeFileUpsertResult, error) {
	s.upsertCalls++
	s.lastUpsert = req
	return s.upsertResult, s.upsertErr
}

func newUpsertKnowledgeRouter(svc interfaces.KnowledgeService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Set(types.UserIDContextKey.String(), "u-test")
		c.Next()
	})
	handler := &KnowledgeHandler{
		kgService: svc,
		kbService: &upsertKnowledgeBaseServiceStub{tenantID: 1},
	}
	router.POST("/knowledge-bases/:id/knowledge/file/create-or-update", handler.CreateOrUpdateKnowledgeFromFile)
	return router
}

// performUpsertRequest builds a multipart create-or-update request with the
// given form fields plus a small file part.
func performUpsertRequest(t *testing.T, router http.Handler, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "report.pdf")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write([]byte("%PDF-1.4 test")); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/knowledge/file/create-or-update", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func decodeUpsertResult(t *testing.T, response *httptest.ResponseRecorder) *types.KnowledgeFileUpsertResult {
	t.Helper()
	var body struct {
		Success bool                             `json:"success"`
		Data    *types.KnowledgeFileUpsertResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	return body.Data
}

// TestCreateOrUpdate_Created verifies an accepted create-or-update request
// returns 202 and the service result.
func TestCreateOrUpdate_Created(t *testing.T) {
	svc := &upsertKnowledgeServiceStub{upsertResult: &types.KnowledgeFileUpsertResult{
		Action: "created", Knowledge: &types.Knowledge{ID: "k-new"},
	}}
	response := performUpsertRequest(t, newUpsertKnowledgeRouter(svc), nil)

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", response.Code, response.Body.String())
	}
	if svc.upsertCalls != 1 {
		t.Fatalf("upsert calls = %d, want 1", svc.upsertCalls)
	}
	result := decodeUpsertResult(t, response)
	if result.Action != "created" || result.Knowledge.ID != "k-new" {
		t.Fatalf("result = %+v, want action=created id=k-new", result)
	}
}

// TestCreateOrUpdate_Unchanged verifies an idempotent service result maps to
// HTTP 200.
func TestCreateOrUpdate_DuplicateIsUnchanged(t *testing.T) {
	existing := &types.Knowledge{ID: "k-existing"}
	svc := &upsertKnowledgeServiceStub{upsertResult: &types.KnowledgeFileUpsertResult{
		Action: "unchanged", Knowledge: existing,
	}}
	response := performUpsertRequest(t, newUpsertKnowledgeRouter(svc), nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	result := decodeUpsertResult(t, response)
	if result.Action != "unchanged" || result.Knowledge.ID != "k-existing" {
		t.Fatalf("result = %+v, want action=unchanged id=k-existing", result)
	}
}

// TestCreateOrUpdate_ForwardsFields verifies a supplied knowledge_id and other
// multipart fields are forwarded to the service.
func TestCreateOrUpdate_ForwardsFields(t *testing.T) {
	svc := &upsertKnowledgeServiceStub{
		upsertResult: &types.KnowledgeFileUpsertResult{
			Action:    "updated",
			Knowledge: &types.Knowledge{ID: "k-existing"},
			TaskID:    "task-1",
		},
	}
	response := performUpsertRequest(t, newUpsertKnowledgeRouter(svc), map[string]string{
		"knowledge_id":            "k-existing",
		"tag_ids":                 "t1,t2",
		"channel":                 "api",
		"expected_update_version": "7",
	})

	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", response.Code, response.Body.String())
	}
	if svc.upsertCalls != 1 {
		t.Fatalf("upsert calls = %d, want 1", svc.upsertCalls)
	}
	if svc.lastUpsert.KnowledgeID != "k-existing" {
		t.Fatalf("KnowledgeID = %q, want k-existing", svc.lastUpsert.KnowledgeID)
	}
	if !svc.lastUpsert.TagIDsProvided || len(svc.lastUpsert.TagIDs) != 2 {
		t.Fatalf("TagIDs = %+v provided=%v, want 2 provided", svc.lastUpsert.TagIDs, svc.lastUpsert.TagIDsProvided)
	}
	if !svc.lastUpsert.ChannelProvided || svc.lastUpsert.Channel != "api" {
		t.Fatalf("Channel = %q provided=%v, want api provided", svc.lastUpsert.Channel, svc.lastUpsert.ChannelProvided)
	}
	if svc.lastUpsert.ExpectedUpdateVersion == nil || *svc.lastUpsert.ExpectedUpdateVersion != 7 {
		t.Fatalf("ExpectedUpdateVersion = %v, want 7", svc.lastUpsert.ExpectedUpdateVersion)
	}
	result := decodeUpsertResult(t, response)
	if result.Action != "updated" || result.TaskID != "task-1" {
		t.Fatalf("result = %+v, want action=updated task-1", result)
	}
}

// TestCreateOrUpdate_UpdateUnchangedReturns200 verifies an unchanged update
// result maps to HTTP 200 rather than 202.
func TestCreateOrUpdate_UpdateUnchangedReturns200(t *testing.T) {
	svc := &upsertKnowledgeServiceStub{
		upsertResult: &types.KnowledgeFileUpsertResult{
			Action:    "unchanged",
			Knowledge: &types.Knowledge{ID: "k-existing"},
		},
	}
	response := performUpsertRequest(t, newUpsertKnowledgeRouter(svc), map[string]string{
		"knowledge_id": "k-existing",
	})

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	result := decodeUpsertResult(t, response)
	if result.Action != "unchanged" {
		t.Fatalf("result = %+v, want action=unchanged", result)
	}
}

func TestCreateOrUpdateRejectsInvalidExpectedUpdateVersion(t *testing.T) {
	svc := &upsertKnowledgeServiceStub{}
	response := performUpsertRequest(t, newUpsertKnowledgeRouter(svc), map[string]string{
		"knowledge_id":            "k-existing",
		"expected_update_version": "not-a-number",
	})

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", response.Code, response.Body.String())
	}
	if svc.updateCalls != 0 {
		t.Fatalf("update calls = %d, want 0", svc.updateCalls)
	}
	if svc.upsertCalls != 0 {
		t.Fatalf("upsert calls = %d, want 0", svc.upsertCalls)
	}
}
