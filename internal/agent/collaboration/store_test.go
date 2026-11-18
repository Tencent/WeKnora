package collaboration

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func budgetStore(t *testing.T) (*Store, Scope) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&Record{}))
	s := NewStore(db)
	scope := Scope{TenantID: 1, UserID: "u", SessionID: "s", ID: "g"}
	require.NoError(t, s.Ensure(context.Background(), scope))
	require.NoError(t, s.SetCollaborationRunBudget(context.Background(), scope, "turn", &types.RunBudgetUsage{
		Limit: types.RunBudget{MaxTotalTokens: 100, MaxWriteActions: 2},
	}))
	return s, scope
}

func TestSharedRunBudgetReservationIsAtomicAndBounded(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 60}))
	require.ErrorIs(t,
		s.ReserveBudget(ctx, scope, "turn", "child-b/round-1", types.RunBudgetUsed{TotalTokens: 50}),
		ErrRunBudgetExhausted,
	)
	require.NoError(t, s.SettleBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 60}))
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 60, usage.Used.TotalTokens)
	require.Equal(t, 0, usage.Limit.MaxWallTimeSec)
	require.LessOrEqual(t, usage.Used.TotalTokens, usage.Limit.MaxTotalTokens)
}

func TestSharedRunBudgetMultipleChildrenAccumulate(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-a/round-1",
		types.RunBudgetUsed{TotalTokens: 30, WriteActions: 1}))
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-b/round-1",
		types.RunBudgetUsed{TotalTokens: 30, WriteActions: 1}))
	require.NoError(t, s.SettleBudget(ctx, scope, "turn", "child-a/round-1",
		types.RunBudgetUsed{TotalTokens: 45, WriteActions: 1}))
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 75, usage.Used.TotalTokens)
	require.Equal(t, 2, usage.Used.WriteActions)
}

func TestSharedRunBudgetSettleReleasesWorstCaseReservation(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 80}))
	require.NoError(t, s.SettleBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 35}))
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 35, usage.Used.TotalTokens,
		"settlement must release unused reservation instead of charging max(reserved, actual)")
}

func TestSharedRunBudgetSettlementRetryIsIdempotent(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 30}))
	require.NoError(t, s.SettleBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 80}))
	require.NoError(t, s.SettleBudget(ctx, scope, "turn", "child-a/round-1", types.RunBudgetUsed{TotalTokens: 80}))
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 80, usage.Used.TotalTokens)
}

func TestSharedRunBudgetTurnIsolation(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.SettleBudget(ctx, scope, "turn", "child-a", types.RunBudgetUsed{TotalTokens: 90}))
	usage, err := s.SharedRunBudget(ctx, scope, "another-turn")
	require.NoError(t, err)
	require.Nil(t, usage)
}

func TestSharedRunBudgetSameTurnInitIsIdempotent(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-a",
		types.RunBudgetUsed{TotalTokens: 40, WriteActions: 1}))
	require.NoError(t, s.SetCollaborationRunBudget(ctx, scope, "turn", &types.RunBudgetUsage{
		Limit: types.RunBudget{MaxTotalTokens: 100, MaxWriteActions: 2},
	}))
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 40, usage.Used.TotalTokens)
	require.Equal(t, 1, usage.Used.WriteActions)
}

func TestSharedRunBudgetRejectsWriteBeyondExactLimit(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "child-a/batch", types.RunBudgetUsed{WriteActions: 2}))
	require.ErrorIs(t, s.ReserveBudget(ctx, scope, "turn", "child-b/batch",
		types.RunBudgetUsed{WriteActions: 1}), ErrRunBudgetExhausted)
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 2, usage.Used.WriteActions)
}

func TestSharedRunBudgetExtensionIsIdempotentAndPreservesUsage(t *testing.T) {
	s, scope := budgetStore(t)
	ctx := context.Background()
	require.NoError(t, s.ReserveBudget(ctx, scope, "turn", "parent/model", types.RunBudgetUsed{TotalTokens: 60}))
	require.NoError(t, s.ExtendBudget(ctx, scope, "turn", "prompt-1", types.RunBudget{MaxTotalTokens: 50}))
	require.NoError(t, s.ExtendBudget(ctx, scope, "turn", "prompt-1", types.RunBudget{MaxTotalTokens: 50}))
	usage, err := s.SharedRunBudget(ctx, scope, "turn")
	require.NoError(t, err)
	require.Equal(t, 150, usage.Limit.MaxTotalTokens)
	require.Equal(t, 60, usage.Used.TotalTokens)
}
