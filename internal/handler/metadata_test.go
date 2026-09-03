package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
)

type metadataHandlerServiceStub struct {
	configureDefinition func(context.Context, types.ConfigureMetadataDefinition) (*types.MetadataDefinition, error)
	validateDocument    func(context.Context, string, []types.MetadataValueChange) error
	changeDocument      func(context.Context, types.ChangeDocumentMetadata) (*types.DocumentMetadata, error)
	readDocument        func(context.Context, []string) ([]*types.DocumentMetadata, error)
}

func (s *metadataHandlerServiceStub) ReadSchema(context.Context, string) (*types.MetadataSchema, error) {
	return nil, nil
}

func (s *metadataHandlerServiceStub) ConfigureDefinition(
	ctx context.Context,
	command types.ConfigureMetadataDefinition,
) (*types.MetadataDefinition, error) {
	return s.configureDefinition(ctx, command)
}

func (s *metadataHandlerServiceStub) ArchiveDefinition(context.Context, string, string) error {
	return nil
}

func (s *metadataHandlerServiceStub) ConfigureAutoRule(
	context.Context,
	types.ConfigureMetadataAutoRule,
) (*types.MetadataAutoRule, error) {
	return nil, nil
}

func (s *metadataHandlerServiceStub) DeleteAutoRule(context.Context, string, string) error {
	return nil
}

func (s *metadataHandlerServiceStub) ReadDocumentMetadata(
	ctx context.Context,
	knowledgeIDs []string,
) ([]*types.DocumentMetadata, error) {
	if s.readDocument != nil {
		return s.readDocument(ctx, knowledgeIDs)
	}
	return nil, nil
}

func (s *metadataHandlerServiceStub) ValidateDocumentMetadataChanges(
	ctx context.Context,
	knowledgeBaseID string,
	changes []types.MetadataValueChange,
) error {
	if s.validateDocument != nil {
		return s.validateDocument(ctx, knowledgeBaseID, changes)
	}
	return nil
}

func (s *metadataHandlerServiceStub) ChangeDocumentMetadata(
	ctx context.Context,
	command types.ChangeDocumentMetadata,
) (*types.DocumentMetadata, error) {
	return s.changeDocument(ctx, command)
}

func (s *metadataHandlerServiceStub) ConfirmDocumentMetadata(
	context.Context,
	types.ConfirmDocumentMetadata,
) (*types.DocumentMetadata, error) {
	return nil, nil
}

func (s *metadataHandlerServiceStub) ApplyAutomaticResults(
	context.Context,
	types.ApplyAutomaticMetadataResults,
) (*types.ApplyAutomaticMetadataReport, error) {
	return nil, nil
}

func (s *metadataHandlerServiceStub) ResolveDocumentScope(
	context.Context,
	types.MetadataScopeQuery,
) (types.DocumentScope, error) {
	return types.DocumentScope{}, nil
}

func TestMetadataHandler_CreateDefinitionMapsRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var captured types.ConfigureMetadataDefinition
	service := &metadataHandlerServiceStub{
		configureDefinition: func(
			_ context.Context,
			command types.ConfigureMetadataDefinition,
		) (*types.MetadataDefinition, error) {
			captured = command
			return &types.MetadataDefinition{ID: "definition-1"}, nil
		},
		changeDocument: unexpectedMetadataChange(t),
	}
	router := metadataHandlerTestRouter()
	router.POST("/knowledge-bases/:id/metadata-definitions", NewMetadataHandler(service, nil).CreateDefinition)

	request := httptest.NewRequest(http.MethodPost, "/knowledge-bases/kb-1/metadata-definitions", strings.NewReader(`{
		"name":"Document Type",
		"desc":"Classification",
		"value_type":"single_select",
		"required":true,
		"filterable":true,
		"sort_order":3,
		"options":[{"label":"Guide","sort_order":1}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "kb-1", captured.KnowledgeBaseID)
	require.Equal(t, "Document Type", captured.Name)
	require.Equal(t, "Classification", captured.Description)
	require.Equal(t, types.MetadataValueTypeSingleSelect, captured.ValueType)
	require.True(t, captured.Required)
	require.True(t, captured.Filterable)
	require.Equal(t, "Guide", captured.Options[0].Label)
}

func TestMetadataHandler_GetDocumentMetadataReturnsEmptySchema(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &metadataHandlerServiceStub{
		configureDefinition: unexpectedMetadataDefinition(t),
		changeDocument:      unexpectedMetadataChange(t),
		readDocument: func(_ context.Context, knowledgeIDs []string) ([]*types.DocumentMetadata, error) {
			require.Equal(t, []string{"doc-1"}, knowledgeIDs)
			return []*types.DocumentMetadata{{
				KnowledgeID: "doc-1",
				Values:      []types.DocumentMetadataField{},
			}}, nil
		},
	}
	router := metadataHandlerTestRouter()
	router.GET("/knowledge/:id/metadata-values", NewMetadataHandler(service, nil).GetDocumentMetadata)

	request := httptest.NewRequest(http.MethodGet, "/knowledge/doc-1/metadata-values", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"knowledge_id":"doc-1"`)
	require.Contains(t, response.Body.String(), `"values":[]`)
}

func TestMetadataHandler_GetDocumentMetadataReturnsNotFoundForMissingKnowledge(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &metadataHandlerServiceStub{
		configureDefinition: unexpectedMetadataDefinition(t),
		changeDocument:      unexpectedMetadataChange(t),
		readDocument: func(context.Context, []string) ([]*types.DocumentMetadata, error) {
			return []*types.DocumentMetadata{}, nil
		},
	}
	router := metadataHandlerTestRouter()
	router.GET("/knowledge/:id/metadata-values", NewMetadataHandler(service, nil).GetDocumentMetadata)

	request := httptest.NewRequest(http.MethodGet, "/knowledge/doc-missing/metadata-values", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestMetadataHandler_ChangeDocumentMetadataDistinguishesNullFromMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var captured types.ChangeDocumentMetadata
	service := &metadataHandlerServiceStub{
		configureDefinition: unexpectedMetadataDefinition(t),
		changeDocument: func(
			_ context.Context,
			command types.ChangeDocumentMetadata,
		) (*types.DocumentMetadata, error) {
			captured = command
			return &types.DocumentMetadata{KnowledgeID: command.KnowledgeID}, nil
		},
	}
	router := metadataHandlerTestRouter()
	router.PATCH("/knowledge/:knowledge_id/metadata-values", NewMetadataHandler(service, nil).ChangeDocumentMetadata)

	request := httptest.NewRequest(http.MethodPatch, "/knowledge/doc-1/metadata-values", strings.NewReader(`{
		"changes":[
			{"metadata_definition_id":"definition-clear","value":null,"expected_version":2},
			{"metadata_definition_id":"definition-policy","allow_auto_overwrite":true,"expected_version":4}
		]
	}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), types.UserIDContextKey, "user-1"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "doc-1", captured.KnowledgeID)
	require.Equal(t, "user-1", captured.UpdatedBy)
	require.Len(t, captured.Changes, 2)
	require.True(t, captured.Changes[0].ValueSet)
	require.Nil(t, captured.Changes[0].Value)
	require.False(t, captured.Changes[1].ValueSet)
	require.NotNil(t, captured.Changes[1].AllowAutoOverwrite)
	require.True(t, *captured.Changes[1].AllowAutoOverwrite)
}

func TestMetadataHandler_ChangeDocumentMetadataPreservesConflictResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &metadataHandlerServiceStub{
		configureDefinition: unexpectedMetadataDefinition(t),
		changeDocument: func(
			context.Context,
			types.ChangeDocumentMetadata,
		) (*types.DocumentMetadata, error) {
			return nil, apperrors.NewConflictError("metadata value version conflict").WithDetails(
				map[string]any{"version": 5},
			)
		},
	}
	router := metadataHandlerTestRouter()
	router.PATCH("/knowledge/:knowledge_id/metadata-values", NewMetadataHandler(service, nil).ChangeDocumentMetadata)

	request := httptest.NewRequest(http.MethodPatch, "/knowledge/doc-1/metadata-values", strings.NewReader(`{
		"changes":[{"metadata_definition_id":"definition-1","value":"new","expected_version":4}]
	}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusConflict, response.Code)
	require.Contains(t, response.Body.String(), `"code":1005`)
	require.Contains(t, response.Body.String(), `"version":5`)
}

func metadataHandlerTestRouter() *gin.Engine {
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	return router
}

func unexpectedMetadataDefinition(
	t *testing.T,
) func(context.Context, types.ConfigureMetadataDefinition) (*types.MetadataDefinition, error) {
	t.Helper()
	return func(context.Context, types.ConfigureMetadataDefinition) (*types.MetadataDefinition, error) {
		t.Fatal("unexpected ConfigureDefinition call")
		return nil, nil
	}
}

func unexpectedMetadataChange(
	t *testing.T,
) func(context.Context, types.ChangeDocumentMetadata) (*types.DocumentMetadata, error) {
	t.Helper()
	return func(context.Context, types.ChangeDocumentMetadata) (*types.DocumentMetadata, error) {
		t.Fatal("unexpected ChangeDocumentMetadata call")
		return nil, nil
	}
}
