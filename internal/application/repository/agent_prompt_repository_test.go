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

func TestAgentPromptRepositoryListStaleLeasesOnlyPendingAndLapsed(t *testing.T) {
	ctx := context.Background()
	repo := newAgentPromptTestRepo(t)
	now := time.Now()
	lease := func(d time.Duration) *time.Time { at := now.Add(d); return &at }
	for _, row := range []*types.AgentPrompt{
		{ID: "older", Status: types.AgentPromptPending, LeaseUntil: lease(-2 * time.Minute)},
		{ID: "old", Status: types.AgentPromptPending, LeaseUntil: lease(-time.Minute)},
		{ID: "alive", Status: types.AgentPromptPending, LeaseUntil: lease(time.Minute)},
		{ID: "closed", Status: types.AgentPromptSuspended, LeaseUntil: lease(-time.Minute)},
	} {
		row.TenantID, row.SessionID, row.Kind = 7, "s1", types.AgentPromptKindToolApproval
		require.NoError(t, repo.Create(ctx, row))
	}

	rows, err := repo.ListStaleLeases(ctx, []string{types.AgentPromptPending}, now, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "older", rows[0].ID)
	require.Equal(t, "old", rows[1].ID)

	rows, err = repo.ListStaleLeases(ctx, []string{types.AgentPromptPending}, now, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestAgentRunSuspensionRepositorySaveReplacesAndDeleteIsConditional(t *testing.T) {
	ctx := context.Background()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.AgentRunSuspension{}))
	repo := NewAgentRunSuspensionRepository(db)

	missing, err := repo.Get(ctx, "m1")
	require.NoError(t, err)
	require.Nil(t, missing)

	require.NoError(t, repo.Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m1", TenantID: 7, SessionID: "s1", PromptID: "p1",
		SnapshotVersion: 1, Snapshot: types.JSON(`{"round":1}`),
	}))
	require.NoError(t, repo.Save(ctx, &types.AgentRunSuspension{
		AssistantMessageID: "m1", TenantID: 7, SessionID: "s1", PromptID: "p2",
		SnapshotVersion: 1, Snapshot: types.JSON(`{"round":2}`),
	}))
	row, err := repo.Get(ctx, "m1")
	require.NoError(t, err)
	require.Equal(t, "p2", row.PromptID)
	require.JSONEq(t, `{"round":2}`, string(row.Snapshot))

	require.NoError(t, repo.Delete(ctx, "m1", "p1"))
	row, err = repo.Get(ctx, "m1")
	require.NoError(t, err)
	require.NotNil(t, row, "an older prompt must not drop a newer prompt's state")

	require.NoError(t, repo.Delete(ctx, "m1", "p2"))
	row, err = repo.Get(ctx, "m1")
	require.NoError(t, err)
	require.Nil(t, row)

	require.Error(t, repo.Save(ctx, &types.AgentRunSuspension{}))
}

func TestAgentPromptRepositoryExpiryAndSessionQueries(t *testing.T) {
	ctx := context.Background()
	repo := newAgentPromptTestRepo(t)
	now := time.Now()
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	for _, row := range []*types.AgentPrompt{
		{ID: "expired", SessionID: "s1", Status: types.AgentPromptSuspended, ExpiresAt: at(-time.Minute)},
		{ID: "open", SessionID: "s1", Status: types.AgentPromptSuspended, ExpiresAt: at(time.Hour)},
		{ID: "forever", SessionID: "s2", Status: types.AgentPromptSuspended},
		{ID: "done", SessionID: "s1", Status: types.AgentPromptDone, ExpiresAt: at(-time.Minute)},
	} {
		row.TenantID, row.Kind = 7, types.AgentPromptKindToolApproval
		require.NoError(t, repo.Create(ctx, row))
	}

	expired, err := repo.ListExpired(ctx, now, 10)
	require.NoError(t, err)
	require.Len(t, expired, 1)
	require.Equal(t, "expired", expired[0].ID)

	inSession, err := repo.ListBySession(ctx, "s1", []string{types.AgentPromptSuspended})
	require.NoError(t, err)
	ids := []string{}
	for _, r := range inSession {
		ids = append(ids, r.ID)
	}
	require.ElementsMatch(t, []string{"expired", "open"}, ids)

	answer := func(id string) bool {
		ok, err := repo.Transition(ctx, id, types.AgentPromptTransition{
			From: []string{types.AgentPromptSuspended}, To: types.AgentPromptAnswered, UnexpiredAt: &now,
		})
		require.NoError(t, err)
		return ok
	}
	require.False(t, answer("expired"), "an expired prompt cannot be answered")
	require.True(t, answer("open"))
	require.True(t, answer("forever"), "a prompt without expiry stays answerable")
}

func TestAgentPromptTransitionRechecksOwnerAndLease(t *testing.T) {
	repo := newAgentPromptTestRepo(t)
	ctx := t.Context()
	past, now, future := time.Now().Add(-time.Minute), time.Now(), time.Now().Add(time.Minute)
	owner, other := "owner", "other"
	require.NoError(t, repo.Create(ctx, &types.AgentPrompt{
		ID: "p", TenantID: 1, SessionID: "s", Kind: "ask_user",
		Status: types.AgentPromptPending, OwnerInstance: owner, LeaseUntil: &past,
	}))
	transition := types.AgentPromptTransition{
		From: []string{types.AgentPromptPending}, To: types.AgentPromptSuspended,
		ExpectedOwner: &other, LeaseBefore: &now,
	}
	ok, err := repo.Transition(ctx, "p", transition)
	require.NoError(t, err)
	require.False(t, ok)
	transition.ExpectedOwner = &owner
	_, err = repo.RenewLease(ctx, "p", owner, future)
	require.NoError(t, err)
	ok, err = repo.Transition(ctx, "p", transition)
	require.NoError(t, err)
	require.False(t, ok)
	_, err = repo.RenewLease(ctx, "p", owner, past)
	require.NoError(t, err)
	ok, err = repo.Transition(ctx, "p", transition)
	require.NoError(t, err)
	require.True(t, ok)
}
