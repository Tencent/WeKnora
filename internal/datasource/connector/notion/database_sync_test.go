package notion

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func syncDatabases(
	t *testing.T, f *databaseFixture, cursor *types.SyncCursor,
) ([]types.FetchedItem, *types.SyncCursor) {
	t.Helper()
	items, next, err := NewConnector().FetchIncremental(context.Background(), f.config("db-a", "db-b"), cursor)
	require.NoError(t, err)
	require.NotNil(t, next)
	return items, next
}

func databaseItems(items []types.FetchedItem) map[string]string {
	result := make(map[string]string)
	for _, item := range items {
		if item.Metadata["object_type"] == objectTypeDatabase && !item.IsDeleted {
			result[item.ExternalID] = string(item.Content)
		}
	}
	return result
}

func TestDatabaseSyncRemovedRows(t *testing.T) {
	f := newDatabaseFixture(t)
	items, cursor := syncDatabases(t, f, databaseLegacyCursor())
	require.Len(t, databaseItems(items), 2, "legacy cursors must establish per-database membership")
	f.rows["db-a"] = []string{"row-a"}
	items, cursor = syncDatabases(t, f, cursor)
	updated := databaseItems(items)
	require.Len(t, updated, 1, "only the affected database should be rebuilt")
	require.Contains(t, updated["db-a"], "row-a")
	require.NotContains(t, updated["db-a"], "row-b")
	f.rows["db-a"] = nil
	items, cursor = syncDatabases(t, f, cursor)
	require.Contains(t, databaseItems(items)["db-a"], "| Title |")
	require.NotContains(t, databaseItems(items)["db-a"], "row-a")
	f.rows["db-a"] = []string{"row-new"}
	items, cursor = syncDatabases(t, f, cursor)
	require.Contains(t, databaseItems(items)["db-a"], "row-new")
	f.blockReads = 0
	items, _ = syncDatabases(t, f, cursor)
	require.Empty(t, items, "unchanged databases must not emit updates or deletions")
	require.Zero(t, f.blockReads, "unchanged databases must not fetch record bodies")
}

func TestDatabaseSyncQueryIsAuthoritative(t *testing.T) {
	f := newDatabaseFixture(t)
	_, cursor := syncDatabases(t, f, databaseLegacyCursor())
	f.searchRows = false
	items, cursor := syncDatabases(t, f, cursor)
	require.Empty(t, items, "rows absent from Search but returned by query must not be deleted")
	f.rows["db-a"] = []string{"row-a"}
	items, _ = syncDatabases(t, f, cursor)
	require.NotContains(t, databaseItems(items)["db-a"], "row-b")
	require.Contains(t, databaseItems(items)["db-a"], "row-a")
}

func TestDatabaseSyncFailurePreservesCursor(t *testing.T) {
	for _, failure := range []string{"query_page", "record_body"} {
		t.Run(failure, func(t *testing.T) {
			f := newDatabaseFixture(t)
			_, cursor := syncDatabases(t, f, databaseLegacyCursor())
			f.rows["db-a"] = []string{"row-a"}
			f.failQuery = failure == "query_page"
			f.failBlocks = failure == "record_body"
			items, next, err := NewConnector().FetchIncremental(context.Background(), f.config("db-a", "db-b"), cursor)
			require.Error(t, err)
			require.Nil(t, next)
			require.Empty(t, items)
			f.failQuery, f.failBlocks = false, false
			items, _ = syncDatabases(t, f, cursor)
			require.Contains(t, databaseItems(items)["db-a"], "row-a")
			require.NotContains(t, databaseItems(items)["db-a"], "row-b")
		})
	}
}

func TestDatabaseSyncChangedRecordAndDeselection(t *testing.T) {
	f := newDatabaseFixture(t)
	_, cursor := syncDatabases(t, f, databaseLegacyCursor())
	f.times["row-a"] = "2026-01-15T11:00:00Z"
	items, cursor := syncDatabases(t, f, cursor)
	require.Len(t, databaseItems(items), 1, "record edits must rebuild an unchanged parent database")
	items, next, err := NewConnector().FetchIncremental(context.Background(), f.config("db-b"), cursor)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Empty(t, items, "deselecting a database must not delete it or its records")
}

func TestDatabaseSyncDoesNotDuplicateRows(t *testing.T) {
	f := newDatabaseFixture(t)
	for range 10 {
		items, _ := syncDatabases(t, f, databaseLegacyCursor())
		for _, item := range items {
			require.False(t, strings.HasPrefix(item.ExternalID, "row-"), "database records belong in the aggregate")
		}
	}
}

func TestDatabaseSyncDeselectedRowsAbsentFromSearch(t *testing.T) {
	f := newDatabaseFixture(t)
	_, cursor := syncDatabases(t, f, databaseLegacyCursor())
	f.searchRows = false
	items, _, err := NewConnector().FetchIncremental(context.Background(), f.config("db-b"), cursor)
	require.NoError(t, err)
	require.Empty(t, items, "deselected database membership must not produce record deletions")
}

func TestDatabaseSyncMembershipNotJustCount(t *testing.T) {
	f := newDatabaseFixture(t)
	_, cursor := syncDatabases(t, f, databaseLegacyCursor())
	f.rows["db-a"] = []string{"row-b", "row-a"}
	f.blockReads = 0
	items, cursor := syncDatabases(t, f, cursor)
	require.Empty(t, items, "query result ordering must not trigger rebuilding")
	require.Zero(t, f.blockReads)
	f.rows["db-a"] = []string{"row-a", "row-new"}
	items, _ = syncDatabases(t, f, cursor)
	require.Contains(t, databaseItems(items)["db-a"], "row-new")
	require.NotContains(t, databaseItems(items)["db-a"], "row-b")
}

func TestDatabaseSyncExplicitlySelectedRow(t *testing.T) {
	f := newDatabaseFixture(t)
	_, cursor := syncDatabases(t, f, databaseLegacyCursor())
	f.times["row-a"] = "2026-01-15T11:00:00Z"
	items, _, err := NewConnector().FetchIncremental(context.Background(), f.config("row-a"), cursor)
	require.NoError(t, err)
	require.Len(t, items, 1, "a selected row must not be excluded with its deselected database")
	require.Equal(t, "row-a", items[0].ExternalID)
	require.False(t, items[0].IsDeleted)
}
