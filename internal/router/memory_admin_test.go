package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/application/service/memory"
	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type memoryAdminTenantRepo struct{ interfaces.TenantRepository }

const (
	memoryAdminTargetID  = "10000000-0000-0000-0000-000000000001"
	memoryAdminSelfID    = "10000000-0000-0000-0000-000000000002"
	memoryAdminOtherID   = "10000000-0000-0000-0000-000000000003"
	memoryAdminForeignID = "20000000-0000-0000-0000-000000000001"
	memoryAdminMissingID = "30000000-0000-0000-0000-000000000001"
)

func (*memoryAdminTenantRepo) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	return &types.Tenant{ID: id, MemoryConfig: &types.MemoryConfig{Enabled: true}}, nil
}

type memoryAdminAudit struct {
	interfaces.AuditLogService
	entries []*types.AuditLog
	err     error
}

func (a *memoryAdminAudit) Log(_ context.Context, entry *types.AuditLog) error {
	a.entries = append(a.entries, entry)
	return a.err
}

func (*memoryAdminAudit) LogDenied(context.Context, *gin.Context, uint64, string, string, types.TenantRole) error {
	return nil
}

type memoryAdminHarness struct {
	db    *gorm.DB
	r     *gin.Engine
	audit *memoryAdminAudit
}

func newMemoryAdminHarness(t *testing.T, enforceRBAC bool) *memoryAdminHarness {
	t.Helper()
	return newMemoryAdminHarnessWithAudit(t, enforceRBAC, false)
}

func newMemoryAdminHarnessWithAudit(t *testing.T, enforceRBAC, persistentAudit bool) *memoryAdminHarness {
	t.Helper()
	previousMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(previousMode) })
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())),
		&gorm.Config{Logger: gormlogger.Discard})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.MemorySubject{}, &types.MemoryItem{}, &types.MemoryTombstone{},
		&types.MemoryTopicStat{}, &types.MemoryDocAffinity{},
		&types.MemoryItemEmbedding{}, &types.MemoryExtractionSession{}))
	cfg := &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enforceRBAC}}
	svc := memory.NewMemoryService(repository.NewMemoryRepository(db), &memoryAdminTenantRepo{}, nil, nil, nil, cfg)
	audit := &memoryAdminAudit{}
	var auditService interfaces.AuditLogService = audit
	if persistentAudit {
		require.NoError(t, db.AutoMigrate(&types.AuditLog{}))
		auditService = service.NewAuditLogService(repository.NewAuditLogRepository(db))
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler(), middleware.AuditServiceProvider(auditService))
	g := &rbacGuards{cfg: cfg, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	api := r.Group("/api/v1", g.apiKeyAuthorizer.Middleware())
	RegisterMemoryRoutes(api, handler.NewMemoryHandler(svc), g)
	if persistentAudit {
		api.GET("/tenants/:id/audit-log", g.PathTenantMatch(), g.Admin(),
			handler.NewAuditLogHandler(auditService).ListTenantAuditLog)
	}
	return &memoryAdminHarness{db: db, r: r, audit: audit}
}

func (h *memoryAdminHarness) seed(t *testing.T, tenantID uint64, id, subject, status string) string {
	t.Helper()
	require.NoError(t, h.db.Create(&types.MemorySubject{
		ID: id, TenantID: tenantID, SubjectID: subject, Enabled: true, BlockText: "private profile text",
	}).Error)
	itemID := memoryAdminItemID(id)
	require.NoError(t, h.db.Create(&types.MemoryItem{
		ID: itemID, TenantID: tenantID, SubjectID: subject, Kind: types.MemoryKindFact,
		Content: "private memory content", Status: status, NormalizedKey: itemID,
	}).Error)
	return itemID
}

func memoryAdminItemID(subjectID string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(subjectID)).String()
}

func memoryAdminContext(t *testing.T, role types.TenantRole) context.Context {
	t.Helper()
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(1))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "operator")
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "operator"})
	return types.WithCaller(ctx, types.Caller{TenantID: 1, UserID: "operator", Role: role})
}

func (h *memoryAdminHarness) request(
	ctx context.Context, t *testing.T, method, path, body string, code int,
) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, "/api/v1"+path, strings.NewReader(body)).WithContext(ctx)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	h.r.ServeHTTP(w, req)
	require.Equal(t, code, w.Code, "%s %s: %s", method, path, w.Body.String())
	return w
}

// TestMemoryAdminRoutesLifecycle 验证管理员只修改目标主体，审计保留操作者且不泄露记忆正文。
func TestMemoryAdminRoutesLifecycle(t *testing.T) {
	for _, role := range []types.TenantRole{types.TenantRoleAdmin, types.TenantRoleOwner} {
		t.Run(string(role), func(t *testing.T) {
			h := newMemoryAdminHarness(t, true)
			itemID := h.seed(t, 1, memoryAdminTargetID, "im_user:channel/person", types.MemoryStatusPending)
			h.seed(t, 1, memoryAdminSelfID, "web_user:operator", types.MemoryStatusActive)
			h.seed(t, 2, memoryAdminForeignID, "im_user:channel/person", types.MemoryStatusActive)
			ctx := types.WithExecutionTenant(memoryAdminContext(t, role), 2)
			w := h.request(ctx, t, http.MethodGet, "/admin/memory/subjects?limit=1&offset=0", "", 200)
			require.NotContains(t, w.Body.String(), "private profile text")
			require.NotContains(t, w.Body.String(), "private memory content")
			var list struct {
				Data  []json.RawMessage `json:"data"`
				Total int64             `json:"total"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
			require.Len(t, list.Data, 1)
			require.EqualValues(t, 2, list.Total)
			targetPath := "/admin/memory/subjects/" + memoryAdminTargetID
			h.request(ctx, t, http.MethodGet, targetPath+"/items?status=pending", "", 200)
			h.request(ctx, t, http.MethodPost, targetPath+"/items/"+itemID+"/confirm", "", 200)
			h.request(ctx, t, http.MethodPost, targetPath+"/items/"+itemID+"/confirm", "", 200)
			h.request(ctx, t, http.MethodPost, targetPath+"/items/"+itemID+"/reject", "", 409)
			archivedID := "40000000-0000-0000-0000-000000000001"
			require.NoError(t, h.db.Create(&types.MemoryItem{
				ID: archivedID, TenantID: 1, SubjectID: "im_user:channel/person", Kind: types.MemoryKindFact,
				Content: "private archived memory", Status: types.MemoryStatusArchived,
			}).Error)
			h.request(ctx, t, http.MethodPost, targetPath+"/items/"+archivedID+"/confirm", "", 409)
			h.request(ctx, t, http.MethodPut, targetPath+"/settings", `{"enabled":false}`, 200)
			h.request(ctx, t, http.MethodDelete, targetPath+"/items", "", 200)
			var target types.MemorySubject
			require.NoError(t, h.db.First(&target, "id = ?", memoryAdminTargetID).Error)
			require.False(t, target.Enabled)
			var count int64
			require.NoError(t, h.db.Model(&types.MemoryItem{}).Count(&count).Error)
			require.EqualValues(t, 2, count)
			self := h.request(ctx, t, http.MethodGet, "/memory/items?subject_id=im_user:channel/person", "", 200)
			require.Contains(t, self.Body.String(), memoryAdminItemID(memoryAdminSelfID))
			require.NotContains(t, self.Body.String(), memoryAdminItemID(memoryAdminForeignID))
			require.NotEmpty(t, h.audit.entries)
			actions := map[types.AuditAction]int{}
			for _, entry := range h.audit.entries {
				require.EqualValues(t, 1, entry.TenantID)
				require.Equal(t, "operator", entry.ActorUserID)
				require.Equal(t, string(role), entry.ActorRole)
				require.Empty(t, entry.ScopeType)
				require.Empty(t, entry.ScopeID)
				if entry.Action == types.AuditAction("memory.subjects_listed") {
					require.Equal(t, "workspace", entry.TargetType)
					require.Equal(t, "1", entry.TargetID)
					require.JSONEq(t, `{"limit":1,"offset":0,"count":1}`, string(entry.Details))
				} else {
					require.Equal(t, "memory_subject", entry.TargetType)
					require.Equal(t, memoryAdminTargetID, entry.TargetID)
				}
				require.Empty(t, entry.TargetUserID)
				require.Equal(t, types.AuditOutcomeSuccess, entry.Outcome)
				encoded, err := json.Marshal(entry)
				require.NoError(t, err)
				require.NotContains(t, string(encoded), "private")
				actions[entry.Action]++
			}
			require.Positive(t, actions[types.AuditAction("memory.subject_inspected")])
			require.Equal(t, 1, actions[types.AuditAction("memory.subjects_listed")])
			require.Equal(t, 2, actions[types.AuditAction("memory.item_confirmed")])
			require.Equal(t, 1, actions[types.AuditAction("memory.subject_cleared")])
			require.Equal(t, 1, actions[types.AuditAction("memory.subject_settings_updated")])
		})
	}
}

// TestMemoryAdminRoutesAuthorization 验证 RBAC 开关不放宽管理权限，所有管理路由默认拒绝 API Key。
func TestMemoryAdminRoutesAuthorization(t *testing.T) {
	for _, enforce := range []bool{true, false} {
		t.Run(fmt.Sprint(enforce), func(t *testing.T) {
			h := newMemoryAdminHarness(t, enforce)
			h.seed(t, 1, memoryAdminTargetID, "api_external_user:person", types.MemoryStatusPending)
			targetPath := "/admin/memory/subjects/" + memoryAdminTargetID
			routes := []struct{ method, path, body string }{
				{http.MethodGet, "/admin/memory/subjects", ""},
				{http.MethodGet, targetPath + "/items", ""},
				{http.MethodPost, targetPath + "/items/" + memoryAdminItemID(memoryAdminTargetID) + "/confirm", ""},
				{http.MethodPost, targetPath + "/items/" + memoryAdminItemID(memoryAdminTargetID) + "/reject", ""},
				{http.MethodDelete, targetPath + "/items", ""},
				{http.MethodPut, targetPath + "/settings", `{"enabled":false}`},
			}
			for _, role := range []types.TenantRole{types.TenantRoleViewer, types.TenantRoleContributor} {
				for _, route := range routes {
					h.request(memoryAdminContext(t, role), t, route.method, route.path, route.body, 403)
				}
				personal := h.request(memoryAdminContext(t, role), t, http.MethodGet,
					"/memory/items?subject_id="+memoryAdminTargetID, "", 200)
				require.NotContains(t, personal.Body.String(), "private memory content")
			}
			for _, caller := range []types.Caller{
				{TenantID: 1, Role: types.TenantRoleAdmin},
				{UserID: "operator", Role: types.TenantRoleAdmin},
			} {
				ctx := types.WithCaller(t.Context(), caller)
				for _, route := range routes {
					h.request(ctx, t, route.method, route.path, route.body, 403)
				}
			}
			ctx := types.WithTenantAPIKeyScope(memoryAdminContext(t, types.TenantRoleOwner), types.TenantAPIKeyScope{
				KeyID: 1, FullAccess: true,
			})
			for _, route := range routes {
				h.request(ctx, t, route.method, route.path, route.body, 403)
			}
		})
	}
}

// TestMemoryAdminRoutesValidationAndIsolation 验证外租户和主体混淆统一返回 404，输入错误不写审计。
func TestMemoryAdminRoutesValidationAndIsolation(t *testing.T) {
	h := newMemoryAdminHarness(t, true)
	itemID := h.seed(t, 1, memoryAdminTargetID, "im_user:person", types.MemoryStatusPending)
	otherItemID := h.seed(t, 1, memoryAdminOtherID, "im_user:other", types.MemoryStatusPending)
	foreignItemID := h.seed(t, 2, memoryAdminForeignID, "im_user:person", types.MemoryStatusPending)
	ctx := memoryAdminContext(t, types.TenantRoleAdmin)
	targetPath := "/admin/memory/subjects/" + memoryAdminTargetID
	for _, id := range []string{memoryAdminMissingID, memoryAdminForeignID, "im_user:person"} {
		h.request(ctx, t, http.MethodGet, "/admin/memory/subjects/"+id+"/items", "", 404)
		h.request(ctx, t, http.MethodPut, "/admin/memory/subjects/"+id+"/settings", `{"enabled":false}`, 404)
		h.request(ctx, t, http.MethodDelete, "/admin/memory/subjects/"+id+"/items", "", 404)
	}
	for _, action := range []string{"confirm", "reject"} {
		for _, id := range []string{"missing-item", otherItemID, foreignItemID} {
			h.request(ctx, t, http.MethodPost, targetPath+"/items/"+id+"/"+action, "", 404)
		}
	}
	for _, body := range []string{"{", "{}", `{"enabled":null}`, `{"enabled":"false"}`} {
		h.request(ctx, t, http.MethodPut, targetPath+"/settings", body, 400)
	}
	h.request(ctx, t, http.MethodGet, targetPath+"/items?status=unknown", "", 400)
	require.Empty(t, h.audit.entries)
	h.request(ctx, t, http.MethodPost, targetPath+"/items/"+itemID+"/reject", "", 200)
	require.Len(t, h.audit.entries, 1)
	require.Equal(t, types.AuditAction("memory.item_rejected"), h.audit.entries[0].Action)
	require.JSONEq(t, fmt.Sprintf(`{"item_id":%q,"status":"rejected"}`, itemID), string(h.audit.entries[0].Details))
}

// TestMemoryAdminAuditFailureIsBestEffort 验证审计故障不改变已提交的业务结果。
func TestMemoryAdminAuditFailureIsBestEffort(t *testing.T) {
	h := newMemoryAdminHarness(t, false)
	h.seed(t, 1, memoryAdminTargetID, "im_user:person", types.MemoryStatusPending)
	h.audit.err = errors.New("audit unavailable")
	ctx := memoryAdminContext(t, types.TenantRoleAdmin)
	targetPath := "/admin/memory/subjects/" + memoryAdminTargetID
	h.request(ctx, t, http.MethodPut, targetPath+"/settings", `{"enabled":false}`, 200)
	var subject types.MemorySubject
	require.NoError(t, h.db.First(&subject, "id = ?", memoryAdminTargetID).Error)
	require.False(t, subject.Enabled)
	h.request(ctx, t, http.MethodGet, targetPath+"/items", "", 200)
}

// TestMemoryAdminRoutesBoundPagination 验证非法分页回落到有界默认值，合法上限和偏移保持有效。
func TestMemoryAdminRoutesBoundPagination(t *testing.T) {
	h := newMemoryAdminHarness(t, true)
	subjects := make([]types.MemorySubject, 205)
	for i := range subjects {
		subjects[i] = types.MemorySubject{
			ID:       uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprint(i))).String(),
			TenantID: 1, SubjectID: fmt.Sprintf("im_user:person/%d", i), Enabled: true,
		}
	}
	require.NoError(t, h.db.Create(&subjects).Error)
	ctx := memoryAdminContext(t, types.TenantRoleAdmin)
	for _, tc := range []struct {
		query string
		count int
	}{
		{"", 50},
		{"?limit=0&offset=-1", 50},
		{"?limit=201&offset=invalid", 50},
		{"?limit=200", 200},
		{"?limit=200&offset=200", 5},
	} {
		w := h.request(ctx, t, http.MethodGet, "/admin/memory/subjects"+tc.query, "", 200)
		var body struct {
			Data  []types.MemorySubjectSummary `json:"data"`
			Total int64                        `json:"total"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		require.Len(t, body.Data, tc.count)
		require.EqualValues(t, 205, body.Total)
	}
	require.Len(t, h.audit.entries, 5, "one audit write per request, independent of page size")
}

// TestMemoryAdminRoutesInternalError 验证存储故障返回通用错误且不泄露 SQL 信息、不写成功审计。
func TestMemoryAdminRoutesInternalError(t *testing.T) {
	h := newMemoryAdminHarness(t, true)
	require.NoError(t, h.db.Migrator().DropTable(&types.MemorySubject{}))
	ctx := memoryAdminContext(t, types.TenantRoleAdmin)
	paths := []string{"/admin/memory/subjects", "/admin/memory/subjects/" + memoryAdminTargetID + "/items"}
	for _, path := range paths {
		w := h.request(ctx, t, http.MethodGet, path, "", 500)
		require.NotContains(t, w.Body.String(), "no such table")
		require.NotContains(t, w.Body.String(), "SELECT")
	}
	require.Empty(t, h.audit.entries)
}

// TestMemoryAdminAuditTenantFeed 验证真实租户审计查询可见六类记忆事件，目标 UUID 规范化且租户和知识库隔离保持不变。
func TestMemoryAdminAuditTenantFeed(t *testing.T) {
	const subjectID = "aabbccdd-0011-2233-4455-66778899aabb"
	for _, pathID := range []string{strings.ReplaceAll(subjectID, "-", ""), strings.ToUpper(subjectID)} {
		t.Run(pathID, func(t *testing.T) {
			h := newMemoryAdminHarnessWithAudit(t, true, true)
			confirmID := h.seed(t, 1, subjectID, "im_user:channel/private-person", types.MemoryStatusPending)
			rejectID := uuid.NewString()
			require.NoError(t, h.db.Create(&types.MemoryItem{
				ID: rejectID, TenantID: 1, SubjectID: "im_user:channel/private-person",
				Kind: types.MemoryKindFact, Content: "private rejected content", Status: types.MemoryStatusPending,
			}).Error)
			ctx := memoryAdminContext(t, types.TenantRoleAdmin)
			operationCtx := types.WithExecutionTenant(ctx, 2)
			targetPath := "/admin/memory/subjects/" + pathID
			h.request(operationCtx, t, http.MethodGet, "/admin/memory/subjects", "", 200)
			h.request(operationCtx, t, http.MethodGet, targetPath+"/items", "", 200)
			h.request(operationCtx, t, http.MethodPost, targetPath+"/items/"+confirmID+"/confirm", "", 200)
			h.request(operationCtx, t, http.MethodPost, targetPath+"/items/"+rejectID+"/reject", "", 200)
			h.request(operationCtx, t, http.MethodPut, targetPath+"/settings", `{"enabled":false}`, 200)
			h.request(operationCtx, t, http.MethodDelete, targetPath+"/items", "", 200)

			t.Run("canonical_target", func(t *testing.T) {
				var entries []*types.AuditLog
				err := h.db.Where("action <> ?", types.AuditActionMemorySubjectsListed).Find(&entries).Error
				require.NoError(t, err)
				require.Len(t, entries, 5)
				for _, entry := range entries {
					require.Equal(t, subjectID, entry.TargetID)
				}
			})

			t.Run("tenant_feed", func(t *testing.T) {
				// 写入知识库作用域事件，确认记忆修复不把知识库私有活动暴露给租户 feed。
				auditService := service.NewAuditLogService(repository.NewAuditLogRepository(h.db))
				require.NoError(t, auditService.Log(ctx, &types.AuditLog{
					TenantID: 1, Action: types.AuditActionKnowledgeCreated,
					ScopeType: "knowledge_base", ScopeID: "private-kb",
					TargetType: "knowledge", TargetID: "private-doc",
				}))
				w := h.request(ctx, t, http.MethodGet, "/tenants/1/audit-log", "", 200)
				var body struct {
					Data []*types.AuditLog `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				require.Len(t, body.Data, 6)
				actions := map[types.AuditAction]int{}
				for _, entry := range body.Data {
					actions[entry.Action]++
					require.EqualValues(t, 1, entry.TenantID)
					require.Equal(t, "operator", entry.ActorUserID)
					require.Equal(t, "admin", entry.ActorRole)
					require.Empty(t, entry.ScopeType)
					require.Empty(t, entry.ScopeID)
					require.Equal(t, types.AuditOutcomeSuccess, entry.Outcome)
					if entry.Action == types.AuditActionMemorySubjectsListed {
						require.Equal(t, "workspace", entry.TargetType)
						require.Equal(t, "1", entry.TargetID)
					} else {
						require.Equal(t, "memory_subject", entry.TargetType)
						require.Equal(t, subjectID, entry.TargetID)
					}
				}
				for _, action := range []types.AuditAction{
					types.AuditActionMemorySubjectsListed, types.AuditActionMemorySubjectInspected,
					types.AuditActionMemoryItemConfirmed, types.AuditActionMemoryItemRejected,
					types.AuditActionMemorySubjectSettingsUpdated, types.AuditActionMemorySubjectCleared,
				} {
					require.Equal(t, 1, actions[action], "missing or duplicated action %s", action)
				}
				require.NotContains(t, w.Body.String(), "private")
				require.NotContains(t, w.Body.String(), "content")
				require.NotContains(t, w.Body.String(), "block_text")
			})

			t.Run("foreign_tenant", func(t *testing.T) {
				foreignCtx := context.WithValue(ctx, types.TenantIDContextKey, uint64(2))
				foreignCtx = types.WithCaller(foreignCtx, types.Caller{
					TenantID: 2, UserID: "foreign-admin", Role: types.TenantRoleAdmin,
				})
				w := h.request(foreignCtx, t, http.MethodGet, "/tenants/2/audit-log", "", 200)
				var body struct {
					Data []*types.AuditLog `json:"data"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
				require.Empty(t, body.Data)
				h.request(foreignCtx, t, http.MethodGet, "/tenants/1/audit-log", "", 403)
			})
		})
	}
}
