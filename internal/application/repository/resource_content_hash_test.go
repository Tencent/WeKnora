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

func newResourceContentHashTestRepo(t *testing.T) *resourceRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	// A single connection keeps the in-memory database from vanishing behind a
	// second pooled connection.
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&types.StoredResource{}))
	return NewResourceRepository(db).(*resourceRepository)
}

func storedResource(id, handle string, tenantID uint64, contentHash string, createdAt time.Time) *types.StoredResource {
	return &types.StoredResource{
		ID:           id,
		Handle:       handle,
		TenantID:     tenantID,
		Provider:     "local",
		PhysicalPath: "local://" + id + "/exports/" + id + ".png",
		LocationHash: id,
		Kind:         "image",
		ContentHash:  contentHash,
		CreatedAt:    createdAt,
	}
}

func TestGetActiveByContentHashFindsLiveRow(t *testing.T) {
	ctx := context.Background()
	repo := newResourceContentHashTestRepo(t)
	require.NoError(t, repo.Create(ctx, storedResource("r1", "HandleAAAAAAAAAAAAAA01", 7, "hash-a", time.Now())))

	found, err := repo.GetActiveByContentHash(ctx, 7, "hash-a")
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "r1", found.ID)
	require.Equal(t, "hash-a", found.ContentHash)
}

func TestGetActiveByContentHashMisses(t *testing.T) {
	ctx := context.Background()
	repo := newResourceContentHashTestRepo(t)
	require.NoError(t, repo.Create(ctx, storedResource("r1", "HandleAAAAAAAAAAAAAA01", 7, "hash-a", time.Now())))
	require.NoError(t, repo.Create(ctx, storedResource("r2", "HandleAAAAAAAAAAAAAA02", 7, "", time.Now())))

	for label, query := range map[string]struct {
		tenantID uint64
		hash     string
	}{
		"unknown hash": {7, "hash-missing"},
		"other tenant": {8, "hash-a"},
		"zero tenant":  {0, "hash-a"},
		"blank hash":   {7, ""},
	} {
		found, err := repo.GetActiveByContentHash(ctx, query.tenantID, query.hash)
		require.NoError(t, err, label)
		require.Nilf(t, found, "case %s", label)
	}
}

func TestGetActiveByContentHashIgnoresDeletedRows(t *testing.T) {
	ctx := context.Background()
	repo := newResourceContentHashTestRepo(t)
	require.NoError(t, repo.Create(ctx, storedResource("r1", "HandleAAAAAAAAAAAAAA01", 7, "hash-a", time.Now())))
	require.NoError(t, repo.MarkDeleted(ctx, "r1"))

	found, err := repo.GetActiveByContentHash(ctx, 7, "hash-a")
	require.NoError(t, err)
	require.Nil(t, found, "a soft-deleted resource must not be reused")
}

// Rows registered before this lookup existed can share a hash. The choice has to
// be deterministic or two identical requests could be handed different handles.
func TestGetActiveByContentHashPrefersOldestDuplicate(t *testing.T) {
	ctx := context.Background()
	repo := newResourceContentHashTestRepo(t)
	now := time.Now()
	require.NoError(t, repo.Create(ctx, storedResource("r-new", "HandleAAAAAAAAAAAAAA02", 7, "hash-a", now)))
	require.NoError(t, repo.Create(ctx, storedResource(
		"r-old", "HandleAAAAAAAAAAAAAA01", 7, "hash-a", now.Add(-time.Hour),
	)))

	found, err := repo.GetActiveByContentHash(ctx, 7, "hash-a")
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, "r-old", found.ID)
}
