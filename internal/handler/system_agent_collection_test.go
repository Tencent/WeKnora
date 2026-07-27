package handler

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/xuri/excelize/v2"
)

type fakeSystemCollectionService struct {
	interfaces.AgentCollectionService
	filter      types.AgentCollectionProfileFilter
	page        *types.AgentCollectionProfilePage
	summary     *types.AgentCollectionSummary
	profile     *types.AgentCollectionProfile
	history     *types.AgentCollectionHistoryPage
	export      *types.AgentCollectionExport
	adminUpdate types.SystemAdminCollectionUpdateInput
	purged      string
}

func (f *fakeSystemCollectionService) ListProfiles(
	_ context.Context, filter types.AgentCollectionProfileFilter,
) (*types.AgentCollectionProfilePage, error) {
	f.filter = filter
	return f.page, nil
}
func (f *fakeSystemCollectionService) SummarizeProfiles(
	_ context.Context, filter types.AgentCollectionProfileFilter,
) (*types.AgentCollectionSummary, error) {
	f.filter = filter
	return f.summary, nil
}
func (f *fakeSystemCollectionService) GetProfileByID(context.Context, string) (*types.AgentCollectionProfile, error) {
	return f.profile, nil
}
func (f *fakeSystemCollectionService) ListHistory(context.Context, string, int, int) (*types.AgentCollectionHistoryPage, error) {
	return f.history, nil
}
func (f *fakeSystemCollectionService) UpdateAsSystemAdmin(
	_ context.Context, input types.SystemAdminCollectionUpdateInput,
) (*types.AgentCollectionProfile, error) {
	f.adminUpdate = input
	return f.profile, nil
}
func (f *fakeSystemCollectionService) PurgeProfile(_ context.Context, profileID string) error {
	f.purged = profileID
	return nil
}
func (f *fakeSystemCollectionService) GetExport(context.Context, string) (*types.AgentCollectionExport, error) {
	return f.export, nil
}

type fakeSystemCollectionAgents struct {
	interfaces.CustomAgentService
	agent *types.CustomAgent
}

func (f *fakeSystemCollectionAgents) GetAgentByIDAndTenant(
	context.Context, string, uint64,
) (*types.CustomAgent, error) {
	return f.agent, nil
}

func systemCollectionTestHandler(service *fakeSystemCollectionService) *SystemAgentCollectionHandler {
	agent := &types.CustomAgent{ID: "agent-1", TenantID: 7, Name: "法务助手", Config: collectionHandlerConfig()}
	return NewSystemAgentCollectionHandler(service, &fakeSystemCollectionAgents{agent: agent}, nil)
}

func collectionHandlerConfig() types.CustomAgentConfig {
	return types.CustomAgentConfig{CollectionSchemaVersion: 2, CollectionFields: []types.AgentCollectionField{
		{Key: "status", Label: "状态", Type: types.AgentCollectionShortText, Enabled: true, Order: 1},
	}}
}

func collectionHandlerRouter(handler *SystemAgentCollectionHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "admin-1")
		c.Request = c.Request.WithContext(ctx)
	})
	router.GET("/profiles", handler.ListProfiles)
	router.PUT("/profiles/:profile_id/fields/:field_key", handler.UpdateField)
	router.DELETE("/profiles/:profile_id", handler.PurgeProfile)
	router.GET("/exports/:export_id", handler.GetExport)
	return router
}

func TestSystemAgentCollectionCompletedExportRequiresDownloadFlag(t *testing.T) {
	path := t.TempDir() + "/profiles.csv"
	require.NoError(t, os.WriteFile(path, []byte("profile"), 0o600))
	service := &fakeSystemCollectionService{export: &types.AgentCollectionExport{
		ID: "export-1", ActorUserID: "admin-1", Status: types.AgentCollectionExportCompleted,
		StoragePath: path, Filename: "profiles.csv",
	}}
	router := collectionHandlerRouter(systemCollectionTestHandler(service))

	response := performCollectionHandlerRequest(router, http.MethodGet, "/exports/export-1", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Header().Get("Content-Type"), "application/json")
	require.Contains(t, response.Body.String(), `"status":"completed"`)
	require.NotContains(t, response.Body.String(), "storage_path")

	response = performCollectionHandlerRequest(router, http.MethodGet, "/exports/export-1?download=1", "")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "profile", response.Body.String())
}

func TestSystemAgentCollectionListReturnsSummaryAndFilters(t *testing.T) {
	complete := false
	service := &fakeSystemCollectionService{
		page:    &types.AgentCollectionProfilePage{Items: []*types.AgentCollectionProfile{}, Total: 0, Page: 2, PageSize: 25},
		summary: &types.AgentCollectionSummary{Users: 4, Profiles: 5, UpdatedToday: 2, Incomplete: 3},
	}
	router := collectionHandlerRouter(systemCollectionTestHandler(service))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/profiles?tenant_id=8&agent_id=agent-1&user_id=user-1&keyword=case&complete=false&field_key=status&field_value=open&page=2&page_size=25", nil)
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, uint64(8), service.filter.TenantID)
	require.Equal(t, "status", service.filter.FieldKey)
	require.Equal(t, "open", service.filter.FieldValue)
	require.Equal(t, complete, *service.filter.Complete)
	require.Contains(t, response.Body.String(), `"incomplete":3`)
}

func TestSystemAgentCollectionUpdateAndPurgeRequireReasons(t *testing.T) {
	profile := &types.AgentCollectionProfile{ID: "profile-1", AgentID: "agent-1", AgentTenantID: 7}
	service := &fakeSystemCollectionService{profile: profile}
	router := collectionHandlerRouter(systemCollectionTestHandler(service))

	response := performCollectionHandlerRequest(router, http.MethodPut, "/profiles/profile-1/fields/status", `{"value":"open"}`)
	require.Equal(t, http.StatusBadRequest, response.Code)
	response = performCollectionHandlerRequest(router, http.MethodPut, "/profiles/profile-1/fields/status", `{"value":"open","reason":"用户申请更正"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "admin-1", service.adminUpdate.ActorUserID)
	require.Equal(t, "用户申请更正", service.adminUpdate.ChangeReason)

	response = performCollectionHandlerRequest(router, http.MethodDelete, "/profiles/profile-1", `{"confirmation":"PURGE","reason":"合规删除"}`)
	require.Equal(t, http.StatusBadRequest, response.Code)
	response = performCollectionHandlerRequest(router, http.MethodDelete, "/profiles/profile-1", `{"confirmation":"profile-1","reason":"合规删除"}`)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "profile-1", service.purged)
}

func performCollectionHandlerRequest(router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	return response
}

func TestAgentCollectionExportFilesHaveStableHeaders(t *testing.T) {
	profiles := []*types.AgentCollectionProfile{{
		TenantID: 8, AgentTenantID: 7, AgentID: "agent-1", UserID: "user-1", IsComplete: true,
		Values: types.JSONMap{"status": map[string]any{"value": "open"}}, UpdatedAt: time.Now().UTC(),
	}}
	fields := []string{"status"}

	csvPath, err := writeAgentCollectionCSV(t.TempDir(), "export-1", profiles, fields)
	require.NoError(t, err)
	data, err := os.ReadFile(csvPath)
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}))
	rows, err := csv.NewReader(bytes.NewReader(data[3:])).ReadAll()
	require.NoError(t, err)
	require.Equal(t, "tenant_id", rows[0][0])
	require.Equal(t, "status", rows[0][6])

	xlsxPath, err := writeAgentCollectionXLSX(t.TempDir(), "export-2", profiles, fields)
	require.NoError(t, err)
	book, err := excelize.OpenFile(xlsxPath)
	require.NoError(t, err)
	defer book.Close()
	rows, err = book.GetRows("Profiles")
	require.NoError(t, err)
	require.Equal(t, "status", rows[0][6])
	require.Equal(t, "open", rows[1][6])
	require.Equal(t, "'=HYPERLINK(\"bad\")", formatAgentCollectionValue(map[string]any{"value": `=HYPERLINK("bad")`}))
}
