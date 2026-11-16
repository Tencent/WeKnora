package handler

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubStorageBindingKnowledgeService struct {
	interfaces.KnowledgeService
	total int64
	calls int
}

func (s *stubStorageBindingKnowledgeService) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context, _ string, page *types.Pagination, _ types.KnowledgeListFilter,
) (*types.PageResult, error) {
	s.calls++
	return types.NewPageResult(s.total, page, []*types.Knowledge{}), nil
}

// SetKBModelConfig sends only model IDs. Saving it must not pin a default
// provider or overwrite the backend projection on an existing knowledge base.
//
//nolint:staticcheck // Compatibility coverage deliberately exercises deprecated COS fields.
func TestUpdateKBConfigPreservesOmittedStorage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("STORAGE_ALLOW_LIST", "local")
	cases := []struct {
		name      string
		provider  *types.StorageProviderConfig
		legacy    types.StorageConfig
		backendID string
		fields    string
	}{
		{name: "legacy provider", provider: &types.StorageProviderConfig{Provider: "oss"}},
		{name: "concrete backend", provider: &types.StorageProviderConfig{Provider: "oss"}, backendID: "backend-oss"},
		{
			name: "explicit empty fields", provider: &types.StorageProviderConfig{Provider: "oss"},
			fields: `,"storageProvider":"","storageBackendId":""`,
		},
		{
			name: "whitespace fields", provider: &types.StorageProviderConfig{Provider: "oss"},
			fields: `,"storageProvider":"  ","storageBackendId":"  "`,
		},
		{name: "tenant default remains unpinned"},
		{name: "legacy cos config", legacy: types.StorageConfig{Provider: "cos", BucketName: "existing-bucket"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kb := &types.KnowledgeBase{
				ID: "kb-1", TenantID: 7, SummaryModelID: "m-old",
				StorageProviderConfig: tc.provider, StorageConfig: tc.legacy,
			}
			if tc.backendID != "" {
				kb.StorageBackendID = &tc.backendID
			}
			before, err := json.Marshal(kb.StorageProviderConfig)
			require.NoError(t, err)
			provider := kb.GetStorageProvider()
			repo := &stubInitializationKBRepo{}
			knowledge := &stubStorageBindingKnowledgeService{total: 3}
			models := &stubTenantStampModelService{getModelByID: func(context.Context, string) (*types.Model, error) {
				return &types.Model{ID: "m-new", TenantID: 7}, nil
			}}
			h := &InitializationHandler{
				kbService: &stubInitializationKBService{kb: kb}, kbRepository: repo,
				modelService: models, knowledgeService: knowledge,
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Params = gin.Params{{Key: "kbId", Value: kb.ID}}
			req := httptest.NewRequest(http.MethodPut, "/api/v1/initialization/config/kb-1",
				strings.NewReader(`{"llmModelId":"m-new"`+tc.fields+`}`))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req.WithContext(types.WithCaller(req.Context(), types.Caller{
				TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner,
			}))

			h.UpdateKBConfig(c)

			require.Empty(t, c.Errors)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotNil(t, repo.updated)
			require.Equal(t, "m-new", repo.updated.SummaryModelID)
			after, err := json.Marshal(repo.updated.StorageProviderConfig)
			require.NoError(t, err)
			require.Equal(t, string(before), string(after), "model-only updates must preserve the stored projection")
			require.Equal(t, provider, repo.updated.GetStorageProvider())
			require.Equal(t, tc.legacy, repo.updated.StorageConfig)
			if tc.backendID == "" {
				require.Nil(t, repo.updated.StorageBackendID)
			} else {
				require.Equal(t, tc.backendID, *repo.updated.StorageBackendID)
			}
			require.Zero(t, knowledge.calls, "omitted storage must not enter the migration check")
		})
	}
}

type stubStorageBindingResolver struct {
	interfaces.StorageBackendResolver
	backend *types.StorageBackend
	err     error
	calls   int
}

func (s *stubStorageBindingResolver) ResolveBackend(
	_ context.Context, _ *types.Tenant, _, _ string,
) (*types.StorageBackend, error) {
	s.calls++
	return s.backend, s.err
}

func TestApplyKBStorageBindingExplicitRequests(t *testing.T) {
	cases := []struct {
		name         string
		request      KBModelConfigRequest
		allowed      string
		backend      *types.StorageBackend
		resolveErr   error
		oldBackend   string
		fileCount    int64
		wantProvider string
		wantBackend  string
		wantError    bool
	}{
		{
			name: "explicit local", request: KBModelConfigRequest{StorageProvider: "local"},
			allowed: "local", wantProvider: "local",
		},
		{
			name: "normalized provider", request: KBModelConfigRequest{StorageProvider: " COS "},
			allowed: "cos", wantProvider: "cos",
		},
		{
			name: "provider disallowed", request: KBModelConfigRequest{StorageProvider: "cos"},
			allowed: "local", wantError: true,
		},
		{
			name: "backend only", request: KBModelConfigRequest{StorageBackendID: "backend-new"}, allowed: "s3",
			backend:      &types.StorageBackend{ID: "backend-new", Provider: "s3"},
			wantProvider: "s3", wantBackend: "backend-new",
		},
		{
			name: "backend unavailable", request: KBModelConfigRequest{StorageBackendID: "backend-new"},
			allowed:    "local",
			resolveErr: stderrors.New("unavailable"), wantError: true,
		},
		{
			name: "backend migration with files", request: KBModelConfigRequest{StorageBackendID: "backend-new"},
			allowed: "s3", backend: &types.StorageBackend{ID: "backend-new", Provider: "s3"},
			oldBackend: "backend-old", fileCount: 1, wantError: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STORAGE_ALLOW_LIST", tc.allowed)
			kb := &types.KnowledgeBase{ID: "kb-1", StorageProviderConfig: &types.StorageProviderConfig{Provider: "oss"}}
			if tc.oldBackend != "" {
				kb.StorageBackendID = &tc.oldBackend
			}
			resolver := &stubStorageBindingResolver{backend: tc.backend, err: tc.resolveErr}
			h := &InitializationHandler{
				storageResolver: resolver, knowledgeService: &stubStorageBindingKnowledgeService{total: tc.fileCount},
			}
			err := h.applyKBStorageBinding(context.Background(), kb, kb.ID, &tc.request)
			if tc.wantError {
				var appErr *apperrors.AppError
				require.ErrorAs(t, err, &appErr)
				require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
				require.Equal(t, "oss", kb.GetStorageProvider())
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.wantProvider, kb.GetStorageProvider())
			if tc.wantBackend != "" {
				require.Equal(t, tc.wantBackend, *kb.StorageBackendID)
				require.Equal(t, 1, resolver.calls)
			} else {
				require.Zero(t, resolver.calls)
			}
		})
	}
}
