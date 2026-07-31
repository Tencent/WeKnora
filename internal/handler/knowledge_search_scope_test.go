package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubFolderScopeKBService struct {
	interfaces.KnowledgeBaseService
	kbs          []*types.KnowledgeBase
	requestedIDs []string
}

func (s *stubFolderScopeKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context,
	ids []string,
) ([]*types.KnowledgeBase, error) {
	s.requestedIDs = append([]string(nil), ids...)
	return s.kbs, nil
}

type stubFolderScopeService struct {
	interfaces.DocumentFolderService
	validateCalls int
	searchScopes  []types.KnowledgeSearchScope
}

func (s *stubFolderScopeService) ValidateFolderExistsForUpload(
	_ context.Context,
	_ string,
	_ string,
) error {
	s.validateCalls++
	return nil
}

func (s *stubFolderScopeService) SearchFolders(
	_ context.Context,
	scopes []types.KnowledgeSearchScope,
	_ string,
	_ int,
	_ int,
) ([]*types.DocumentFolderSearchResult, bool, int64, error) {
	s.searchScopes = append([]types.KnowledgeSearchScope(nil), scopes...)
	return nil, false, 0, nil
}

type stubFolderScopeKnowledgeService struct {
	interfaces.KnowledgeService
	searchScopes []types.KnowledgeSearchScope
}

func (s *stubFolderScopeKnowledgeService) SearchKnowledgeForScopes(
	_ context.Context,
	scopes []types.KnowledgeSearchScope,
	_ string,
	_ int,
	_ int,
	_ []string,
) ([]*types.Knowledge, bool, int64, error) {
	s.searchScopes = append([]types.KnowledgeSearchScope(nil), scopes...)
	return nil, false, 0, nil
}

func TestRestrictKnowledgeSearchScopesOnlyNarrowsAuthorizedScopes(t *testing.T) {
	authorized := []types.KnowledgeSearchScope{
		{TenantID: 1, KBID: "kb-a"},
		{TenantID: 1, KBID: "kb-b"},
	}

	got := restrictKnowledgeSearchScopes(authorized, "kb-b,kb-not-authorized")

	assert.Equal(t, []types.KnowledgeSearchScope{
		{TenantID: 1, KBID: "kb-b"},
	}, got)
}

func TestRestrictKnowledgeSearchScopesBlankKeepsAuthorizedScopes(t *testing.T) {
	authorized := []types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb-a"}}

	assert.Equal(t, authorized, restrictKnowledgeSearchScopes(authorized, ""))
}

func TestValidateFolderForKnowledgeCreateRejectsUnsupportedKnowledgeBases(t *testing.T) {
	documentKB := &types.KnowledgeBase{
		ID:       "kb-document",
		TenantID: 1,
		Type:     types.KnowledgeBaseTypeDocument,
	}
	wikiKB := &types.KnowledgeBase{
		ID:       "kb-wiki",
		TenantID: 1,
		Type:     types.KnowledgeBaseTypeDocument,
		IndexingStrategy: types.IndexingStrategy{
			WikiEnabled: true,
		},
	}
	faqKB := &types.KnowledgeBase{
		ID:       "kb-faq",
		TenantID: 1,
		Type:     types.KnowledgeBaseTypeFAQ,
	}

	tests := []struct {
		name        string
		kb          *types.KnowledgeBase
		folderID    string
		wantHTTP    int
		wantCalls   int
		wantMessage string
	}{
		{
			name:      "document folder",
			kb:        documentKB,
			folderID:  "folder-1",
			wantCalls: 1,
		},
		{
			name:        "wiki folder",
			kb:          wikiKB,
			folderID:    "folder-1",
			wantHTTP:    http.StatusBadRequest,
			wantMessage: "Document folders are not supported",
		},
		{
			name:        "faq folder",
			kb:          faqKB,
			folderID:    "folder-1",
			wantHTTP:    http.StatusBadRequest,
			wantMessage: "Document folders are not supported",
		},
		{
			name:      "wiki root",
			kb:        wikiKB,
			folderID:  "",
			wantCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			folderService := &stubFolderScopeService{}
			handler := &KnowledgeHandler{
				cfg: &config.Config{
					KnowledgeBase: &config.KnowledgeBaseConfig{DocumentFoldersEnabled: true},
				},
				folderService: folderService,
			}

			err := handler.validateFolderForKnowledgeCreate(
				context.Background(),
				tt.kb,
				tt.folderID,
			)

			assert.Equal(t, tt.wantCalls, folderService.validateCalls)
			if tt.wantHTTP == 0 {
				require.NoError(t, err)
				return
			}
			appErr, ok := apperrors.IsAppError(err)
			require.True(t, ok)
			assert.Equal(t, tt.wantHTTP, appErr.HTTPCode)
			assert.Contains(t, appErr.Message, tt.wantMessage)
		})
	}
}

func TestSearchDocumentFoldersFiltersWikiAndFAQScopes(t *testing.T) {
	const tenantID = uint64(7)
	kbService := &stubFolderScopeKBService{
		kbs: []*types.KnowledgeBase{
			{
				ID:       "kb-document",
				TenantID: tenantID,
				Type:     types.KnowledgeBaseTypeDocument,
			},
			{
				ID:       "kb-wiki",
				TenantID: tenantID,
				Type:     types.KnowledgeBaseTypeDocument,
				IndexingStrategy: types.IndexingStrategy{
					WikiEnabled: true,
				},
			},
			{
				ID:       "kb-faq",
				TenantID: tenantID,
				Type:     types.KnowledgeBaseTypeFAQ,
			},
		},
	}
	folderService := &stubFolderScopeService{}
	handler := &KnowledgeHandler{
		cfg: &config.Config{
			KnowledgeBase: &config.KnowledgeBaseConfig{DocumentFoldersEnabled: true},
		},
		kbService:     kbService,
		folderService: folderService,
	}
	router := gin.New()
	router.GET("/document-folders/search", handler.SearchDocumentFolders)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-document", "kb-wiki", "kb-faq"},
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/document-folders/search?keyword=guide",
		nil,
	).WithContext(ctx)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.ElementsMatch(
		t,
		[]string{"kb-document", "kb-wiki", "kb-faq"},
		kbService.requestedIDs,
	)
	assert.Equal(t, []types.KnowledgeSearchScope{
		{TenantID: tenantID, KBID: "kb-document"},
	}, folderService.searchScopes)
}

func TestFolderScopeFilteringDoesNotChangeKnowledgeFileSearch(t *testing.T) {
	const tenantID = uint64(7)
	knowledgeService := &stubFolderScopeKnowledgeService{}
	handler := &KnowledgeHandler{kgService: knowledgeService}
	router := gin.New()
	router.GET("/knowledge/search", handler.SearchKnowledge)

	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{
		KnowledgeBaseIDs: types.StringArray{"kb-document", "kb-wiki"},
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"/knowledge/search?keyword=guide",
		nil,
	).WithContext(ctx)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.Contains(t, knowledgeService.searchScopes, types.KnowledgeSearchScope{
		TenantID: tenantID,
		KBID:     "kb-wiki",
	})
}
