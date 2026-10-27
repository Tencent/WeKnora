package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestCurrentWorkspaceProfilePersistsOnlyRequestedFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Tenant{}))
	for _, tenant := range []*types.Tenant{
		{
			ID:           7,
			Name:         "Team",
			Description:  "Original",
			Status:       "active",
			StorageQuota: 1024,
			StorageUsed:  128,
			Business:     "preserved",
		},
		{ID: 99, Name: "Other workspace", Description: "Do not change", Status: "active"},
	} {
		require.NoError(t, db.Create(tenant).Error)
	}
	svc := service.NewTenantService(repository.NewTenantRepository(db), nil, nil)
	h := &TenantHandler{service: svc}
	caller := types.Caller{TenantID: 7, UserID: "owner", Role: types.TenantRoleOwner}
	enforce := true
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforce}}
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		// Execution scope must not override the authenticated workspace.
		ctx := types.WithCaller(c.Request.Context(), caller)
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(99))
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.GET("/api/v1/tenants/current", middleware.RequireRole(types.TenantRoleViewer, cfg), h.GetCurrentWorkspace)
	r.PATCH("/api/v1/tenants/current", middleware.RequireRole(types.TenantRoleOwner, cfg), h.UpdateCurrentWorkspace)
	request := func(method, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/v1/tenants/current?tenant_id=99", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, body := range []string{`{"name":" New name "}`, `{"description":""}`} {
		w := request("PATCH", body)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	w := request("GET", "")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	op, _ := operations.Builtin().Get("workspace.get")
	projected, err := op.Project(w.Body.Bytes())
	require.NoError(t, err)
	assert.Equal(t, "New name", projected.Label("name"))
	var saved types.Tenant
	require.NoError(t, db.First(&saved, 7).Error)
	assert.Empty(t, saved.Description, "empty descriptions must persist to the database")
	assert.Equal(t, int64(1024), saved.StorageQuota)
	assert.Equal(t, int64(128), saved.StorageUsed)
	assert.Equal(t, "preserved", saved.Business)
	other, err := svc.GetTenantByID(context.Background(), 99)
	require.NoError(t, err)
	assert.Equal(t, "Other workspace", other.Name)
	assert.Equal(t, "Do not change", other.Description)
	for _, body := range []string{
		`{}`,
		`{"description":null}`,
		`{"name":"  "}`,
		`{"name":"` + strings.Repeat(
			"a",
			129,
		) + `"}`,
		`{"description":"` + strings.Repeat(
			"a",
			513,
		) + `"}`,
	} {
		assert.Equal(t, http.StatusBadRequest, request("PATCH", body).Code, body)
	}
	// An unrecognized privileged field cannot change stored settings.
	require.Equal(
		t,
		http.StatusOK,
		request("PATCH", `{"description":"Updated","storage_quota":9999,"status":"suspended"}`).Code,
	)
	saved = types.Tenant{}
	require.NoError(t, db.First(&saved, 7).Error)
	assert.Equal(t, int64(1024), saved.StorageQuota)
	assert.Equal(t, "active", saved.Status)
	assert.Equal(t, "New name", saved.Name, "omitted name must remain unchanged")
	for _, role := range []types.TenantRole{
		types.TenantRoleViewer,
		types.TenantRoleContributor,
		types.TenantRoleAdmin,
	} {
		caller.Role = role
		assert.Equal(t, http.StatusForbidden, request("PATCH", `{"name":"Denied"}`).Code)
		assert.Equal(t, http.StatusOK, request("GET", "").Code)
	}
	caller.TenantID = 0
	assert.Equal(t, http.StatusUnauthorized, request("GET", "").Code)
}

func TestCurrentWorkspaceMembersPaginationAndProjection(t *testing.T) {
	ms := &stubMemberService{
		listMembersPage: func(
			_ context.Context,
			id uint64,
			q string,
			page, size int,
		) (
			[]*types.TenantMember,
			int64,
			error,
		) {
			assert.Equal(t, uint64(7), id)
			assert.Equal(t, "Alice", q)
			assert.Equal(t, 2, page)
			assert.Equal(t, 1, size)
			return []*types.TenantMember{
				{UserID: "u1", Role: types.TenantRoleAdmin, Status: types.TenantMemberStatusActive},
			}, 2, nil
		},
	}
	us := &stubMemberUserService{getByIDs: func(_ context.Context, _ []string) (map[string]*types.User, error) {
		return map[string]*types.User{"u1": {ID: "u1", Username: "Alice", Email: "a@example.com"}}, nil
	}}
	h := NewTenantMemberHandler(ms, us)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		c.Request = c.Request.WithContext(
			types.WithCaller(c.Request.Context(), types.Caller{TenantID: 7, Role: types.TenantRoleViewer}),
		)
		c.Next()
	})
	r.GET("/api/v1/tenants/current/members", h.ListCurrentWorkspaceMembers)
	op, _ := operations.Builtin().Get("workspace.list_members")
	params := map[string]json.RawMessage{
		"q":         json.RawMessage(`"Alice"`),
		"page":      json.RawMessage(`2`),
		"page_size": json.RawMessage(`1`),
	}
	call, err := op.Build(params)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(call.Method, call.URI(), nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	out, err := op.Project(w.Body.Bytes())
	require.NoError(t, err)
	blob, err := json.Marshal(out)
	require.NoError(t, err)
	assert.Contains(t, string(blob), `"total":2`)
	assert.Contains(t, string(blob), `"role":"admin"`)
	assert.Contains(t, string(blob), `"email":"a@example.com"`)
}

func TestSystemInformationOperationUsesActualResponse(t *testing.T) {
	t.Setenv("RETRIEVE_DRIVER", "")
	h := &SystemHandler{}
	r := gin.New()
	r.GET("/api/v1/system/info", h.GetSystemInfo)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/system/info", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	op, _ := operations.Builtin().Get("system.get_info")
	out, err := op.Project(w.Body.Bytes())
	require.NoError(t, err)
	assert.Equal(t, Version, out.Label("version"))
	assert.Equal(t, Edition, out.Label("edition"))
	blob, err := json.Marshal(out)
	require.NoError(t, err)
	assert.NotContains(t, string(blob), "db_migration_error")
}
