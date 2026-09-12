package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMCPToolApprovalTestRepo(t *testing.T) interfaces.MCPToolApprovalRepository {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.MCPToolApproval{}))
	return NewMCPToolApprovalRepository(db)
}

func boolPtr(v bool) *bool { return &v }

func TestMCPToolApprovalIsEnabledDefaultsTrue(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()

	enabled, err := repo.IsEnabled(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, enabled)

	required, err := repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, required)
}

func TestMCPToolApprovalUpsertPolicyInsertDefaults(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "only-require", types.MCPToolPolicyPatch{
		RequireApproval: boolPtr(true),
	}))
	enabled, err := repo.IsEnabled(ctx, 1, "svc", "only-require")
	require.NoError(t, err)
	require.True(t, enabled, "new rows must stay enabled when only approval is set")
	required, err := repo.IsRequired(ctx, 1, "svc", "only-require")
	require.NoError(t, err)
	require.True(t, required)

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "only-enabled", types.MCPToolPolicyPatch{
		Enabled: boolPtr(false),
	}))
	enabled, err = repo.IsEnabled(ctx, 1, "svc", "only-enabled")
	require.NoError(t, err)
	require.False(t, enabled)
	required, err = repo.IsRequired(ctx, 1, "svc", "only-enabled")
	require.NoError(t, err)
	require.False(t, required, "new rows must keep require_approval=false when only enabled is set")
}

func TestMCPToolApprovalUpsertPolicyPreservesOtherColumn(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		Enabled: boolPtr(false),
	}))
	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		RequireApproval: boolPtr(true),
	}))

	enabled, err := repo.IsEnabled(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled, "approval patch must not re-enable a disabled tool")
	required, err := repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, required)

	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		Enabled: boolPtr(true),
	}))
	required, err = repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, required, "enabled patch must not clear require_approval")
}

func TestMCPToolApprovalConcurrentColumnPatches(t *testing.T) {
	repo := newMCPToolApprovalTestRepo(t)
	ctx := context.Background()
	require.NoError(t, repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{
		RequireApproval: boolPtr(false),
		Enabled:         boolPtr(true),
	}))

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		errs <- repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{Enabled: boolPtr(false)})
	}()
	go func() {
		defer wg.Done()
		errs <- repo.UpsertPolicy(ctx, 1, "svc", "tool", types.MCPToolPolicyPatch{RequireApproval: boolPtr(true)})
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	enabled, err := repo.IsEnabled(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.False(t, enabled)
	required, err := repo.IsRequired(ctx, 1, "svc", "tool")
	require.NoError(t, err)
	require.True(t, required)
}
