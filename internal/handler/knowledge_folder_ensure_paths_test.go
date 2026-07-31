package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type knowledgeFolderEnsurePathsServiceStub struct {
	interfaces.KnowledgeFolderService
	result   []types.KnowledgeFolderEnsurePathResult
	err      error
	calls    int
	tenantID uint64
	kbID     string
	request  *types.KnowledgeFolderEnsurePathsRequest
}

func (s *knowledgeFolderEnsurePathsServiceStub) EnsurePaths(
	ctx context.Context,
	kbID string,
	req *types.KnowledgeFolderEnsurePathsRequest,
) ([]types.KnowledgeFolderEnsurePathResult, error) {
	s.calls++
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	s.kbID = kbID
	if req != nil {
		requestCopy := *req
		requestCopy.Paths = append([]types.KnowledgeFolderEnsurePathInput(nil), req.Paths...)
		for i := range requestCopy.Paths {
			requestCopy.Paths[i].Segments = append([]string(nil), req.Paths[i].Segments...)
		}
		s.request = &requestCopy
	}
	return s.result, s.err
}

func newKnowledgeFolderEnsurePathsHandlerTestEngine(
	serviceStub interfaces.KnowledgeFolderService,
	effectiveTenantID uint64,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	engine.Use(func(c *gin.Context) {
		ctx := context.WithValue(
			c.Request.Context(),
			types.TenantIDContextKey,
			effectiveTenantID,
		)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	handler := NewKnowledgeFolderHandler(serviceStub)
	engine.POST("/knowledge-bases/:id/folders/ensure-paths", handler.EnsurePaths)
	return engine
}

func TestKnowledgeFolderEnsurePathsHandlerReturnsExactResponseInServiceOrder(t *testing.T) {
	serviceStub := &knowledgeFolderEnsurePathsServiceStub{
		result: []types.KnowledgeFolderEnsurePathResult{
			{
				ClientKey: "src/internal",
				FolderID:  "10000000-0000-4000-8000-000000000001",
			},
			{
				ClientKey: "docs",
				FolderID:  "20000000-0000-4000-8000-000000000002",
			},
		},
	}
	engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 71)
	body := []byte(`{
		"parent_id":"",
		"paths":[
			{"client_key":"src/internal","segments":["src","internal"]},
			{"client_key":"docs","segments":["docs"]}
		]
	}`)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/ensure-paths",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := decodeKnowledgeFolderSuccessData(t, recorder)
	var dataObject map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &dataObject))
	require.Equal(
		t,
		knowledgeFolderHandlerKeySet("items"),
		knowledgeFolderHandlerRawKeySet(dataObject),
	)
	var items []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(dataObject["items"], &items))
	require.Len(t, items, 2)
	for _, item := range items {
		require.Equal(
			t,
			knowledgeFolderHandlerKeySet("client_key", "folder_id"),
			knowledgeFolderHandlerRawKeySet(item),
		)
		for _, internalKey := range []string{
			"tenant_id",
			"knowledge_base_id",
			"parent_id",
			"name",
			"path",
			"depth",
			"sort_order",
			"created_at",
			"updated_at",
			"deleted_at",
			"folder_version",
			"folder_indexed_version",
		} {
			require.NotContains(t, item, internalKey)
		}
	}
	require.Equal(t, "src/internal", decodeKnowledgeFolderJSONString(t, items[0]["client_key"]))
	require.Equal(
		t,
		"10000000-0000-4000-8000-000000000001",
		decodeKnowledgeFolderJSONString(t, items[0]["folder_id"]),
	)
	require.Equal(t, "docs", decodeKnowledgeFolderJSONString(t, items[1]["client_key"]))

	require.Equal(t, 1, serviceStub.calls)
	require.Equal(t, uint64(71), serviceStub.tenantID)
	require.Equal(t, "kb-1", serviceStub.kbID)
	require.NotNil(t, serviceStub.request)
	require.Equal(t, "", serviceStub.request.ParentID)
	require.Len(t, serviceStub.request.Paths, 2)
	require.Equal(t, []string{"src", "internal"}, serviceStub.request.Paths[0].Segments)
}

func TestKnowledgeFolderEnsurePathsHandlerAcceptsExactBodyLimit(t *testing.T) {
	require.Equal(t, int64(1<<20), knowledgeFolderEnsurePathsMaxBodyBytes)
	serviceStub := &knowledgeFolderEnsurePathsServiceStub{
		result: []types.KnowledgeFolderEnsurePathResult{
			{
				ClientKey: "body-marker-32",
				FolderID:  "10000000-0000-4000-8000-000000000001",
			},
		},
	}
	engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 81)
	body := knowledgeFolderEnsurePathsPaddedBody(
		t,
		int(knowledgeFolderEnsurePathsMaxBodyBytes),
	)
	require.Len(t, body, int(knowledgeFolderEnsurePathsMaxBodyBytes))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/ensure-paths",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, serviceStub.calls)
	require.Equal(t, uint64(81), serviceStub.tenantID)
	require.Equal(t, "kb-1", serviceStub.kbID)
	require.NotNil(t, serviceStub.request)
	require.Equal(t, "", serviceStub.request.ParentID)
	require.Len(t, serviceStub.request.Paths, 1)
	require.Equal(t, "body-marker-32", serviceStub.request.Paths[0].ClientKey)
	require.Equal(t, []string{"folder"}, serviceStub.request.Paths[0].Segments)
}

func TestKnowledgeFolderEnsurePathsHandlerRejectsLimitPlusOneBody(t *testing.T) {
	serviceStub := &knowledgeFolderEnsurePathsServiceStub{
		result: []types.KnowledgeFolderEnsurePathResult{
			{ClientKey: "unused", FolderID: "10000000-0000-4000-8000-000000000001"},
		},
	}
	engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)
	body := knowledgeFolderEnsurePathsPaddedBody(
		t,
		int(knowledgeFolderEnsurePathsMaxBodyBytes)+1,
	)
	require.Len(t, body, int(knowledgeFolderEnsurePathsMaxBodyBytes)+1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/ensure-paths",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderEnsurePathsError(
		t,
		recorder,
		http.StatusBadRequest,
		apperrors.ErrBadRequest,
		"请求参数不合法",
	)
	require.Zero(t, serviceStub.calls)
	requireKnowledgeFolderEnsurePathsBodyErrorDoesNotLeak(t, recorder)
}

func TestKnowledgeFolderEnsurePathsHandlerRejectsOversizedTrailingWhitespace(t *testing.T) {
	serviceStub := &knowledgeFolderEnsurePathsServiceStub{}
	engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)
	body := knowledgeFolderEnsurePathsValidBody() +
		strings.Repeat(" ", int(knowledgeFolderEnsurePathsMaxBodyBytes))
	require.Greater(t, len(body), int(knowledgeFolderEnsurePathsMaxBodyBytes))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/ensure-paths",
		strings.NewReader(body),
	)
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderEnsurePathsError(
		t,
		recorder,
		http.StatusBadRequest,
		apperrors.ErrBadRequest,
		"请求参数不合法",
	)
	require.Zero(t, serviceStub.calls)
}

func TestKnowledgeFolderEnsurePathsHandlerRejectsInvalidCompleteBody(t *testing.T) {
	validBody := knowledgeFolderEnsurePathsValidBody()
	tests := []struct {
		name string
		body string
	}{
		{name: "second JSON value", body: validBody + ` {}`},
		{name: "invalid trailing token", body: validBody + `x`},
		{name: "empty body", body: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderEnsurePathsServiceStub{}
			engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/ensure-paths",
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderEnsurePathsError(
				t,
				recorder,
				http.StatusBadRequest,
				apperrors.ErrBadRequest,
				"请求参数不合法",
			)
			require.Zero(t, serviceStub.calls)
		})
	}
}

func TestKnowledgeFolderEnsurePathsHandlerRejectsOversizedBody(t *testing.T) {
	require.Equal(t, int64(1<<20), knowledgeFolderEnsurePathsMaxBodyBytes)
	serviceStub := &knowledgeFolderEnsurePathsServiceStub{
		result: []types.KnowledgeFolderEnsurePathResult{
			{ClientKey: "unused", FolderID: "10000000-0000-4000-8000-000000000001"},
		},
	}
	engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)
	body := `{"parent_id":"","paths":[{"client_key":"` +
		strings.Repeat("x", int(knowledgeFolderEnsurePathsMaxBodyBytes)) +
		`","segments":["folder"]}]}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/ensure-paths",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderEnsurePathsError(
		t,
		recorder,
		http.StatusBadRequest,
		apperrors.ErrBadRequest,
		"请求参数不合法",
	)
	require.Zero(t, serviceStub.calls)
}

func TestKnowledgeFolderEnsurePathsHandlerRejectsInvalidJSON(t *testing.T) {
	serviceStub := &knowledgeFolderEnsurePathsServiceStub{}
	engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/ensure-paths",
		strings.NewReader(`{"parent_id":`),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderEnsurePathsError(
		t,
		recorder,
		http.StatusBadRequest,
		apperrors.ErrBadRequest,
		"请求参数不合法",
	)
	require.Zero(t, serviceStub.calls)
}

func TestKnowledgeFolderEnsurePathsHandlerMapsServiceErrors(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    apperrors.ErrorCode
		wantMessage string
	}{
		{
			name:        "invalid argument",
			err:         service.ErrKnowledgeFolderInvalidArgument,
			wantStatus:  http.StatusBadRequest,
			wantCode:    apperrors.ErrBadRequest,
			wantMessage: "请求参数不合法",
		},
		{
			name:        "invalid name",
			err:         service.ErrKnowledgeFolderInvalidName,
			wantStatus:  http.StatusBadRequest,
			wantCode:    apperrors.ErrBadRequest,
			wantMessage: "请求参数不合法",
		},
		{
			name:        "parent not found",
			err:         service.ErrKnowledgeFolderNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    apperrors.ErrNotFound,
			wantMessage: "目录不存在",
		},
		{
			name:        "depth exceeded",
			err:         service.ErrKnowledgeFolderDepthExceeded,
			wantStatus:  http.StatusConflict,
			wantCode:    apperrors.ErrConflict,
			wantMessage: "目录层级超过限制",
		},
		{
			name:        "data integrity",
			err:         service.ErrKnowledgeFolderDataIntegrity,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    apperrors.ErrInternalServer,
			wantMessage: "目录操作失败",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderEnsurePathsServiceStub{err: tt.err}
			engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/ensure-paths",
				strings.NewReader(`{"paths":[{"client_key":"key","segments":["folder"]}]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderEnsurePathsError(
				t,
				recorder,
				tt.wantStatus,
				tt.wantCode,
				tt.wantMessage,
			)
			require.Equal(t, 1, serviceStub.calls)
		})
	}
}

func TestKnowledgeFolderEnsurePathsHandlerNilOrEmptyResultFailsClosed(t *testing.T) {
	tests := []struct {
		name   string
		result []types.KnowledgeFolderEnsurePathResult
	}{
		{name: "nil", result: nil},
		{name: "empty", result: []types.KnowledgeFolderEnsurePathResult{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serviceStub := &knowledgeFolderEnsurePathsServiceStub{result: tt.result}
			engine := newKnowledgeFolderEnsurePathsHandlerTestEngine(serviceStub, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/ensure-paths",
				strings.NewReader(`{"paths":[{"client_key":"key","segments":["folder"]}]}`),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderEnsurePathsError(
				t,
				recorder,
				http.StatusInternalServerError,
				apperrors.ErrInternalServer,
				"目录操作失败",
			)
			require.Equal(t, 1, serviceStub.calls)
		})
	}
}

func requireKnowledgeFolderEnsurePathsError(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantStatus int,
	wantCode apperrors.ErrorCode,
	wantMessage string,
) {
	t.Helper()
	require.Equal(t, wantStatus, recorder.Code, recorder.Body.String())
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.Equal(
		t,
		knowledgeFolderHandlerKeySet("success", "error"),
		knowledgeFolderHandlerRawKeySet(envelope),
	)
	require.NotContains(t, envelope, "data")
	var success bool
	require.NoError(t, json.Unmarshal(envelope["success"], &success))
	require.False(t, success)

	var errorEnvelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["error"], &errorEnvelope))
	require.Equal(
		t,
		knowledgeFolderHandlerKeySet("code", "message", "details"),
		knowledgeFolderHandlerRawKeySet(errorEnvelope),
	)
	var code apperrors.ErrorCode
	require.NoError(t, json.Unmarshal(errorEnvelope["code"], &code))
	require.Equal(t, wantCode, code)
	var message string
	require.NoError(t, json.Unmarshal(errorEnvelope["message"], &message))
	require.Equal(t, wantMessage, message)
	var details interface{}
	require.NoError(t, json.Unmarshal(errorEnvelope["details"], &details))
	require.Nil(t, details)
}

func knowledgeFolderEnsurePathsValidBody() string {
	return `{"parent_id":"","paths":[{"client_key":"body-marker-32","segments":["folder"]}]}`
}

func knowledgeFolderEnsurePathsPaddedBody(t *testing.T, totalBytes int) string {
	t.Helper()
	body := knowledgeFolderEnsurePathsValidBody()
	require.LessOrEqual(t, len(body), totalBytes)
	return body + strings.Repeat(" ", totalBytes-len(body))
}

func requireKnowledgeFolderEnsurePathsBodyErrorDoesNotLeak(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
) {
	t.Helper()
	response := recorder.Body.String()
	for _, leaked := range []string{
		"http: request body too large",
		"MaxBytesError",
		"unexpected EOF",
		"invalid character",
		"body-marker-32",
	} {
		require.NotContains(t, response, leaked)
	}
}
