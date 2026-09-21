package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func newIntentPolicyTestRepo(t *testing.T) interfaces.IntentPolicyRepository {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.IntentPolicy{}))
	return NewIntentPolicyRepository(db)
}

func mustIntentPolicy(t *testing.T, in types.IntentPolicyInput) *types.IntentPolicy {
	t.Helper()
	p, err := types.NewIntentPolicy(in)
	require.NoError(t, err)
	return p
}

func toolPolicy(t *testing.T, tenantID uint64, ref string, version int) *types.IntentPolicy {
	t.Helper()
	return mustIntentPolicy(t, types.IntentPolicyInput{
		TenantID:       tenantID,
		ScopeType:      types.PolicyScopeTool,
		ScopeRef:       ref,
		ConstraintText: "删除页面前必须有用户明确提到删",
		Version:        version,
	})
}

func TestIntentPolicyRepository_CreateAndGet(t *testing.T) {
	repo := newIntentPolicyTestRepo(t)
	ctx := context.Background()

	p := toolPolicy(t, 1, "svc_wiki:wiki_delete_page", 1)
	require.NoError(t, repo.Create(ctx, p))

	got, err := repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.Equal(t, p.ID, got.ID)
	require.Equal(t, types.PolicyScopeTool, got.ScopeType)
	require.Equal(t, "svc_wiki:wiki_delete_page", got.ScopeRef)
	require.Equal(t, types.RiskTierLow, got.RiskTier)
	require.Equal(t, types.VerdictModeObserve, got.Mode)
	require.True(t, got.Enabled)

	// 跨租户读取一律 not found（租户隔离与 intent_verdicts 对齐）。
	_, err = repo.GetByID(ctx, 2, p.ID)
	require.ErrorIs(t, err, ErrIntentPolicyNotFound)
}

func TestIntentPolicyRepository_CreateRejectsDirty(t *testing.T) {
	repo := newIntentPolicyTestRepo(t)
	ctx := context.Background()

	require.Error(t, repo.Create(ctx, nil), "nil 策略必须拒绝")

	p := toolPolicy(t, 1, "svc:t", 1)
	p.ScopeType = "gateway"
	require.Error(t, repo.Create(ctx, p), "非法 scope_type 必须拒绝")
}

func TestIntentPolicyRepository_ListByTenant(t *testing.T) {
	repo := newIntentPolicyTestRepo(t)
	ctx := context.Background()

	// 同一谱系 v1/v2（旧版本保留）+ 另一条 tenant 级策略 + 别租户的噪声。
	require.NoError(t, repo.Create(ctx, toolPolicy(t, 1, "svc_wiki:wiki_delete_page", 1)))
	require.NoError(t, repo.Create(ctx, toolPolicy(t, 1, "svc_wiki:wiki_delete_page", 2)))
	require.NoError(t, repo.Create(ctx, mustIntentPolicy(t, types.IntentPolicyInput{
		TenantID:       1,
		ScopeType:      types.PolicyScopeTenant,
		ConstraintText: "全租户兜底策略",
	})))
	require.NoError(t, repo.Create(ctx, toolPolicy(t, 2, "svc_wiki:wiki_delete_page", 1)))

	list, err := repo.ListByTenant(ctx, 1)
	require.NoError(t, err)
	require.Len(t, list, 3, "跨 version 全量返回，供解析器取最新")
	for _, p := range list {
		require.Equal(t, uint64(1), p.TenantID, "不得混入其他租户")
	}
	// 确定性排序：(scope_type, scope_ref, version) 升序。
	require.Equal(t, types.PolicyScopeTenant, list[0].ScopeType)
	require.Equal(t, types.PolicyScopeTool, list[1].ScopeType)
	require.Equal(t, 1, list[1].Version)
	require.Equal(t, 2, list[2].Version)
}

func TestIntentPolicyRepository_SetEnabled(t *testing.T) {
	repo := newIntentPolicyTestRepo(t)
	ctx := context.Background()

	p := toolPolicy(t, 1, "svc:t", 1)
	require.NoError(t, repo.Create(ctx, p))

	require.NoError(t, repo.SetEnabled(ctx, 1, p.ID, false))
	got, err := repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.False(t, got.Enabled)
	require.True(t, got.UpdatedAt.After(p.CreatedAt) || got.UpdatedAt.Equal(p.CreatedAt))

	require.NoError(t, repo.SetEnabled(ctx, 1, p.ID, true))
	got, err = repo.GetByID(ctx, 1, p.ID)
	require.NoError(t, err)
	require.True(t, got.Enabled)

	// 不存在 / 跨租户：not found。
	require.ErrorIs(t, repo.SetEnabled(ctx, 1, uuid.NewString(), false), ErrIntentPolicyNotFound)
	require.ErrorIs(t, repo.SetEnabled(ctx, 2, p.ID, false), ErrIntentPolicyNotFound)
}

func TestIntentPolicyRepository_Delete(t *testing.T) {
	repo := newIntentPolicyTestRepo(t)
	ctx := context.Background()

	p := toolPolicy(t, 1, "svc:t", 1)
	require.NoError(t, repo.Create(ctx, p))
	require.NoError(t, repo.Delete(ctx, 1, p.ID))
	_, err := repo.GetByID(ctx, 1, p.ID)
	require.ErrorIs(t, err, ErrIntentPolicyNotFound)

	require.ErrorIs(t, repo.Delete(ctx, 1, p.ID), ErrIntentPolicyNotFound)
	require.ErrorIs(t, repo.Delete(ctx, 2, p.ID), ErrIntentPolicyNotFound)
}
