package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newSessionPolicyTestService(
	t *testing.T,
) (interfaces.MCPToolSessionPolicyService, interfaces.MCPToolSessionPolicyRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.MCPToolSessionPolicy{}, &types.MCPToolApproval{}))
	mcp := newFakeMCPRepo()
	require.NoError(t, mcp.Create(context.Background(), &types.MCPService{ID: "svc", TenantID: 7}))
	repo := repository.NewMCPToolSessionPolicyRepository(db)
	return NewMCPToolSessionPolicyService(repo, mcp), repo, db
}

func TestMCPToolSessionPolicyPersistenceAndRevocation(t *testing.T) {
	svc, repo, _ := newSessionPolicyTestService(t)
	ctx := context.Background()
	rules := []types.MCPToolSessionRule{{Platform: "feishu", ChatType: "group", ChatID: "ops"}}
	require.NoError(t, svc.SetPolicy(ctx, 7, "svc", "tool", rules))
	gate := approval.NewGate(nil, nil, nil).WithSessionPolicies(repo)
	imCtx := types.WithIMConversation(ctx, types.IMConversation{Platform: "feishu", ChatType: "group", ChatID: "ops"})
	enabled, err := gate.IsEnabled(imCtx, 7, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)
	// Replace the row, rather than adding an OR branch; an already-discovered
	// tool must be refused at its next execution after the policy changes.
	require.NoError(t, svc.SetPolicy(ctx, 7, "svc", "tool", []types.MCPToolSessionRule{}))
	enabled, err = gate.IsEnabled(imCtx, 7, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled)
	rows, err := svc.ListByService(ctx, 7, "svc")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Empty(t, rows[0].AllowedSessions)
	require.NoError(t, svc.DeletePolicy(ctx, 7, "svc", "tool"))
	enabled, err = gate.IsEnabled(imCtx, 7, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)
}

func TestMCPToolSessionPolicyServiceRejectsCrossTenantAndInvalidRules(t *testing.T) {
	svc, repo, _ := newSessionPolicyTestService(t)
	ctx := context.Background()
	rules := []types.MCPToolSessionRule{{Platform: "feishu", ChatType: "group", ChatID: "ops"}}
	require.Error(t, svc.SetPolicy(ctx, 8, "svc", "tool", rules))
	require.Error(t, svc.DeletePolicy(ctx, 8, "svc", "tool"))
	_, err := svc.ListByService(ctx, 8, "svc")
	require.Error(t, err)
	require.ErrorIs(t, svc.SetPolicy(ctx, 7, "svc", "tool", nil), types.ErrInvalidMCPToolSessionPolicy)
	rules[0].Platform = ""
	require.ErrorIs(t, svc.SetPolicy(ctx, 7, "svc", "tool", rules), types.ErrInvalidMCPToolSessionPolicy)
	rows, err := repo.ListByService(ctx, 7, "svc")
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestMCPToolSessionPolicyQueriesDoNotGrowWithToolCount(t *testing.T) {
	svc, repo, db := newSessionPolicyTestService(t)
	ctx := context.Background()
	require.NoError(t, svc.SetPolicy(ctx, 7, "svc", "tool_999", []types.MCPToolSessionRule{}))
	mcp := newFakeMCPRepo()
	require.NoError(t, mcp.Create(ctx, &types.MCPService{ID: "svc", TenantID: 7}))
	base := NewMCPToolApprovalService(repository.NewMCPToolApprovalRepository(db), mcp)
	gate := approval.NewGate(nil, &approval.Adapter{Svc: base}, nil).WithSessionPolicies(repo)
	queries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").
		Register("count_session_policy_reads", func(*gorm.DB) { queries++ }))
	names := make([]string, 1000)
	for i := range names {
		names[i] = fmt.Sprintf("tool_%d", i)
	}
	imCtx := types.WithIMConversation(ctx, types.IMConversation{Platform: "feishu", ChatType: "group", ChatID: "ops"})
	result, err := gate.EnabledTools(imCtx, 7, "svc", names)
	require.NoError(t, err)
	require.Len(t, result, 1000)
	require.False(t, result["tool_999"])
	require.True(t, result["tool_0"])
	require.Equal(t, 2, queries, "one base-policy and one session-policy query")
	queries = 0
	result, err = gate.EnabledTools(ctx, 7, "svc", names)
	require.NoError(t, err)
	require.True(t, result["tool_999"])
	require.Equal(t, 1, queries, "non-IM requests must not read the session policy store")
}

func TestMCPToolSessionPolicyRepositoryTenantIsolation(t *testing.T) {
	_, repo, _ := newSessionPolicyTestService(t)
	ctx := context.Background()
	for _, tenant := range []uint64{7, 8} {
		require.NoError(t, repo.Upsert(ctx, &types.MCPToolSessionPolicy{
			TenantID: tenant, ServiceID: "svc", ToolName: "tool", AllowedSessions: []types.MCPToolSessionRule{},
		}))
	}
	require.NoError(t, repo.Delete(ctx, 7, "svc", "tool"))
	rows, err := repo.ListByService(ctx, 7, "svc")
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = repo.ListByService(ctx, 8, "svc")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, uint64(8), rows[0].TenantID)
}

func TestMCPToolSessionPolicyBuiltinService(t *testing.T) {
	_, repo, _ := newSessionPolicyTestService(t)
	mcp := newFakeMCPRepo()
	ctx := context.Background()
	require.NoError(t, mcp.Create(ctx, &types.MCPService{ID: "builtin", TenantID: 1, IsBuiltin: true}))
	svc := NewMCPToolSessionPolicyService(repo, mcp)
	require.NoError(t, svc.SetPolicy(ctx, 7, "builtin", "tool", []types.MCPToolSessionRule{}))
	rows, err := svc.ListByService(ctx, 7, "builtin")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, uint64(7), rows[0].TenantID)
}
