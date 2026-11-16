package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSessionKindListingsAndDeleteAll(t *testing.T) {
	repo, db := newSessionRepositoryForTest(t)
	require.NoError(t, db.AutoMigrate(&testIMChannelSession{}))
	ctx := context.Background()
	normal := createSessionForTest(t, db, 1, "alice")
	rows := []types.Session{
		{TenantID: 1, UserID: "alice", Kind: types.SessionKindAssistant, IsPinned: true},
		{TenantID: 1, UserID: "alice", Kind: types.SessionKindAssistant},
		{TenantID: 1, UserID: "bob", Kind: types.SessionKindAssistant},
		{TenantID: 1, Kind: types.SessionKindAssistant},
		{TenantID: 2, UserID: "alice", Kind: types.SessionKindAssistant},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Model(&rows[1]).Update("updated_at", time.Now().Add(time.Hour)).Error)
	for _, source := range []string{"", "web", "api", "embed", "feishu"} {
		items, _, err := repo.QueryPaged(ctx, &types.SessionListQuery{TenantID: 1, Source: source})
		require.NoError(t, err)
		for _, item := range items {
			require.Empty(t, item.Kind)
		}
	}
	for page, want := range []string{rows[1].ID, rows[0].ID} {
		items, total, err := repo.QueryPaged(ctx, &types.SessionListQuery{
			TenantID: 1, UserID: "alice", Kind: types.SessionKindAssistant, Page: page + 1, PageSize: 1,
		})
		require.NoError(t, err)
		require.EqualValues(t, 2, total)
		require.Len(t, items, 1)
		require.Equal(t, want, items[0].ID)
	}
	items, total, err := repo.QueryPaged(ctx, &types.SessionListQuery{TenantID: 1, Kind: types.SessionKindAssistant})
	require.NoError(t, err)
	require.Empty(t, items)
	require.Zero(t, total)
	listed, err := repo.GetByTenantID(ctx, 1, "alice")
	require.NoError(t, err)
	require.Equal(t, []string{normal.ID}, sessionIDsForTest(listed))
	paged, total, err := repo.GetPagedByTenantID(ctx, 1, "alice", &types.Pagination{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, []string{normal.ID}, sessionIDsForTest(paged))
	deleted, err := repo.DeleteAllByTenantID(ctx, 1, "alice")
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	for _, row := range rows {
		require.EqualValues(t, 1, countActiveSessionsForTest(t, db, row.ID))
	}
	deleted, err = repo.Delete(ctx, 1, "bob", rows[0].ID)
	require.NoError(t, err)
	require.Zero(t, deleted)
	deleted, err = repo.Delete(ctx, 1, "alice", rows[0].ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
}

func TestAssistantExcludedFromMessageSearchAndArtifactLibrary(t *testing.T) {
	db := newArtifactTestDB(t)
	repo := NewMessageRepository(db)
	ctx := context.Background()
	ids := make([]string, 0, 2)
	for _, kind := range []string{"", types.SessionKindAssistant} {
		id := createSession(t, db, &types.Session{TenantID: 1, UserID: "alice", Kind: kind})
		ids = append(ids, id)
		_, err := repo.CreateMessage(ctx, &types.Message{
			SessionID: id, Role: "assistant", Content: "matching content",
			Artifacts: types.MessageArtifacts{testArtifact("report.pptx", "/w/report.pptx", time.Now())},
		})
		require.NoError(t, err)
	}
	for _, filter := range [][]string{nil, ids, {ids[1]}} {
		matches, err := repo.SearchMessagesByKeyword(ctx, 1, "alice", "matching", filter, 20)
		require.NoError(t, err)
		if len(filter) == 1 {
			require.Empty(t, matches)
		} else {
			require.Len(t, matches, 1)
			require.Equal(t, ids[0], matches[0].SessionID)
		}
	}
	owned, err := repo.OwnedSessionIDs(ctx, 1, "alice", ids)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{ids[0]: true}, owned)
	artifacts, total, err := repo.ListArtifactLibrary(ctx, &types.ArtifactLibraryQuery{TenantID: 1, UserID: "alice"})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, artifacts, 1)
	require.Equal(t, ids[0], artifacts[0].SessionID)
}
