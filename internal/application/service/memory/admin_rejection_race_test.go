package memory

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type rejectAfterForgottenCheckRepo struct {
	interfaces.MemoryRepository
	afterCheck func()
}

func (r *rejectAfterForgottenCheckRepo) HasTombstoneForMessage(
	ctx context.Context, scope interfaces.MemoryScope, messageID string, window time.Duration,
) (bool, error) {
	forgotten, err := r.MemoryRepository.HasTombstoneForMessage(ctx, scope, messageID, window)
	if err == nil && r.afterCheck != nil {
		afterCheck := r.afterCheck
		r.afterCheck = nil
		afterCheck()
	}
	return forgotten, err
}

// TestMemoryAdminRejectFencesConcurrentRederivation 重现拒绝发生在提取的墓碑检查与保存之间。
func TestMemoryAdminRejectFencesConcurrentRederivation(t *testing.T) {
	svc, db, tenants := newMemoryHarness(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	ctx := enabledCtx(t, tenants, 1, "alice")
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	proposal := seedAdminItem(t, svc, scope, types.MemoryStatusPending, "an unwanted inference")
	subject, err := svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	rejected := false
	svc.repo = &rejectAfterForgottenCheckRepo{MemoryRepository: svc.repo, afterCheck: func() {
		admin := memoryAdminContext(ctx, 1, types.TenantRoleAdmin)
		require.NoError(t, svc.AdminRejectItem(admin, subject.ID, proposal.ID))
		rejected = true
	}}
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "a reworded guess", Content: "a different unwanted deduction",
		SourceMessageID: proposal.SourceMessageID, Origin: types.MemoryOriginExtracted, Inferred: true,
	})
	require.True(t, rejected, "the real admin rejection must commit before the stale save")
	require.ErrorIs(t, err, ErrPreviouslyForgotten)
	_, total, err := svc.ListItems(ctx, types.MemoryStatusPending, 50, 0)
	require.NoError(t, err)
	require.Zero(t, total, "the rejected source must not immediately create a rephrased proposal")
	_, err = svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Topic: "explicit request", Content: "a deliberately requested new fact",
		SourceMessageID: proposal.SourceMessageID, Origin: types.MemoryOriginExplicit,
	})
	require.NoError(t, err, "source-message suppression must preserve the explicit-request exemption")
}
