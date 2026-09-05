package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

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
