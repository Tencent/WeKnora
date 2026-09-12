package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestEmbeddingCachePersistenceSurvivesBackendRecreationAndIsolatesTenants(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "embedding-cache.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&embeddingCacheRecord{}))
	first := NewEmbeddingCachePersistence(db)
	tenantOne := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(101))
	tenantTwo := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(202))
	expiresAt := time.Now().UTC().Add(time.Hour)

	require.NoError(t, first.Put(tenantOne, "embed-1", map[string][]float32{
		"hash-key": {1.25, -2.5, 3.75},
	}, expiresAt))

	restarted := NewEmbeddingCachePersistence(db)
	vectors, err := restarted.Get(tenantOne, []string{"hash-key"}, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, []float32{1.25, -2.5, 3.75}, vectors["hash-key"].Vector)
	require.WithinDuration(t, expiresAt, vectors["hash-key"].ExpiresAt, time.Millisecond)
	vectors, err = restarted.Get(tenantTwo, []string{"hash-key"}, time.Now().UTC())
	require.NoError(t, err)
	require.Empty(t, vectors)
	vectors, err = restarted.Get(tenantOne, []string{"hash-key"}, expiresAt.Add(time.Second))
	require.NoError(t, err)
	require.Empty(t, vectors)
}

func TestEmbeddingVectorEncodingRoundTrip(t *testing.T) {
	want := []float32{0, 1.5, -3.25}
	got, err := decodeEmbeddingVector(encodeEmbeddingVector(want))
	require.NoError(t, err)
	require.Equal(t, want, got)
	_, err = decodeEmbeddingVector([]byte{1, 2, 3})
	require.Error(t, err)
}
