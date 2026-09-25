package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMCPDirectoryBatchPolicyUsesOneDatabaseQuery(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPToolApproval{}))
	mcpRepo := newFakeMCPRepo()
	ctx := context.Background()
	require.NoError(t, mcpRepo.Create(ctx, &types.MCPService{ID: "svc", TenantID: 7, Enabled: true}))
	svc := NewMCPToolApprovalService(repository.NewMCPToolApprovalRepository(db), mcpRepo)
	require.NoError(t, svc.SetEnabled(ctx, 7, "svc", "tool_0999", false))
	queries := 0
	require.NoError(
		t,
		db.Callback().Query().Before("gorm:query").Register("count_mcp_policy_queries", func(*gorm.DB) { queries++ }),
	)
	names := make([]string, 1000)
	for i := range names {
		names[i] = fmt.Sprintf("tool_%04d", i)
	}
	gate := approval.NewGate(nil, &approval.Adapter{Svc: svc}, nil)
	policies, err := gate.EnabledTools(ctx, 7, "svc", names)
	require.NoError(t, err)
	require.Len(t, policies, 1000)
	require.True(t, policies["tool_0000"])
	require.False(t, policies["tool_0999"])
	require.Equal(t, 1, queries, "one policy query regardless of tool count")
	require.NoError(t, svc.SetEnabled(ctx, 7, "svc", "tool_0000", false))
	queries = 0
	enabled, err := gate.IsEnabled(ctx, 7, "svc", "tool_0000")
	require.NoError(t, err)
	require.False(t, enabled, "exact checks observe policy changes immediately")
	require.Equal(t, 1, queries)
}

func newMCPToolApprovalServiceForTest(t *testing.T) (*mcpToolApprovalService, *fakeMCPRepo) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPToolApproval{}))

	mcpRepo := newFakeMCPRepo()
	require.NoError(t, mcpRepo.Create(context.Background(), &types.MCPService{
		ID: "svc-1", TenantID: 1, Name: "weather",
	}))
	svc := NewMCPToolApprovalService(repository.NewMCPToolApprovalRepository(db), mcpRepo)
	return svc.(*mcpToolApprovalService), mcpRepo
}

func TestMCPToolApprovalServiceSetEnabledPreservesApproval(t *testing.T) {
	svc, _ := newMCPToolApprovalServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.SetRequireApproval(ctx, 1, "svc-1", "get_forecast", true))
	require.NoError(t, svc.SetEnabled(ctx, 1, "svc-1", "get_forecast", false))

	required, err := svc.IsRequired(ctx, 1, "svc-1", "get_forecast")
	require.NoError(t, err)
	require.True(t, required)
	enabled, err := svc.IsEnabled(ctx, 1, "svc-1", "get_forecast")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestMCPToolApprovalServiceSetRequireApprovalPreservesDisabled(t *testing.T) {
	svc, _ := newMCPToolApprovalServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.SetEnabled(ctx, 1, "svc-1", "get_forecast", false))
	require.NoError(t, svc.SetRequireApproval(ctx, 1, "svc-1", "get_forecast", true))

	enabled, err := svc.IsEnabled(ctx, 1, "svc-1", "get_forecast")
	require.NoError(t, err)
	require.False(t, enabled)
	required, err := svc.IsRequired(ctx, 1, "svc-1", "get_forecast")
	require.NoError(t, err)
	require.True(t, required)
}

func TestMCPToolApprovalServiceSetPolicyWritesBothFieldsOnce(t *testing.T) {
	svc, _ := newMCPToolApprovalServiceForTest(t)
	ctx := context.Background()

	require.NoError(t, svc.SetPolicy(ctx, 1, "svc-1", "get_forecast", boolPtr(true), boolPtr(false)))
	required, err := svc.IsRequired(ctx, 1, "svc-1", "get_forecast")
	require.NoError(t, err)
	require.True(t, required)
	enabled, err := svc.IsEnabled(ctx, 1, "svc-1", "get_forecast")
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestMCPToolApprovalServiceSetPolicyRejectsEmptyPatch(t *testing.T) {
	svc, _ := newMCPToolApprovalServiceForTest(t)
	err := svc.SetPolicy(context.Background(), 1, "svc-1", "get_forecast", nil, nil)
	require.Error(t, err)
}

func TestMCPToolApprovalServiceUnknownService(t *testing.T) {
	svc, _ := newMCPToolApprovalServiceForTest(t)
	err := svc.SetEnabled(context.Background(), 1, "missing", "tool", false)
	require.ErrorContains(t, err, "not found")
}
