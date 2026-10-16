package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/datasource"
	websearch "github.com/Tencent/WeKnora/internal/infrastructure/web_search"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func settingsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "settings.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func settingsRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := withDSCtx(httptest.NewRequest(method, path, strings.NewReader(body)), 1)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestSearchProviderPatchPreservesParametersAndDefault(t *testing.T) {
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	db := settingsTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.WebSearchProviderEntity{}))
	repo := repository.NewWebSearchProviderRepository(db)
	svc := service.NewWebSearchProviderService(repo)
	ctx := context.Background()
	original := &types.WebSearchProviderEntity{ID: "search", TenantID: 1, Name: "Search", Description: "Old", Provider: types.WebSearchProviderTypeDuckDuckGo, IsDefault: true, Parameters: types.WebSearchProviderParameters{APIKey: "secret", EngineID: "engine", BaseURL: "https://example.org", ProxyURL: "http://127.0.0.1:3128", ExtraConfig: map[string]string{"locale": "zh"}}}
	require.NoError(t, repo.Create(ctx, original))
	registry := websearch.NewRegistry()
	registry.Register("duckduckgo", func(params types.WebSearchProviderParameters) (interfaces.WebSearchProvider, error) {
		return nil, fmt.Errorf("upstream echoed credential: %s", params.APIKey)
	})
	h := NewWebSearchProviderHandler(repo, svc, registry)
	router := gin.New()
	router.Use(middleware.ErrorHandler(), func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), uint64(1)); c.Next() })
	router.PATCH("/providers/:id", h.PatchProvider)
	router.POST("/providers/:id/test", h.TestProviderByID)
	tested := settingsRequest(router, "POST", "/providers/search/test", "")
	require.Equal(t, 200, tested.Code)
	require.Contains(t, tested.Body.String(), `"success":false`)
	require.NotContains(t, tested.Body.String(), "secret")
	for _, body := range []string{`{"name":"Renamed"}`, `{"description":"","is_default":false}`} {
		response := settingsRequest(router, "PATCH", "/providers/search", body)
		require.Equal(t, 200, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), "secret")
	}
	saved, err := repo.GetByID(ctx, 1, "search")
	require.NoError(t, err)
	require.Equal(t, "Renamed", saved.Name)
	require.Empty(t, saved.Description)
	require.False(t, saved.IsDefault)
	require.Equal(t, original.Parameters, saved.Parameters)
	for _, body := range []string{`{}`, `{"name":" "}`} {
		require.Equal(t, 400, settingsRequest(router, "PATCH", "/providers/search", body).Code)
	}
	foreign := *original
	foreign.ID = "foreign"
	foreign.TenantID = 2
	require.NoError(t, repo.Create(ctx, &foreign))
	require.Equal(t, 404, settingsRequest(router, "PATCH", "/providers/foreign", `{"is_default":true}`).Code)
	// Validation must finish before a default is cleared.
	require.NoError(t, repo.Update(ctx, original))
	invalid := &types.WebSearchProviderEntity{ID: "invalid", TenantID: 1, Name: "Missing key", Provider: types.WebSearchProviderTypeGoogle}
	require.NoError(t, repo.Create(ctx, invalid))
	require.Equal(t, 400, settingsRequest(router, "PATCH", "/providers/invalid", `{"is_default":true}`).Code)
	current, err := repo.GetDefault(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "search", current.ID)
}

func TestDataSourceSyncPatchPersistsFalseAndManualSchedule(t *testing.T) {
	db := settingsTestDB(t)
	require.NoError(t, db.AutoMigrate(&types.DataSource{}))
	repo := repository.NewDataSourceRepository(db)
	scheduler := datasource.NewScheduler(repo, nil, nil)
	kb := &stubKBServiceForDS{getByID: func(context.Context, string) (*types.KnowledgeBase, error) {
		return &types.KnowledgeBase{ID: "kb", TenantID: 1}, nil
	}}
	svc := service.NewDataSourceService(repo, nil, nil, kb, nil, nil, scheduler, nil, nil, nil)
	original := &types.DataSource{ID: "ds", TenantID: 1, KnowledgeBaseID: "kb", Name: "Docs", Type: "rss", Status: types.DataSourceStatusPaused, SyncSchedule: "0 0 * * * *", SyncMode: "incremental", ConflictStrategy: "overwrite", SyncDeletions: true, Config: types.JSON(`{"type":"rss","credentials":{"auth_headers":"saved-secret"},"resource_ids":["old"],"settings":{"feed_urls":"https://example.org/feed","nested":{"keep":true}}}`)}
	require.NoError(t, repo.Create(context.Background(), original))
	h := NewDataSourceHandler(svc, kb)
	router := newDataSourceTestRouter(h)
	router.PATCH("/datasource/:id/sync-settings", h.PatchSyncSettings)
	response := settingsRequest(router, "PATCH", "/datasource/ds/sync-settings", `{"sync_schedule":"","sync_deletions":false}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "saved-secret")
	saved, err := repo.FindByID(context.Background(), "ds")
	require.NoError(t, err)
	require.Empty(t, saved.SyncSchedule)
	require.False(t, saved.SyncDeletions)
	require.Equal(t, types.DataSourceStatusPaused, saved.Status)
	require.Equal(t, original.Name, saved.Name)
	require.JSONEq(t, string(original.Config), string(saved.Config))
	require.Zero(t, scheduler.EntryCount(), "patch must not resume a paused source")
	for _, body := range []string{`{}`, `{"sync_schedule":"0 9 * * *"}`, `{"sync_mode":"bad"}`, `{"conflict_strategy":"bad"}`, `{"resource_ids":[]}`, `{"resource_ids":["x","x"]}`} {
		response = settingsRequest(router, "PATCH", "/datasource/ds/sync-settings", body)
		require.Equal(t, 400, response.Code, response.Body.String())
	}
	saved, err = repo.FindByID(context.Background(), "ds")
	require.NoError(t, err)
	require.Empty(t, saved.SyncSchedule)
	response = settingsRequest(router, "PATCH", "/datasource/ds/sync-settings", `{"sync_schedule":"CRON_TZ=Asia/Shanghai 0 0 9 * * *","sync_mode":"full","conflict_strategy":"skip"}`)
	require.Equal(t, 200, response.Code, response.Body.String())
	saved, err = repo.FindByID(context.Background(), "ds")
	require.NoError(t, err)
	require.Equal(t, "CRON_TZ=Asia/Shanghai 0 0 9 * * *", saved.SyncSchedule)
	require.Equal(t, "full", saved.SyncMode)
	require.Equal(t, "skip", saved.ConflictStrategy)
	require.False(t, saved.SyncDeletions)
}

func TestResourceSelectionPatchKeepsConnectorSettings(t *testing.T) {
	original := &types.DataSource{Config: types.JSON(`{"type":"notion","credentials":{"token":"secret"},"resource_ids":["old"],"settings":{"deep":{"keep":true}}}`)}
	var patch PatchDataSourceSyncRequest
	require.NoError(t, json.Unmarshal([]byte(`{"resource_ids":["a","b"]}`), &patch))
	updated, err := patch.apply(original)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"notion","resource_ids":["a","b"],"settings":{"deep":{"keep":true}}}`, string(updated.Config))
	require.Contains(t, string(original.Config), "secret", "input must not be mutated")
	// The service merges the stored credential instead of accepting one from this patch.
}

type searchBindingRepo struct {
	interfaces.WebSearchProviderRepository
	tenant uint64
}

func (r *searchBindingRepo) GetByID(_ context.Context, tenant uint64, id string) (*types.WebSearchProviderEntity, error) {
	r.tenant = tenant
	if id == "known" {
		return &types.WebSearchProviderEntity{ID: id, TenantID: tenant}, nil
	}
	return nil, nil
}
func TestAgentSearchBindingPatch(t *testing.T) {
	repo := &searchBindingRepo{}
	agent := patchTestAgent()
	agent.Config.WebSearchEnabled = false
	backing := &agentAvatarRepository{agent: agent}
	response := servePatchWith(t, backing, `{"web_search_provider_id":"known"}`, &CustomAgentHandler{webSearchProviders: repo})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, uint64(1), repo.tenant)
	require.Equal(t, "known", backing.agent.Config.WebSearchProviderID)
	require.False(t, backing.agent.Config.WebSearchEnabled)
	response = servePatchWith(t, backing, `{"web_search_provider_id":"foreign"}`, &CustomAgentHandler{webSearchProviders: repo})
	require.Equal(t, 400, response.Code, response.Body.String())
	require.Equal(t, "known", backing.agent.Config.WebSearchProviderID)
	response = servePatchWith(t, backing, `{"web_search_provider_id":"","web_search_enabled":true}`, &CustomAgentHandler{webSearchProviders: repo})
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Empty(t, backing.agent.Config.WebSearchProviderID)
	require.True(t, backing.agent.Config.WebSearchEnabled)
}
