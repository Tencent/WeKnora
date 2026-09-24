package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// spyPolicyStore 记录 InvalidateTenant 调用，验证策略变更触发缓存失效
// （设计 §8.3：任何策略变更都必须按租户失效缓存）。
type spyPolicyStore struct {
	mu          sync.Mutex
	invalidated []uint64
}

func (s *spyPolicyStore) Resolve(_ context.Context, _ intentgate.ScopeQuery) (*types.IntentPolicy, error) {
	return nil, nil
}

func (s *spyPolicyStore) InvalidateTenant(tenantID uint64) {
	s.mu.Lock()
	s.invalidated = append(s.invalidated, tenantID)
	s.mu.Unlock()
}

func (s *spyPolicyStore) invalidateCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.invalidated)
}

func newIntentPolicyHandlerTestRepo(t *testing.T) interfaces.IntentPolicyRepository {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.IntentPolicy{}))
	return repository.NewIntentPolicyRepository(db)
}

// newIntentPolicyTestEngine 构造 handler 级测试引擎：注入租户上下文 +
// ErrorHandler（c.Error 的 AppError 由它渲染状态码）。
func newIntentPolicyTestEngine(t *testing.T, tenantID uint64, h *IntentPolicyHandler) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, tenantID)
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user-admin-1")
		c.Request = c.Request.WithContext(ctx)
		c.Set(types.TenantIDContextKey.String(), tenantID)
		c.Set(types.UserIDContextKey.String(), "user-admin-1")
		c.Next()
	})
	g := r.Group("/api/v1/intent-policies")
	g.POST("", h.CreatePolicy)
	g.GET("", h.ListPolicies)
	g.GET("/:id", h.GetPolicy)
	g.PUT("/:id", h.UpdatePolicy)
	g.POST("/:id/disable", h.DisablePolicy)
	g.POST("/:id/enable", h.EnablePolicy)
	return r
}

func doPolicyJSON(t *testing.T, engine *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

type intentPolicyResponse struct {
	Success bool                `json:"success"`
	Data    *types.IntentPolicy `json:"data"`
}

type intentPolicyListResponse struct {
	Success bool                  `json:"success"`
	Data    []*types.IntentPolicy `json:"data"`
}

func decodePolicy(t *testing.T, rec *httptest.ResponseRecorder) *types.IntentPolicy {
	t.Helper()
	var resp intentPolicyResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Data, "body=%s", rec.Body.String())
	return resp.Data
}

func validCreateBody() map[string]any {
	return map[string]any{
		"scope_type":      types.PolicyScopeTool,
		"scope_ref":       "*:wiki_delete_page",
		"constraint_text": "删除页面前必须有用户明确提到删",
	}
}

func TestIntentPolicyCreateDefaultsToObserve(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	spy := &spyPolicyStore{}
	h := NewIntentPolicyHandler(repo, spy)
	engine := newIntentPolicyTestEngine(t, 1, h)

	rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())

	p := decodePolicy(t, rec)
	require.Equal(t, types.VerdictModeObserve, p.Mode, "mode 缺省必须为 observe")
	require.Equal(t, types.RiskTierLow, p.RiskTier, "risk_tier 缺省必须为 low")
	require.Equal(t, 1, p.Version)
	require.True(t, p.Enabled)
	require.Equal(t, uint64(1), p.TenantID)
	require.Equal(t, "user-admin-1", p.CreatedBy)
	require.Equal(t, 1, spy.invalidateCount(), "创建后必须失效该租户的策略缓存")
}

func TestIntentPolicyCreateExplicitEnforce(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	h := NewIntentPolicyHandler(repo, nil) // store 为 nil 时不得 panic
	engine := newIntentPolicyTestEngine(t, 1, h)

	body := validCreateBody()
	body["mode"] = types.VerdictModeEnforce
	body["risk_tier"] = types.RiskTierHigh
	rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", body)
	require.Equal(t, http.StatusCreated, rec.Code, "body=%s", rec.Body.String())

	p := decodePolicy(t, rec)
	require.Equal(t, types.VerdictModeEnforce, p.Mode)
	require.Equal(t, types.RiskTierHigh, p.RiskTier)
}

func TestIntentPolicyCreateValidation(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	h := NewIntentPolicyHandler(repo, nil)
	engine := newIntentPolicyTestEngine(t, 1, h)

	cases := map[string]map[string]any{
		"missing constraint_text": {
			"scope_type": types.PolicyScopeTool,
			"scope_ref":  "*:wiki_delete_page",
		},
		"unknown scope_type": {
			"scope_type":      "galaxy",
			"constraint_text": "x",
		},
		"tool scope without scope_ref": {
			"scope_type":      types.PolicyScopeTool,
			"constraint_text": "x",
		},
		"unknown mode": {
			"scope_type":      types.PolicyScopeTenant,
			"constraint_text": "x",
			"mode":            "yolo",
		},
		"unknown risk_tier": {
			"scope_type":      types.PolicyScopeTenant,
			"constraint_text": "x",
			"risk_tier":       "extreme",
		},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", body)
			require.Equal(t, http.StatusBadRequest, rec.Code, "body=%s", rec.Body.String())
		})
	}
}

func TestIntentPolicyCreateDuplicateLineageConflict(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	h := NewIntentPolicyHandler(repo, nil)
	engine := newIntentPolicyTestEngine(t, 1, h)

	rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	require.Equal(t, http.StatusCreated, rec.Code)

	// 同一谱系（scope_type + scope_ref）重复 POST 必须 409——修改走 PUT。
	rec = doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	require.Equal(t, http.StatusConflict, rec.Code, "body=%s", rec.Body.String())
}

func TestIntentPolicyUpdateCreatesNewVersionAndKeepsOld(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	spy := &spyPolicyStore{}
	h := NewIntentPolicyHandler(repo, spy)
	engine := newIntentPolicyTestEngine(t, 1, h)

	rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	require.Equal(t, http.StatusCreated, rec.Code)
	v1 := decodePolicy(t, rec)

	// PUT 更新：version 1 → 2，谱系（scope）不变，内容换新。
	update := map[string]any{
		"constraint_text": "删除页面前必须有用户明确提到删，且页面超过 30 天未更新",
		"rule_expr":       "value <= 75",
		"arg_path":        "$.amount",
	}
	rec = doPolicyJSON(t, engine, http.MethodPut, "/api/v1/intent-policies/"+v1.ID, update)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	v2 := decodePolicy(t, rec)
	require.Equal(t, 2, v2.Version)
	require.NotEqual(t, v1.ID, v2.ID, "新版本必须是新行")
	require.Equal(t, v1.ScopeType, v2.ScopeType)
	require.Equal(t, v1.ScopeRef, v2.ScopeRef)
	require.NotNil(t, v2.RuleExpr)
	require.Equal(t, "value <= 75", *v2.RuleExpr)
	require.NotNil(t, v2.ArgPath)
	require.Equal(t, "$.amount", *v2.ArgPath)

	// v1 仍可查（旧版本保留，设计 §3.2）。
	rec = doPolicyJSON(t, engine, http.MethodGet, "/api/v1/intent-policies/"+v1.ID, nil)
	require.Equal(t, http.StatusOK, rec.Code, "v1 必须仍可查询, body=%s", rec.Body.String())
	require.Equal(t, 1, decodePolicy(t, rec).Version)

	// 再 PUT v2 → version 3（基于谱系最大 version，不是目标行 version）。
	rec = doPolicyJSON(t, engine, http.MethodPut, "/api/v1/intent-policies/"+v2.ID, update)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.Equal(t, 3, decodePolicy(t, rec).Version)

	// 列表应含 v1/v2/v3 全部版本。
	rec = doPolicyJSON(t, engine, http.MethodGet, "/api/v1/intent-policies", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list intentPolicyListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Data, 3)
	require.Equal(t, 3, spy.invalidateCount(), "每次变更都要失效缓存")
}

func TestIntentPolicyUpdateInheritsDisabledState(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	h := NewIntentPolicyHandler(repo, nil)
	engine := newIntentPolicyTestEngine(t, 1, h)

	rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	v1 := decodePolicy(t, rec)

	rec = doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies/"+v1.ID+"/disable", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.False(t, decodePolicy(t, rec).Enabled)

	// 停用后 PUT：新版本继承停用状态，不会因更新而悄悄复活。
	rec = doPolicyJSON(t, engine, http.MethodPut, "/api/v1/intent-policies/"+v1.ID,
		map[string]any{"constraint_text": "新措辞"})
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.False(t, decodePolicy(t, rec).Enabled)
}

func TestIntentPolicyEnableDisable(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	spy := &spyPolicyStore{}
	h := NewIntentPolicyHandler(repo, spy)
	engine := newIntentPolicyTestEngine(t, 1, h)

	rec := doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	v1 := decodePolicy(t, rec)
	require.True(t, v1.Enabled)
	before := spy.invalidateCount()

	rec = doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies/"+v1.ID+"/disable", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.False(t, decodePolicy(t, rec).Enabled)

	rec = doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies/"+v1.ID+"/enable", nil)
	require.Equal(t, http.StatusOK, rec.Code, "body=%s", rec.Body.String())
	require.True(t, decodePolicy(t, rec).Enabled)
	require.Equal(t, before+2, spy.invalidateCount(), "启停都要失效缓存")

	// 停用不存在的策略 → 404。
	rec = doPolicyJSON(t, engine, http.MethodPost, "/api/v1/intent-policies/nope/disable", nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestIntentPolicyCrossTenantIsolation(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	h := NewIntentPolicyHandler(repo, nil)

	// 租户 1 建一条策略。
	engineT1 := newIntentPolicyTestEngine(t, 1, h)
	rec := doPolicyJSON(t, engineT1, http.MethodPost, "/api/v1/intent-policies", validCreateBody())
	require.Equal(t, http.StatusCreated, rec.Code)
	victim := decodePolicy(t, rec)

	// 租户 2 对该策略的任何访问都必须 404（不暴露存在性）。
	engineT2 := newIntentPolicyTestEngine(t, 2, h)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/intent-policies/" + victim.ID},
		{http.MethodPut, "/api/v1/intent-policies/" + victim.ID},
		{http.MethodPost, "/api/v1/intent-policies/" + victim.ID + "/disable"},
		{http.MethodPost, "/api/v1/intent-policies/" + victim.ID + "/enable"},
	} {
		rec := doPolicyJSON(t, engineT2, tc.method, tc.path,
			map[string]any{"constraint_text": "x"})
		require.Equal(t, http.StatusNotFound, rec.Code,
			"%s %s 跨租户必须 404, body=%s", tc.method, tc.path, rec.Body.String())
	}

	// 租户 2 的列表不含租户 1 的策略。
	rec = doPolicyJSON(t, engineT2, http.MethodGet, "/api/v1/intent-policies", nil)
	require.Equal(t, http.StatusOK, rec.Code)
	var list intentPolicyListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Empty(t, list.Data)
}

func TestIntentPolicyGetNotFound(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	h := NewIntentPolicyHandler(repo, nil)
	engine := newIntentPolicyTestEngine(t, 1, h)

	rec := doPolicyJSON(t, engine, http.MethodGet, "/api/v1/intent-policies/"+uuid.NewString(), nil)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

// TestIsDuplicatePolicy 覆盖唯一冲突的判定矩阵（review 修复的并发兜底：
// read-then-insert 挡不住并发双插入，数据库唯一索引是最终裁决）。
func TestIsDuplicatePolicy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"gorm sentinel", gorm.ErrDuplicatedKey, true},
		{"sqlite message wrapped", fmt.Errorf("create intent policy: %w", errors.New("UNIQUE constraint failed: intent_policies.tenant_id")), true},
		{"postgres message wrapped", fmt.Errorf("create intent policy: %w", errors.New(`pq: duplicate key value violates unique constraint "idx_intent_policies_lineage_version"`)), true},
		{"unrelated", errors.New("connection reset"), false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, isDuplicatePolicy(tc.err), tc.name)
	}
}

// TestIntentPolicyLineageVersionUniqueEnforced：唯一索引真实落库——同
// (tenant, scope_type, scope_ref, version) 的第二行必须被数据库拒绝
// （AutoMigrate 依据模型的 uniqueIndex tag 建索引，与迁移 000114/000034
// 同约束）。
func TestIntentPolicyLineageVersionUniqueEnforced(t *testing.T) {
	repo := newIntentPolicyHandlerTestRepo(t)
	ctx := context.Background()
	first := validCreatedPolicy(t, ctx, repo, 1)
	dup := *first
	dup.ID = uuid.NewString()
	require.Error(t, repo.Create(ctx, &dup), "同谱系同 version 的第二行必须被唯一索引拒绝")
	require.True(t, isDuplicatePolicy(repo.Create(ctx, &dup)),
		"repo 返回的冲突错误必须能被 isDuplicatePolicy 识别（→ handler 409）")
}

// validCreatedPolicy 造一条已落库的策略（v=version）。
func validCreatedPolicy(t *testing.T, ctx context.Context, repo interfaces.IntentPolicyRepository, version int) *types.IntentPolicy {
	t.Helper()
	p := &types.IntentPolicy{
		ID:             uuid.NewString(),
		TenantID:       1,
		ScopeType:      "tool",
		ScopeRef:       fmt.Sprintf("mcp_x_%s:*", uuid.NewString()[:8]),
		ConstraintText: "c",
		RiskTier:       types.RiskTierLow,
		Mode:           types.VerdictModeObserve,
		Version:        version,
		Enabled:        true,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	require.NoError(t, repo.Create(ctx, p))
	return p
}
