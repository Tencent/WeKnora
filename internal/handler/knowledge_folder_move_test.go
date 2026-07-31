package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type knowledgeFolderMoveHandlerServiceStub struct {
	result   *types.KnowledgeFolderMoveResult
	err      error
	calls    int
	tenantID uint64
	input    *types.KnowledgeFolderMoveInput
}

func (s *knowledgeFolderMoveHandlerServiceStub) MoveKnowledge(
	ctx context.Context,
	input *types.KnowledgeFolderMoveInput,
) (*types.KnowledgeFolderMoveResult, error) {
	s.calls++
	s.tenantID, _ = types.TenantIDFromContext(ctx)
	if input != nil {
		inputCopy := *input
		inputCopy.KnowledgeIDs = append([]string(nil), input.KnowledgeIDs...)
		s.input = &inputCopy
	}
	return s.result, s.err
}

func newKnowledgeFolderMoveHandlerTestEngine(
	moveService interfaces.KnowledgeFolderMoveService,
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
	handler := NewKnowledgeFolderHandler(nil, moveService)
	engine.POST("/knowledge-bases/:id/folders/move-knowledge", handler.MoveKnowledge)
	return engine
}

func TestKnowledgeFolderMoveHandlerReturnsCountsOnlyAndPassesEffectiveScope(t *testing.T) {
	moveService := &knowledgeFolderMoveHandlerServiceStub{
		result: &types.KnowledgeFolderMoveResult{
			ChangedCount:   1,
			UnchangedCount: 1,
		},
	}
	engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 71)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["11111111-1111-4111-8111-111111111111",`+
				`"22222222-2222-4222-8222-222222222222"],`+
				`"target_folder_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	data := decodeKnowledgeFolderSuccessData(t, recorder)
	dataObject := decodeKnowledgeFolderJSONObject(t, data)
	require.Equal(
		t,
		knowledgeFolderHandlerKeySet("changed_count", "unchanged_count"),
		knowledgeFolderHandlerRawKeySet(dataObject),
	)
	require.Equal(t, 1, decodeKnowledgeFolderJSONInt(t, dataObject["changed_count"]))
	require.Equal(t, 1, decodeKnowledgeFolderJSONInt(t, dataObject["unchanged_count"]))
	for _, forbidden := range []string{
		"knowledge_ids",
		"target_folder_id",
		"tenant_id",
		"knowledge_base_id",
		"folder_id",
		"pending",
	} {
		require.NotContains(t, dataObject, forbidden)
	}

	require.Equal(t, 1, moveService.calls)
	require.Equal(t, uint64(71), moveService.tenantID)
	require.NotNil(t, moveService.input)
	require.Equal(t, uint64(71), moveService.input.TenantID)
	require.Equal(t, "kb-1", moveService.input.KnowledgeBaseID)
	require.Equal(
		t,
		[]string{
			"11111111-1111-4111-8111-111111111111",
			"22222222-2222-4222-8222-222222222222",
		},
		moveService.input.KnowledgeIDs,
	)
	require.Equal(
		t,
		"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		moveService.input.TargetFolderID,
	)
}

func TestKnowledgeFolderMoveHandlerAcceptsExplicitRootTarget(t *testing.T) {
	moveService := &knowledgeFolderMoveHandlerServiceStub{
		result: &types.KnowledgeFolderMoveResult{ChangedCount: 1},
	}
	engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 81)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
				`"target_folder_id":""}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
	require.NotNil(t, moveService.input)
	require.Equal(t, types.KnowledgeFolderRootID, moveService.input.TargetFolderID)
}

func TestKnowledgeFolderMoveHandlerRequiresTargetFolderIDPresence(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "omitted",
			body: `{"knowledge_ids":["11111111-1111-4111-8111-111111111111"]}`,
		},
		{
			name: "null",
			body: `{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],` +
				`"target_folder_id":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveHandlerServiceStub{
				result: &types.KnowledgeFolderMoveResult{ChangedCount: 1},
			}
			engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				http.StatusBadRequest,
				apperrors.ErrBadRequest,
				"请求参数不合法",
			)
			require.Zero(t, moveService.calls)
		})
	}
}

func TestKnowledgeFolderMoveHandlerRequiresOneTo200KnowledgeIDs(t *testing.T) {
	require.Equal(t, 200, knowledgeFolderMoveMaxKnowledgeIDs)
	tooMany := make([]string, knowledgeFolderMoveMaxKnowledgeIDs+1)
	for index := range tooMany {
		tooMany[index] = "11111111-1111-4111-8111-111111111111"
	}
	tooManyBody, err := json.Marshal(map[string]interface{}{
		"knowledge_ids":    tooMany,
		"target_folder_id": "",
	})
	require.NoError(t, err)
	require.Less(t, len(tooManyBody), int(knowledgeFolderMoveMaxBodyBytes))

	tests := []struct {
		name string
		body string
	}{
		{
			name: "empty",
			body: `{"knowledge_ids":[],"target_folder_id":""}`,
		},
		{
			name: "over maximum",
			body: string(tooManyBody),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveHandlerServiceStub{
				result: &types.KnowledgeFolderMoveResult{ChangedCount: 1},
			}
			engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				http.StatusBadRequest,
				apperrors.ErrBadRequest,
				"请求参数不合法",
			)
			require.Zero(t, moveService.calls)
		})
	}
}

func TestKnowledgeFolderMoveHandlerDefersIDValidationToService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "malformed knowledge id",
			body: `{"knowledge_ids":["not-a-uuid"],"target_folder_id":""}`,
		},
		{
			name: "malformed target folder id",
			body: `{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],` +
				`"target_folder_id":"not-a-uuid"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveHandlerServiceStub{
				err: service.ErrKnowledgeFolderInvalidArgument,
			}
			engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				http.StatusBadRequest,
				apperrors.ErrBadRequest,
				"请求参数不合法",
			)
			require.Equal(t, 1, moveService.calls)
		})
	}
}

func TestKnowledgeFolderMoveHandlerPassesDuplicateIDsUnchangedToService(t *testing.T) {
	const duplicateID = "11111111-1111-4111-8111-111111111111"
	moveService := &knowledgeFolderMoveHandlerServiceStub{
		result: &types.KnowledgeFolderMoveResult{
			ChangedCount: 1,
		},
	}
	engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["`+duplicateID+`","`+duplicateID+`"],`+
				`"target_folder_id":""}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
	require.NotNil(t, moveService.input)
	require.Equal(t, []string{duplicateID, duplicateID}, moveService.input.KnowledgeIDs)
}

func TestKnowledgeFolderMoveHandlerAcceptsExactBodyLimit(t *testing.T) {
	require.Equal(t, int64(64<<10), knowledgeFolderMoveMaxBodyBytes)
	moveService := &knowledgeFolderMoveHandlerServiceStub{
		result: &types.KnowledgeFolderMoveResult{ChangedCount: 1},
	}
	engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
	body := knowledgeFolderMovePaddedBody(t, int(knowledgeFolderMoveMaxBodyBytes))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
}

func TestKnowledgeFolderMoveHandlerAcceptsTrailingWhitespace(t *testing.T) {
	moveService := &knowledgeFolderMoveHandlerServiceStub{
		result: &types.KnowledgeFolderMoveResult{ChangedCount: 1},
	}
	engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(knowledgeFolderMoveValidBody()+" \n\t\r"),
	)
	request.Header.Set("Content-Type", "application/json")

	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
}

func TestKnowledgeFolderMoveHandlerRejectsLimitPlusOneBody(t *testing.T) {
	moveService := &knowledgeFolderMoveHandlerServiceStub{
		result: &types.KnowledgeFolderMoveResult{ChangedCount: 1},
	}
	engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
	body := knowledgeFolderMovePaddedBody(t, int(knowledgeFolderMoveMaxBodyBytes)+1)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(body),
	)
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	requireKnowledgeFolderMoveHandlerError(
		t,
		recorder,
		http.StatusBadRequest,
		apperrors.ErrBadRequest,
		"请求参数不合法",
	)
	require.Zero(t, moveService.calls)
	requireKnowledgeFolderMoveBodyErrorDoesNotLeak(t, recorder)
}

func TestKnowledgeFolderMoveHandlerRejectsInvalidCompleteBody(t *testing.T) {
	validBody := knowledgeFolderMoveValidBody()
	tests := []struct {
		name string
		body string
	}{
		{name: "empty", body: ""},
		{name: "truncated", body: `{"knowledge_ids":`},
		{name: "second json object", body: validBody + ` {}`},
		{name: "trailing garbage", body: validBody + `x`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveHandlerServiceStub{}
			engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				strings.NewReader(tt.body),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				http.StatusBadRequest,
				apperrors.ErrBadRequest,
				"请求参数不合法",
			)
			require.Zero(t, moveService.calls)
			requireKnowledgeFolderMoveBodyErrorDoesNotLeak(t, recorder)
		})
	}
}

func TestKnowledgeFolderMoveHandlerRejectsUnknownJSONFields(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "tenant", field: "tenant_id", value: "7"},
		{name: "knowledge base", field: "knowledge_base_id", value: `"kb-other"`},
		{name: "folder version", field: "folder_version", value: "4"},
		{name: "indexed version", field: "folder_indexed_version", value: "3"},
		{name: "operation", field: "operation_id", value: `"secret-operation"`},
		{name: "arbitrary", field: "unexpected", value: "true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveHandlerServiceStub{}
			engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
			body := `{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],` +
				`"target_folder_id":"","` + tt.field + `":` + tt.value + `}`
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				strings.NewReader(body),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				http.StatusBadRequest,
				apperrors.ErrBadRequest,
				"请求参数不合法",
			)
			require.Zero(t, moveService.calls)
			require.NotContains(t, recorder.Body.String(), tt.field)
			require.NotContains(t, recorder.Body.String(), "unknown field")
			requireKnowledgeFolderMoveBodyErrorDoesNotLeak(t, recorder)
		})
	}
}

func TestKnowledgeFolderMoveHandlerMapsServiceErrorsWithoutLeakingIDs(t *testing.T) {
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
			name:        "target folder not found",
			err:         service.ErrKnowledgeFolderNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    apperrors.ErrNotFound,
			wantMessage: "资源不存在",
		},
		{
			name:        "knowledge not found",
			err:         service.ErrKnowledgeFolderMoveKnowledgeNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    apperrors.ErrNotFound,
			wantMessage: "资源不存在",
		},
		{
			name: "deleting knowledge is unavailable",
			err: fmt.Errorf(
				"%w: knowledge %s is %s at internal version 99",
				service.ErrKnowledgeFolderMoveKnowledgeNotFound,
				"11111111-1111-4111-8111-111111111111",
				types.ParseStatusDeleting,
			),
			wantStatus:  http.StatusNotFound,
			wantCode:    apperrors.ErrNotFound,
			wantMessage: "资源不存在",
		},
		{
			name:        "data integrity",
			err:         service.ErrKnowledgeFolderDataIntegrity,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    apperrors.ErrInternalServer,
			wantMessage: "目录操作失败",
		},
		{
			name:        "internal",
			err:         service.ErrKnowledgeFolderInternal,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    apperrors.ErrInternalServer,
			wantMessage: "目录操作失败",
		},
		{
			name:        "unknown database error",
			err:         errors.New("database failure for 11111111-1111-4111-8111-111111111111"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    apperrors.ErrInternalServer,
			wantMessage: "目录操作失败",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			moveService := &knowledgeFolderMoveHandlerServiceStub{err: tt.err}
			engine := newKnowledgeFolderMoveHandlerTestEngine(moveService, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				strings.NewReader(knowledgeFolderMoveValidBody()),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				tt.wantStatus,
				tt.wantCode,
				tt.wantMessage,
			)
			require.Equal(t, 1, moveService.calls)
			require.NotContains(
				t,
				recorder.Body.String(),
				"11111111-1111-4111-8111-111111111111",
			)
			require.NotContains(t, recorder.Body.String(), types.ParseStatusDeleting)
			require.NotContains(t, recorder.Body.String(), "internal version")
		})
	}
}

func TestKnowledgeFolderMoveHandlerFailsClosedWithoutServiceOrResult(t *testing.T) {
	tests := []struct {
		name        string
		moveService interfaces.KnowledgeFolderMoveService
		wantCalls   int
	}{
		{name: "service unavailable"},
		{
			name:        "nil result",
			moveService: &knowledgeFolderMoveHandlerServiceStub{},
			wantCalls:   1,
		},
		{
			name: "negative changed count",
			moveService: &knowledgeFolderMoveHandlerServiceStub{
				result: &types.KnowledgeFolderMoveResult{
					ChangedCount: -1,
				},
			},
			wantCalls: 1,
		},
		{
			name: "negative unchanged count",
			moveService: &knowledgeFolderMoveHandlerServiceStub{
				result: &types.KnowledgeFolderMoveResult{
					UnchangedCount: -1,
				},
			},
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := newKnowledgeFolderMoveHandlerTestEngine(tt.moveService, 1)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/folders/move-knowledge",
				bytes.NewBufferString(knowledgeFolderMoveValidBody()),
			)
			request.Header.Set("Content-Type", "application/json")
			engine.ServeHTTP(recorder, request)

			requireKnowledgeFolderMoveHandlerError(
				t,
				recorder,
				http.StatusInternalServerError,
				apperrors.ErrInternalServer,
				"目录操作失败",
			)
			if stub, ok := tt.moveService.(*knowledgeFolderMoveHandlerServiceStub); ok {
				require.Equal(t, tt.wantCalls, stub.calls)
			}
		})
	}
}

func requireKnowledgeFolderMoveHandlerError(
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

func knowledgeFolderMoveValidBody() string {
	return `{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],` +
		`"target_folder_id":""}`
}

func knowledgeFolderMovePaddedBody(t *testing.T, totalBytes int) string {
	t.Helper()
	body := knowledgeFolderMoveValidBody()
	require.LessOrEqual(t, len(body), totalBytes)
	return body + strings.Repeat(" ", totalBytes-len(body))
}

func requireKnowledgeFolderMoveBodyErrorDoesNotLeak(
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
		"11111111-1111-4111-8111-111111111111",
	} {
		require.NotContains(t, response, leaked)
	}
}

func decodeKnowledgeFolderJSONInt(t *testing.T, data json.RawMessage) int {
	t.Helper()
	var value int
	require.NoError(t, json.Unmarshal(data, &value))
	return value
}
