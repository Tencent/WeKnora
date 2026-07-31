package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const knowledgeCreateHandlerFolderID = "10000000-0000-4000-8000-000000000001"

type knowledgeCreateHandlerServiceStub struct {
	interfaces.KnowledgeService

	err            error
	result         *types.Knowledge
	fileCalls      int
	urlCalls       int
	manualCalls    int
	updateCalls    int
	fileFolderID   string
	urlFolderID    string
	manualFolderID string
	updateID       string
	url            string
	urlFileName    string
	urlFileType    string
	urlMultimodel  *bool
	urlTitle       string
	urlTagIDs      []string
	urlChannel     string
	urlProcess     *types.KnowledgeProcessOverrides
	manualPayload  *types.ManualKnowledgePayload
	updatePayload  *types.ManualKnowledgePayload
}

func (s *knowledgeCreateHandlerServiceStub) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	_ *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	s.fileCalls++
	s.fileFolderID = folderID
	return s.knowledgeResult(folderID), s.err
}

func (s *knowledgeCreateHandlerServiceStub) CreateKnowledgeFromURL(
	_ context.Context,
	_ string,
	rawURL string,
	fileName string,
	fileType string,
	enableMultimodel *bool,
	title string,
	tagIDs []string,
	channel string,
	processConfig *types.KnowledgeProcessOverrides,
	folderID string,
) (*types.Knowledge, error) {
	s.urlCalls++
	s.urlFolderID = folderID
	s.url = rawURL
	s.urlFileName = fileName
	s.urlFileType = fileType
	if enableMultimodel != nil {
		value := *enableMultimodel
		s.urlMultimodel = &value
	}
	s.urlTitle = title
	s.urlTagIDs = append([]string(nil), tagIDs...)
	s.urlChannel = channel
	s.urlProcess = processConfig
	return s.knowledgeResult(folderID), s.err
}

func (s *knowledgeCreateHandlerServiceStub) CreateKnowledgeFromManual(
	_ context.Context,
	_ string,
	payload *types.ManualKnowledgePayload,
	_ string,
	folderID string,
) (*types.Knowledge, error) {
	s.manualCalls++
	s.manualFolderID = folderID
	if payload != nil {
		copy := *payload
		copy.TagIDs = append([]string(nil), payload.TagIDs...)
		s.manualPayload = &copy
	}
	return s.knowledgeResult(folderID), s.err
}

func (s *knowledgeCreateHandlerServiceStub) GetKnowledgeByIDOnly(
	_ context.Context,
	id string,
) (*types.Knowledge, error) {
	return &types.Knowledge{
		ID:              id,
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		FolderID:        knowledgeCreateHandlerFolderID,
	}, nil
}

func (s *knowledgeCreateHandlerServiceStub) UpdateManualKnowledge(
	_ context.Context,
	id string,
	payload *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	s.updateCalls++
	s.updateID = id
	if payload != nil {
		copy := *payload
		copy.TagIDs = append([]string(nil), payload.TagIDs...)
		s.updatePayload = &copy
	}
	return s.knowledgeResult(knowledgeCreateHandlerFolderID), s.err
}

func (s *knowledgeCreateHandlerServiceStub) knowledgeResult(folderID string) *types.Knowledge {
	if s.result != nil {
		return s.result
	}
	return &types.Knowledge{
		ID:                   "knowledge-1",
		KnowledgeBaseID:      "kb-1",
		FolderID:             folderID,
		FolderVersion:        9,
		FolderIndexedVersion: 8,
	}
}

type knowledgeCreateHandlerKBServiceStub struct {
	interfaces.KnowledgeBaseService
}

func (s *knowledgeCreateHandlerKBServiceStub) GetKnowledgeBaseByID(
	_ context.Context,
	id string,
) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{ID: id, TenantID: 1}, nil
}

func TestCreateKnowledgeFromFilePassesRawFolderID(t *testing.T) {
	tests := []struct {
		name       string
		include    bool
		folderID   string
		wantFolder string
	}{
		{name: "omitted"},
		{name: "explicit root", include: true},
		{
			name:       "non-root is not trimmed",
			include:    true,
			folderID:   " " + knowledgeCreateHandlerFolderID + " ",
			wantFolder: " " + knowledgeCreateHandlerFolderID + " ",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &knowledgeCreateHandlerServiceStub{}
			router := newKnowledgeCreateHandlerTestRouter(stub)
			body, contentType := newKnowledgeCreateMultipartBody(
				t,
				test.include,
				test.folderID,
			)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/knowledge/file",
				body,
			)
			request.Header.Set("Content-Type", contentType)

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, 1, stub.fileCalls)
			require.Equal(t, test.wantFolder, stub.fileFolderID)
			assertKnowledgeCreateResponseFolderAndHiddenVersions(
				t,
				recorder,
				test.wantFolder,
			)
		})
	}
}

func TestCreateKnowledgeFromURLUsesDTOAndDoesNotPerformHandlerSSRFValidation(t *testing.T) {
	stub := &knowledgeCreateHandlerServiceStub{}
	router := newKnowledgeCreateHandlerTestRouter(stub)
	body := []byte(fmt.Sprintf(`{
		"url":"http://127.0.0.1/private",
		"file_name":"document.pdf",
		"file_type":"pdf",
		"enable_multimodel":true,
		"title":"Title",
		"tag_ids":["tag-1"],
		"channel":"api",
		"process_config":{},
		"folder_id":%q
	}`, knowledgeCreateHandlerFolderID))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/knowledge/url",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, stub.urlCalls)
	require.Equal(t, knowledgeCreateHandlerFolderID, stub.urlFolderID)
	require.Equal(t, "http://127.0.0.1/private", stub.url)
	require.Equal(t, "document.pdf", stub.urlFileName)
	require.Equal(t, "pdf", stub.urlFileType)
	require.NotNil(t, stub.urlMultimodel)
	require.True(t, *stub.urlMultimodel)
	require.Equal(t, "Title", stub.urlTitle)
	require.Equal(t, []string{"tag-1"}, stub.urlTagIDs)
	require.Equal(t, "api", stub.urlChannel)
	require.Equal(t, &types.KnowledgeProcessOverrides{}, stub.urlProcess)
	assertKnowledgeCreateResponseFolderAndHiddenVersions(
		t,
		recorder,
		knowledgeCreateHandlerFolderID,
	)
}

func TestCreateKnowledgeFromURLTreatsOmittedAndExplicitEmptyFolderAsRoot(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "omitted",
			body: `{"url":"http://127.0.0.1/private"}`,
		},
		{
			name: "explicit root",
			body: `{"url":"http://127.0.0.1/private","folder_id":""}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &knowledgeCreateHandlerServiceStub{}
			router := newKnowledgeCreateHandlerTestRouter(stub)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/knowledge/url",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
			require.Equal(t, 1, stub.urlCalls)
			require.Empty(t, stub.urlFolderID)
			assertKnowledgeCreateResponseFolderAndHiddenVersions(t, recorder, "")
		})
	}
}

func TestCreateManualKnowledgeUsesCreateOnlyDTOMapper(t *testing.T) {
	stub := &knowledgeCreateHandlerServiceStub{}
	router := newKnowledgeCreateHandlerTestRouter(stub)
	body := []byte(fmt.Sprintf(`{
		"title":"Manual",
		"content":"Content",
		"status":"publish",
		"tag_ids":["tag-1","tag-2"],
		"channel":"api",
		"process_config":{},
		"folder_id":%q,
		"folder_version":99,
		"folder_indexed_version":98
	}`, knowledgeCreateHandlerFolderID))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/knowledge/manual",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, stub.manualCalls)
	require.Equal(t, knowledgeCreateHandlerFolderID, stub.manualFolderID)
	require.Equal(t, &types.ManualKnowledgePayload{
		Title:         "Manual",
		Content:       "Content",
		Status:        types.ManualKnowledgeStatusPublish,
		TagIDs:        []string{"tag-1", "tag-2"},
		Channel:       "api",
		ProcessConfig: &types.KnowledgeProcessOverrides{},
	}, stub.manualPayload)
	assertKnowledgeCreateResponseFolderAndHiddenVersions(
		t,
		recorder,
		knowledgeCreateHandlerFolderID,
	)
}

func TestCreateManualKnowledgeTreatsOmittedAndExplicitEmptyFolderAsRoot(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "omitted",
			body: `{"title":"Manual","content":"Content","status":"publish"}`,
		},
		{
			name: "explicit root",
			body: `{"title":"Manual","content":"Content","status":"publish","folder_id":""}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &knowledgeCreateHandlerServiceStub{}
			router := newKnowledgeCreateHandlerTestRouter(stub)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				http.MethodPost,
				"/knowledge-bases/kb-1/knowledge/manual",
				bytes.NewBufferString(test.body),
			)
			request.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
			require.Equal(t, 1, stub.manualCalls)
			require.Empty(t, stub.manualFolderID)
			assertKnowledgeCreateResponseFolderAndHiddenVersions(t, recorder, "")
		})
	}
}

func TestUpdateManualKnowledgeDoesNotAcceptFolderPlacementFields(t *testing.T) {
	stub := &knowledgeCreateHandlerServiceStub{}
	router := newKnowledgeCreateHandlerTestRouter(stub)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/knowledge/manual/knowledge-1",
		bytes.NewBufferString(fmt.Sprintf(`{
			"title":"Updated",
			"content":"Updated content",
			"status":"publish",
			"tag_ids":["tag-1"],
			"channel":"api",
			"folder_id":%q,
			"folder_version":99,
			"folder_indexed_version":98
		}`, knowledgeCreateHandlerFolderID)),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, stub.updateCalls)
	require.Equal(t, "knowledge-1", stub.updateID)
	require.Equal(t, &types.ManualKnowledgePayload{
		Title:   "Updated",
		Content: "Updated content",
		Status:  types.ManualKnowledgeStatusPublish,
		TagIDs:  []string{"tag-1"},
		Channel: "api",
	}, stub.updatePayload)
	encoded, err := json.Marshal(stub.updatePayload)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "folder_id")
	require.NotContains(t, string(encoded), "folder_version")
	require.NotContains(t, string(encoded), "folder_indexed_version")
}

func TestKnowledgeCreateFolderErrorsUseExistingSanitizedMapping(t *testing.T) {
	endpoints := []struct {
		name    string
		request func(*testing.T) *http.Request
		calls   func(*knowledgeCreateHandlerServiceStub) int
	}{
		{
			name: "file",
			request: func(t *testing.T) *http.Request {
				body, contentType := newKnowledgeCreateMultipartBody(t, true, "bad")
				request := httptest.NewRequest(
					http.MethodPost,
					"/knowledge-bases/kb-1/knowledge/file",
					body,
				)
				request.Header.Set("Content-Type", contentType)
				return request
			},
			calls: func(stub *knowledgeCreateHandlerServiceStub) int {
				return stub.fileCalls
			},
		},
		{
			name: "URL",
			request: func(_ *testing.T) *http.Request {
				request := httptest.NewRequest(
					http.MethodPost,
					"/knowledge-bases/kb-1/knowledge/url",
					bytes.NewBufferString(`{"url":"not a URL","folder_id":"bad"}`),
				)
				request.Header.Set("Content-Type", "application/json")
				return request
			},
			calls: func(stub *knowledgeCreateHandlerServiceStub) int {
				return stub.urlCalls
			},
		},
		{
			name: "manual",
			request: func(_ *testing.T) *http.Request {
				request := httptest.NewRequest(
					http.MethodPost,
					"/knowledge-bases/kb-1/knowledge/manual",
					bytes.NewBufferString(
						`{"title":"Manual","content":"Content","folder_id":"bad"}`,
					),
				)
				request.Header.Set("Content-Type", "application/json")
				return request
			},
			calls: func(stub *knowledgeCreateHandlerServiceStub) int {
				return stub.manualCalls
			},
		},
	}
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    int
		wantMessage string
	}{
		{
			name:        "invalid argument",
			err:         service.ErrKnowledgeFolderInvalidArgument,
			wantStatus:  http.StatusBadRequest,
			wantCode:    1000,
			wantMessage: "请求参数不合法",
		},
		{
			name:        "invalid name",
			err:         service.ErrKnowledgeFolderInvalidName,
			wantStatus:  http.StatusBadRequest,
			wantCode:    1000,
			wantMessage: "请求参数不合法",
		},
		{
			name:        "not found",
			err:         service.ErrKnowledgeFolderNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    1003,
			wantMessage: "目录不存在",
		},
		{
			name:        "data integrity",
			err:         service.ErrKnowledgeFolderDataIntegrity,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    1007,
			wantMessage: "目录操作失败",
		},
		{
			name:        "internal",
			err:         service.ErrKnowledgeFolderInternal,
			wantStatus:  http.StatusInternalServerError,
			wantCode:    1007,
			wantMessage: "目录操作失败",
		},
	}

	for _, test := range tests {
		for _, endpoint := range endpoints {
			t.Run(test.name+"/"+endpoint.name, func(t *testing.T) {
				const sensitiveMarker = "sql/path/tenant/kb/dsn marker"
				stub := &knowledgeCreateHandlerServiceStub{
					err: fmt.Errorf("%w: %s", test.err, sensitiveMarker),
				}
				router := newKnowledgeCreateHandlerTestRouter(stub)
				recorder := httptest.NewRecorder()

				router.ServeHTTP(recorder, endpoint.request(t))

				require.Equal(t, test.wantStatus, recorder.Code, recorder.Body.String())
				require.Equal(t, 1, endpoint.calls(stub))
				assertKnowledgeCreateErrorEnvelope(
					t,
					recorder,
					test.wantCode,
					test.wantMessage,
				)
				for _, marker := range []string{
					sensitiveMarker,
					"sql",
					"path",
					"tenant",
					"kb",
					"dsn",
				} {
					require.NotContains(t, recorder.Body.String(), marker)
				}
			})
		}
	}
}

func TestCreateKnowledgeFromURLMapsInvalidURLToSafeBadRequest(t *testing.T) {
	const (
		rawURL         = "http://127.0.0.1/private?token=sensitive"
		sensitiveCause = "SSRF DNS result 127.0.0.1 for tenant-7/kb-secret"
	)
	stub := &knowledgeCreateHandlerServiceStub{
		err: fmt.Errorf("%w: %s: %s", service.ErrInvalidURL, sensitiveCause, rawURL),
	}
	router := newKnowledgeCreateHandlerTestRouter(stub)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/knowledge/url",
		bytes.NewBufferString(fmt.Sprintf(`{"url":%q}`, rawURL)),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, stub.urlCalls)
	assertKnowledgeCreateErrorEnvelope(t, recorder, 1000, "Invalid URL")
	for _, sensitive := range []string{
		rawURL,
		sensitiveCause,
		"127.0.0.1",
		"SSRF",
		"DNS",
		"tenant-7",
		"kb-secret",
		"token=sensitive",
	} {
		require.NotContains(t, recorder.Body.String(), sensitive)
	}
}

func TestCreateKnowledgeDuplicateResponseKeepsExistingFolderAndHidesVersions(t *testing.T) {
	existing := &types.Knowledge{
		ID:                   "existing",
		KnowledgeBaseID:      "kb-1",
		FolderID:             "20000000-0000-4000-8000-000000000002",
		FolderVersion:        7,
		FolderIndexedVersion: 6,
	}
	duplicateErr := types.NewDuplicateURLError(existing)
	stub := &knowledgeCreateHandlerServiceStub{
		result: existing,
		err:    duplicateErr,
	}
	router := newKnowledgeCreateHandlerTestRouter(stub)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/knowledge/url",
		bytes.NewBufferString(fmt.Sprintf(
			`{"url":"http://phase33b.invalid/page","folder_id":%q}`,
			knowledgeCreateHandlerFolderID,
		)),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code, recorder.Body.String())
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, map[string]struct{}{
		"success": {},
		"message": {},
		"data":    {},
		"code":    {},
	}, knowledgeCreateHandlerJSONKeySet(response))
	var success bool
	require.NoError(t, json.Unmarshal(response["success"], &success))
	require.False(t, success)
	var code string
	require.NoError(t, json.Unmarshal(response["code"], &code))
	require.Equal(t, "duplicate_url", code)
	var message string
	require.NoError(t, json.Unmarshal(response["message"], &message))
	require.Equal(t, duplicateErr.Error(), message)
	assertKnowledgeCreateResponseFolderAndHiddenVersions(
		t,
		recorder,
		existing.FolderID,
	)
}

func newKnowledgeCreateHandlerTestRouter(
	serviceStub interfaces.KnowledgeService,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Next()
	})
	handler := &KnowledgeHandler{
		kgService: serviceStub,
		kbService: &knowledgeCreateHandlerKBServiceStub{},
	}
	router.POST(
		"/knowledge-bases/:id/knowledge/file",
		handler.CreateKnowledgeFromFile,
	)
	router.POST(
		"/knowledge-bases/:id/knowledge/url",
		handler.CreateKnowledgeFromURL,
	)
	router.POST(
		"/knowledge-bases/:id/knowledge/manual",
		handler.CreateManualKnowledge,
	)
	router.PUT(
		"/knowledge/manual/:id",
		handler.UpdateManualKnowledge,
	)
	return router
}

func newKnowledgeCreateMultipartBody(
	t *testing.T,
	includeFolder bool,
	folderID string,
) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "document.txt")
	require.NoError(t, err)
	_, err = part.Write([]byte("content"))
	require.NoError(t, err)
	if includeFolder {
		require.NoError(t, writer.WriteField("folder_id", folderID))
	}
	require.NoError(t, writer.Close())
	return body, writer.FormDataContentType()
}

func assertKnowledgeCreateResponseFolderAndHiddenVersions(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantFolderID string,
) {
	t.Helper()
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response["data"], &data))
	var folderID string
	require.NoError(t, json.Unmarshal(data["folder_id"], &folderID))
	require.Equal(t, wantFolderID, folderID)
	require.NotContains(t, data, "folder_version")
	require.NotContains(t, data, "folder_indexed_version")
}

func assertKnowledgeCreateErrorEnvelope(
	t *testing.T,
	recorder *httptest.ResponseRecorder,
	wantCode int,
	wantMessage string,
) {
	t.Helper()
	var response map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, map[string]struct{}{
		"success": {},
		"error":   {},
	}, knowledgeCreateHandlerJSONKeySet(response))

	var success bool
	require.NoError(t, json.Unmarshal(response["success"], &success))
	require.False(t, success)

	var errorObject map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response["error"], &errorObject))
	require.Equal(t, map[string]struct{}{
		"code":    {},
		"message": {},
		"details": {},
	}, knowledgeCreateHandlerJSONKeySet(errorObject))

	var code int
	require.NoError(t, json.Unmarshal(errorObject["code"], &code))
	require.Equal(t, wantCode, code)
	var message string
	require.NoError(t, json.Unmarshal(errorObject["message"], &message))
	require.Equal(t, wantMessage, message)
	require.Equal(t, "null", string(errorObject["details"]))
}

func knowledgeCreateHandlerJSONKeySet(
	object map[string]json.RawMessage,
) map[string]struct{} {
	keys := make(map[string]struct{}, len(object))
	for key := range object {
		keys[key] = struct{}{}
	}
	return keys
}
