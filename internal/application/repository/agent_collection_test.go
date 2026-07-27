package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newAgentCollectionRepositoryForTest(t *testing.T) (*agentCollectionRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.AgentCollectionProfile{},
		&types.AgentCollectionHistory{},
		&types.AgentCollectionExport{},
	))
	return &agentCollectionRepository{db: db}, db
}

func collectionChange(field string, value any, source types.AgentCollectionSource) types.AgentCollectionValueChange {
	return types.AgentCollectionValueChange{FieldKey: field, Value: value, Source: source}
}

func applyCollectionForTest(
	t *testing.T,
	repo *agentCollectionRepository,
	tenantID uint64,
	agentID, userID string,
	changes ...types.AgentCollectionValueChange,
) *types.AgentCollectionProfile {
	t.Helper()
	profile, err := repo.ApplyChanges(context.Background(), types.ApplyCollectionChangesInput{
		TenantID: tenantID, AgentID: agentID, UserID: userID, SchemaVersion: 1,
		RequiredTotal: len(changes), CompletedRequired: len(changes), IsComplete: true, Changes: changes,
	})
	require.NoError(t, err)
	return profile
}

func TestAgentCollectionRepositoryApplyChangesCreatesProfileAndHistory(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	profile := applyCollectionForTest(t, repo, 10, "agent-1", "user-1",
		collectionChange("status", "dismissed", types.CollectionSourceStructuredAnswer),
		collectionChange("years", 3.0, types.CollectionSourceStructuredAnswer),
	)

	require.NotEmpty(t, profile.ID)
	require.Equal(t, int64(1), profile.LockVersion)
	require.Equal(t, "dismissed", profile.Values["status"].(map[string]any)["value"])
	history, err := repo.ListHistory(context.Background(), profile.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(2), history.Total)
	require.Len(t, history.Items, 2)
}

func TestAgentCollectionRepositoryIdenticalValueDoesNotAppendHistory(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	profile := applyCollectionForTest(t, repo, 10, "agent-1", "user-1",
		collectionChange("status", "dismissed", types.CollectionSourceStructuredAnswer),
	)
	originalLock := profile.LockVersion

	profile = applyCollectionForTest(t, repo, 10, "agent-1", "user-1",
		collectionChange("status", "dismissed", types.CollectionSourceMessageExtraction),
	)
	require.Equal(t, originalLock, profile.LockVersion)
	history, err := repo.ListHistory(context.Background(), profile.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), history.Total)
}

func TestAgentCollectionRepositorySeparatesInactiveValues(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	profile := applyCollectionForTest(t, repo, 10, "agent-1", "user-1",
		collectionChange("status", "dismissed", types.CollectionSourceStructuredAnswer),
	)
	change := collectionChange("status", "dismissed", types.CollectionSourceSchemaMigration)
	change.Inactive = true
	profile = applyCollectionForTest(t, repo, 10, "agent-1", "user-1", change)
	require.NotContains(t, profile.Values, "status")
	require.Contains(t, profile.InactiveValues, "status")
}

func TestAgentCollectionRepositoryOlderMessageCannotOverwriteNewerValue(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	newer := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	newChange := collectionChange("status", "dismissed", types.CollectionSourceMessageExtraction)
	newChange.SourceMessageAt = &newer
	profile := applyCollectionForTest(t, repo, 10, "agent-1", "user-1", newChange)

	older := newer.Add(-time.Hour)
	oldChange := collectionChange("status", "employed", types.CollectionSourceMessageExtraction)
	oldChange.SourceMessageAt = &older
	profile = applyCollectionForTest(t, repo, 10, "agent-1", "user-1", oldChange)
	require.Equal(t, "dismissed", profile.Values["status"].(map[string]any)["value"])
}

func TestAgentCollectionRepositoryListsAcrossTenantsWithFilters(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	applyCollectionForTest(t, repo, 10, "agent-1", "user-1", collectionChange("status", "a", types.CollectionSourceStructuredAnswer))
	applyCollectionForTest(t, repo, 20, "agent-2", "user-2", collectionChange("status", "b", types.CollectionSourceStructuredAnswer))

	page, err := repo.ListProfiles(context.Background(), types.AgentCollectionProfileFilter{TenantID: 20, Page: 1, PageSize: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Equal(t, uint64(20), page.Items[0].TenantID)
}

func TestAgentCollectionRepositorySummarizesFilteredProfiles(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	applyCollectionForTest(t, repo, 10, "agent-1", "user-1", collectionChange("status", "a", types.CollectionSourceStructuredAnswer))
	profile := applyCollectionForTest(t, repo, 10, "agent-1", "user-2", collectionChange("status", "b", types.CollectionSourceStructuredAnswer))
	_, err := repo.ApplyChanges(context.Background(), types.ApplyCollectionChangesInput{
		TenantID: 10, AgentID: profile.AgentID, UserID: profile.UserID, SchemaVersion: 1,
		RequiredTotal: 2, CompletedRequired: 1, IsComplete: false,
	})
	require.NoError(t, err)
	applyCollectionForTest(t, repo, 20, "agent-2", "user-3", collectionChange("status", "c", types.CollectionSourceStructuredAnswer))

	summary, err := repo.SummarizeProfiles(context.Background(), types.AgentCollectionProfileFilter{TenantID: 10})
	require.NoError(t, err)
	require.Equal(t, int64(2), summary.Profiles)
	require.Equal(t, int64(2), summary.Users)
	require.Equal(t, int64(1), summary.Incomplete)
	exported, err := repo.ListProfilesForExport(context.Background(), types.AgentCollectionProfileFilter{TenantID: 10}, 100001)
	require.NoError(t, err)
	require.Len(t, exported, 2)
}

func TestAgentCollectionRepositoryExportLifecycleAndPurge(t *testing.T) {
	repo, _ := newAgentCollectionRepositoryForTest(t)
	profile := applyCollectionForTest(t, repo, 10, "agent-1", "user-1", collectionChange("status", "a", types.CollectionSourceStructuredAnswer))
	export := &types.AgentCollectionExport{
		ID: "export-1", ActorUserID: "admin-1", Format: "csv",
		FilterSnapshot: types.JSONMap{}, Status: types.AgentCollectionExportPending,
	}
	require.NoError(t, repo.CreateExport(context.Background(), export))
	export.Status = types.AgentCollectionExportCompleted
	export.Filename = "profiles.csv"
	require.NoError(t, repo.UpdateExport(context.Background(), export))
	loaded, err := repo.GetExport(context.Background(), export.ID)
	require.NoError(t, err)
	require.Equal(t, types.AgentCollectionExportCompleted, loaded.Status)

	require.NoError(t, repo.PurgeProfile(context.Background(), profile.ID))
	_, err = repo.GetProfileByID(context.Background(), profile.ID)
	require.ErrorIs(t, err, ErrAgentCollectionProfileNotFound)
}

func TestAgentCollectionRepositoryRollsBackProfileWhenHistoryWriteFails(t *testing.T) {
	repo, db := newAgentCollectionRepositoryForTest(t)
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("fail_collection_history", func(tx *gorm.DB) {
		if tx.Statement.Table == "agent_collection_history" {
			tx.AddError(errors.New("history write failed"))
		}
	}))

	_, err := repo.ApplyChanges(context.Background(), types.ApplyCollectionChangesInput{
		TenantID: 10, AgentID: "agent-1", UserID: "user-1", SchemaVersion: 1,
		Changes: []types.AgentCollectionValueChange{collectionChange("status", "dismissed", types.CollectionSourceStructuredAnswer)},
	})
	require.ErrorContains(t, err, "history write failed")
	var count int64
	require.NoError(t, db.Model(&types.AgentCollectionProfile{}).Count(&count).Error)
	require.Zero(t, count)
}
