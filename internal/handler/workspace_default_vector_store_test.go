package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type workspaceDefaultStoreRepository struct {
	interfaces.VectorStoreRepository
	store    *types.VectorStore
	err      error
	tenantID uint64
	id       string
}

func (r *workspaceDefaultStoreRepository) GetByID(_ context.Context, tenantID uint64, id string) (*types.VectorStore, error) {
	r.tenantID, r.id = tenantID, id
	return r.store, r.err
}

func workspaceVectorStoreTestRouter(tenant *types.Tenant) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
		c.Set(types.TenantIDContextKey.String(), uint64(1))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	return router
}

func TestWorkspaceDefaultVectorStoreSettings(t *testing.T) {
	for _, tc := range []struct {
		name        string
		body        string
		storeTenant uint64
		deleted     bool
		dbErr       error
		wantID      string
		wantLookup  bool
		status      int
	}{
		{name: "old client omits slot", body: `{}`, wantID: "previous", status: 200},
		{name: "clear slot", body: `{"vector_store_id":""}`, status: 200},
		{name: "unchanged stale value", body: `{"vector_store_id":"previous"}`, wantID: "previous", status: 200},
		{name: "owned saved store", body: `{"vector_store_id":"  target  "}`, storeTenant: 1, wantID: "target", wantLookup: true, status: 200},
		{name: "other workspace", body: `{"vector_store_id":"target"}`, storeTenant: 2, wantLookup: true, status: 400},
		{name: "missing store", body: `{"vector_store_id":"target"}`, wantLookup: true, status: 400},
		{name: "deleted store", body: `{"vector_store_id":"target"}`, storeTenant: 1, deleted: true, wantLookup: true, status: 400},
		{name: "deployment store", body: `{"vector_store_id":"__env_postgres"}`, status: 400},
		{name: "read failure", body: `{"vector_store_id":"target"}`, dbErr: fmt.Errorf("transient read failure"), wantLookup: true, status: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &workspaceDefaultStoreRepository{err: tc.dbErr}
			if tc.storeTenant != 0 {
				repo.store = &types.VectorStore{ID: "target", TenantID: tc.storeTenant, EngineType: types.MilvusRetrieverEngineType}
				if tc.deleted {
					repo.store.DeletedAt.Valid = true
					repo.store.DeletedAt.Time = time.Now()
				}
			}
			tenant := &types.Tenant{ID: 1, DefaultSettings: &types.DefaultSettings{VectorStoreID: "previous"}}
			svc := &defaultSettingsTenantService{}
			h := &TenantHandler{service: svc, vectorStores: repo, modelService: &defaultSettingsModelsService{}}
			router := workspaceVectorStoreTestRouter(tenant)
			router.PUT("/tenants/kv/default-settings", h.updateTenantDefaultSettingsInternal)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/tenants/kv/default-settings", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, "previous", tenant.DefaultSettings.VectorStoreID, "don't mutate the caller's context")
			if tc.wantLookup {
				require.Equal(t, uint64(1), repo.tenantID)
				require.Equal(t, "target", repo.id)
			} else {
				require.Empty(t, repo.id)
			}
			if tc.status == http.StatusOK {
				require.NotNil(t, svc.saved)
				require.Equal(t, tc.wantID, svc.saved.DefaultSettings.VectorStoreID)
			} else {
				require.Nil(t, svc.saved)
				require.NotContains(t, w.Body.String(), "transient read failure")
			}
		})
	}
}

type workspaceDefaultStoreKBService struct {
	interfaces.KnowledgeBaseService
	storeIDs []string
	err      error
}

func (s *workspaceDefaultStoreKBService) CreateKnowledgeBase(_ context.Context, kb *types.KnowledgeBase) (*types.KnowledgeBase, error) {
	store := ""
	if kb.VectorStoreID != nil {
		store = *kb.VectorStoreID
	}
	s.storeIDs = append(s.storeIDs, store)
	if s.err != nil {
		return nil, s.err
	}
	kb.ID, kb.TenantID = "new-kb", 1
	return kb, nil
}

func TestCreateKBWorkspaceDefaultVectorStore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		setup  string
		err    error
		status int
		calls  []string
	}{
		{name: "omission preserves system", body: `{"name":"Example"}`, setup: "saved-store", status: 201, calls: []string{""}},
		{name: "explicit false preserves system", body: `{"name":"Example","use_workspace_default_vector_store":false}`, setup: "saved-store", status: 201, calls: []string{""}},
		{name: "explicit store is unchanged", body: `{"name":"Example","vector_store_id":"explicit"}`, setup: "saved-store", status: 201, calls: []string{"explicit"}},
		{name: "opt in pins a configured value", body: `{"name":"Example","use_workspace_default_vector_store":true}`, setup: "saved-store", status: 201, calls: []string{"saved-store"}},
		{name: "opt in without a saved value uses system", body: `{"name":"Example","use_workspace_default_vector_store":true}`, status: 201, calls: []string{""}},
		{name: "both options rejected", body: `{"name":"Example","vector_store_id":"explicit","use_workspace_default_vector_store":true}`, setup: "saved-store", status: 400},
		{name: "bad flag type rejected", body: `{"name":"Example","use_workspace_default_vector_store":"true"}`, status: 400},
		{name: "deleted default fails rather than silently switching engines", body: `{"name":"Example","use_workspace_default_vector_store":true}`, setup: "deleted", err: errors.NewVectorStoreBindingInvalidError("vector store not found"), status: 400, calls: []string{"deleted"}},
		{name: "temporary failure remains a failure", body: `{"name":"Example","use_workspace_default_vector_store":true}`, setup: "saved-store", err: errors.NewInternalServerError("vector store lookup failed"), status: 500, calls: []string{"saved-store"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tenant := &types.Tenant{ID: 1, DefaultSettings: &types.DefaultSettings{VectorStoreID: tc.setup}}
			svc := &workspaceDefaultStoreKBService{err: tc.err}
			h := &KnowledgeBaseHandler{service: svc}
			router := workspaceVectorStoreTestRouter(tenant)
			router.POST("/knowledge-bases", h.CreateKnowledgeBase)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/knowledge-bases", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, tc.calls, svc.storeIDs)
		})
	}
}
