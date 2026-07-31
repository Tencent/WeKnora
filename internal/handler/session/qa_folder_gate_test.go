package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type folderGateSessionService struct {
	interfaces.SessionService
	searchCalls                  int
	knowledgeBaseIDs             []string
	knowledgeIDs                 []string
	knowledgeBaseIDByKnowledgeID map[string]string
	folderScopes                 []types.FolderScope
}

func (s *folderGateSessionService) SearchKnowledge(
	_ context.Context,
	knowledgeBaseIDs []string,
	knowledgeIDs []string,
	knowledgeBaseIDByKnowledgeID map[string]string,
	_ []types.TagScope,
	folderScopes []types.FolderScope,
	_ string,
) ([]*types.SearchResult, error) {
	s.searchCalls++
	s.knowledgeBaseIDs = append([]string(nil), knowledgeBaseIDs...)
	s.knowledgeIDs = append([]string(nil), knowledgeIDs...)
	s.knowledgeBaseIDByKnowledgeID = knowledgeBaseIDByKnowledgeID
	s.folderScopes = append([]types.FolderScope(nil), folderScopes...)
	return nil, nil
}

func (s *folderGateSessionService) GetOwnedSession(
	_ context.Context,
	id string,
) (*types.Session, error) {
	return &types.Session{ID: id, TenantID: 1}, nil
}

var malformedFolderMentionCases = []struct {
	name string
	item string
}{
	{name: "empty id", item: `{"id":"","type":"folder","kb_id":"kb-1"}`},
	{name: "blank id", item: `{"id":"   ","type":"folder","kb_id":"kb-1"}`},
	{name: "empty kb id", item: `{"id":"folder-1","type":"folder","kb_id":""}`},
	{name: "blank kb id", item: `{"id":"folder-1","type":"folder","kb_id":"   "}`},
}

func TestParseQARequestRejectsMalformedFolderMentions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range malformedFolderMentionCases {
		t.Run(tt.name, func(t *testing.T) {
			handler := &Handler{
				config: &config.Config{
					KnowledgeBase: &config.KnowledgeBaseConfig{DocumentFoldersEnabled: true},
				},
				sessionService: &folderGateSessionService{},
			}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
			ctx.Request = httptest.NewRequest(
				http.MethodPost,
				"/sessions/session-1/knowledge-qa",
				strings.NewReader(`{
					"query":"folder query",
					"knowledge_base_ids":["kb-1"],
					"mentioned_items":[`+tt.item+`]
				}`),
			)
			ctx.Request.Header.Set("Content-Type", "application/json")

			requestContext, request, err := handler.parseQARequest(ctx, "KnowledgeQA")

			require.Nil(t, requestContext)
			require.Nil(t, request)
			appErr, ok := err.(*apperrors.AppError)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			require.Contains(t, appErr.Message, "folder mention")
		})
	}
}

func TestSearchKnowledgeRejectsMalformedFolderMentions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range malformedFolderMentionCases {
		t.Run(tt.name, func(t *testing.T) {
			service := &folderGateSessionService{}
			handler := &Handler{
				config: &config.Config{
					KnowledgeBase: &config.KnowledgeBaseConfig{DocumentFoldersEnabled: true},
				},
				sessionService: service,
			}
			response := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(response)
			ctx.Request = httptest.NewRequest(
				http.MethodPost,
				"/sessions/search",
				strings.NewReader(`{
					"query":"folder query",
					"knowledge_base_ids":["kb-1"],
					"mentioned_items":[`+tt.item+`]
				}`),
			)
			ctx.Request.Header.Set("Content-Type", "application/json")

			handler.SearchKnowledge(ctx)

			require.Zero(t, service.searchCalls)
			require.Len(t, ctx.Errors, 1)
			appErr, ok := ctx.Errors.Last().Err.(*apperrors.AppError)
			require.True(t, ok)
			require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
			require.Contains(t, appErr.Message, "folder mention")
		})
	}
}

func TestSearchKnowledgeRejectsFolderScopeWhenCapabilityDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &folderGateSessionService{}
	handler := &Handler{
		config: &config.Config{
			KnowledgeBase: &config.KnowledgeBaseConfig{
				DocumentFoldersEnabled: false,
			},
		},
		sessionService: service,
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/sessions/search",
		strings.NewReader(`{
			"query":"folder query",
			"mentioned_items":[
				{"id":"folder-1","name":"Folder","type":"folder","kb_id":"kb-1"}
			]
		}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SearchKnowledge(ctx)

	require.Zero(t, service.searchCalls)
	require.Len(t, ctx.Errors, 1)
	appErr, ok := ctx.Errors.Last().Err.(*apperrors.AppError)
	require.True(t, ok)
	require.Equal(t, http.StatusServiceUnavailable, appErr.HTTPCode)
}

func TestSearchKnowledgeForwardsFileMentionKnowledgeBaseHints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &folderGateSessionService{}
	handler := &Handler{
		config: &config.Config{
			KnowledgeBase: &config.KnowledgeBaseConfig{
				DocumentFoldersEnabled: true,
			},
		},
		sessionService: service,
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/knowledge-search",
		strings.NewReader(`{
			"query":"scoped query",
			"mentioned_items":[
				{"id":"stale-doc","name":"Old file","type":"file","kb_id":"kb-a"},
				{"id":"folder-b","name":"Folder B","type":"folder","kb_id":"kb-b"}
			]
		}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	handler.SearchKnowledge(ctx)

	require.Empty(t, ctx.Errors)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, []string{"stale-doc"}, service.knowledgeIDs)
	require.Equal(t, map[string]string{"stale-doc": "kb-a"}, service.knowledgeBaseIDByKnowledgeID)
}

func TestSearchKnowledgeAllowsRestrictedAPIKeyFileAndFolderWithinScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &folderGateSessionService{}
	handler := &Handler{
		config: &config.Config{
			KnowledgeBase: &config.KnowledgeBaseConfig{
				DocumentFoldersEnabled: true,
			},
		},
		sessionService: service,
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-search",
		strings.NewReader(`{
			"query":"scoped query",
			"mentioned_items":[
				{"id":"doc-1","name":"File","type":"file","kb_id":"kb-allowed"},
				{"id":"folder-1","name":"Folder","type":"folder","kb_id":"kb-allowed"}
			]
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	requestContext := types.WithTenantAPIKeyScope(request.Context(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	ctx.Request = request.WithContext(requestContext)

	handler.SearchKnowledge(ctx)

	require.Empty(t, ctx.Errors)
	require.Equal(t, 1, service.searchCalls)
	require.Equal(t, []string{"doc-1"}, service.knowledgeIDs)
	require.Equal(t, map[string]string{"doc-1": "kb-allowed"}, service.knowledgeBaseIDByKnowledgeID)
	require.Equal(t, []types.FolderScope{{
		KnowledgeBaseID: "kb-allowed",
		FolderID:        "folder-1",
	}}, service.folderScopes)
}

func TestSearchKnowledgeRejectsRestrictedAPIKeyOutOfScopeFileHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &folderGateSessionService{}
	handler := &Handler{
		config: &config.Config{
			KnowledgeBase: &config.KnowledgeBaseConfig{
				DocumentFoldersEnabled: true,
			},
		},
		sessionService: service,
	}

	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-search",
		strings.NewReader(`{
			"query":"scoped query",
			"mentioned_items":[
				{"id":"doc-1","name":"File","type":"file","kb_id":"kb-blocked"},
				{"id":"folder-1","name":"Folder","type":"folder","kb_id":"kb-allowed"}
			]
		}`),
	)
	request.Header.Set("Content-Type", "application/json")
	requestContext := types.WithTenantAPIKeyScope(request.Context(), types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-allowed"},
	})
	ctx.Request = request.WithContext(requestContext)

	handler.SearchKnowledge(ctx)

	require.Zero(t, service.searchCalls)
	require.Len(t, ctx.Errors, 1)
	appErr, ok := ctx.Errors.Last().Err.(*apperrors.AppError)
	require.True(t, ok)
	require.Equal(t, http.StatusForbidden, appErr.HTTPCode)
}

func TestFileKnowledgeBaseHintsAreForwardedToQARequest(t *testing.T) {
	hints := fileKnowledgeBaseHints([]MentionedItemRequest{
		{ID: "file-1", Type: "file", KBID: "kb-1"},
		{ID: "file-without-kb", Type: "file"},
		{ID: "folder-1", Type: "folder", KBID: "kb-1"},
	})
	requestContext := &qaRequestContext{
		assistantMessage:             &types.Message{},
		knowledgeBaseIDByKnowledgeID: hints,
	}

	request := requestContext.buildQARequest()

	require.Equal(t, map[string]string{"file-1": "kb-1"}, request.KnowledgeBaseIDByKnowledgeID)
}
