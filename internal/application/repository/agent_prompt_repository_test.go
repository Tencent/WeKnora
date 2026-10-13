package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAgentPromptTestRepo(t *testing.T) *AgentPromptRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.AgentPrompt{}))
	return NewAgentPromptRepository(db).(*AgentPromptRepository)
}

func TestAgentPromptRepositoryTransitionIsCompareAndSet(t *testing.T) {
	ctx := context.Background()
	repo := newAgentPromptTestRepo(t)
	lease := time.Now().Add(time.Minute)
	require.NoError(t, repo.Create(ctx, &types.AgentPrompt{
		ID: "p1", TenantID: 7, UserID: "alice", SessionID: "s1",
		Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptPending,
		Request: types.JSON(`{"args":{"a":1}}`), OwnerInstance: "owner", LeaseUntil: &lease,
	}))

	resolvedBy := "alice"
	ok, err := repo.Transition(ctx, "p1", types.AgentPromptTransition{
		From: []string{types.AgentPromptPending}, To: types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":true}`), ResolvedBy: &resolvedBy,
	})
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = repo.Transition(ctx, "p1", types.AgentPromptTransition{
		From: []string{types.AgentPromptPending}, To: types.AgentPromptAnswered,
		Response: types.JSON(`{"approved":false}`),
	})
	require.NoError(t, err)
	require.False(t, ok, "a second answer must lose")

	row, err := repo.Get(ctx, "p1")
	require.NoError(t, err)
	require.Equal(t, types.AgentPromptAnswered, row.Status)
	require.JSONEq(t, `{"approved":true}`, string(row.Response))
	require.JSONEq(t, `{"args":{"a":1}}`, string(row.Request))
	require.Equal(t, "alice", row.ResolvedBy)

	missing, err := repo.Get(ctx, "nope")
	require.NoError(t, err)
	require.Nil(t, missing)
}

func TestAgentPromptRepositoryRenewLeaseRequiresPendingOwner(t *testing.T) {
	ctx := context.Background()
	repo := newAgentPromptTestRepo(t)
	require.NoError(t, repo.Create(ctx, &types.AgentPrompt{
		ID: "p1", TenantID: 7, SessionID: "s1",
		Kind: types.AgentPromptKindToolApproval, Status: types.AgentPromptPending,
		OwnerInstance: "owner",
	}))

	until := time.Now().Add(time.Minute)
	ok, err := repo.RenewLease(ctx, "p1", "intruder", until)
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = repo.RenewLease(ctx, "p1", "owner", until)
	require.NoError(t, err)
	require.True(t, ok)

	_, err = repo.Transition(ctx, "p1", types.AgentPromptTransition{
		From: []string{types.AgentPromptPending}, To: types.AgentPromptExpired,
	})
	require.NoError(t, err)
	ok, err = repo.RenewLease(ctx, "p1", "owner", until)
	require.NoError(t, err)
	require.False(t, ok, "a closed row has no lease to renew")
}
