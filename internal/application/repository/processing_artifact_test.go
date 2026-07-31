package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProcessingArtifactTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.ProcessingArtifact{}))
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func TestProcessingArtifactGetIgnoresExpiredArtifacts(t *testing.T) {
	db := setupProcessingArtifactTestDB(t)
	repo := NewProcessingArtifactRepository(db)
	ctx := context.Background()
	expiredAt := time.Now().Add(-time.Minute)
	expired := &types.ProcessingArtifact{
		ID:              uuid.NewString(),
		TenantID:        7,
		Stage:           "parse",
		KeyVersion:      1,
		ArtifactKey:     "expired",
		ProcessorDigest: "processor",
		OutputDigest:    "output",
		OutputSchema:    "parse.v1",
		Codec:           "json",
		Payload:         []byte(`{"old":true}`),
		PayloadChecksum: "checksum",
		PayloadSize:     int64(len([]byte(`{"old":true}`))),
		CreatedAt:       time.Now().Add(-time.Hour),
		ExpiresAt:       &expiredAt,
	}
	inserted, err := repo.PutIfAbsent(ctx, expired)
	require.NoError(t, err)
	require.True(t, inserted)

	got, err := repo.Get(ctx, 7, "parse", 1, "expired")
	require.ErrorIs(t, err, ErrProcessingArtifactNotFound)
	require.Nil(t, got)
}

func TestProcessingArtifactGetReturnsUnexpiredArtifacts(t *testing.T) {
	db := setupProcessingArtifactTestDB(t)
	repo := NewProcessingArtifactRepository(db)
	ctx := context.Background()
	expiresAt := time.Now().Add(time.Hour)
	fresh := &types.ProcessingArtifact{
		ID:              uuid.NewString(),
		TenantID:        7,
		Stage:           "parse",
		KeyVersion:      1,
		ArtifactKey:     "fresh",
		ProcessorDigest: "processor",
		OutputDigest:    "output",
		OutputSchema:    "parse.v1",
		Codec:           "json",
		Payload:         []byte(`{"fresh":true}`),
		PayloadChecksum: "checksum",
		PayloadSize:     int64(len([]byte(`{"fresh":true}`))),
		CreatedAt:       time.Now(),
		ExpiresAt:       &expiresAt,
	}
	inserted, err := repo.PutIfAbsent(ctx, fresh)
	require.NoError(t, err)
	require.True(t, inserted)

	got, err := repo.Get(ctx, 7, "parse", 1, "fresh")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, fresh.ID, got.ID)
}

func TestProcessingArtifactDeleteExpiredDeletesOnlyExpiredRows(t *testing.T) {
	db := setupProcessingArtifactTestDB(t)
	repo := NewProcessingArtifactRepository(db)
	ctx := context.Background()
	expiredAt := time.Now().Add(-time.Hour)
	freshExpiresAt := time.Now().Add(time.Hour)
	rows := []*types.ProcessingArtifact{
		{
			ID:              uuid.NewString(),
			TenantID:        7,
			Stage:           "parse",
			KeyVersion:      1,
			ArtifactKey:     "expired",
			ProcessorDigest: "processor",
			OutputDigest:    "output",
			OutputSchema:    "parse.v1",
			Codec:           "json",
			Payload:         []byte(`{"old":true}`),
			PayloadChecksum: "checksum",
			PayloadSize:     int64(len([]byte(`{"old":true}`))),
			CreatedAt:       time.Now().Add(-2 * time.Hour),
			ExpiresAt:       &expiredAt,
		},
		{
			ID:              uuid.NewString(),
			TenantID:        7,
			Stage:           "parse",
			KeyVersion:      1,
			ArtifactKey:     "fresh",
			ProcessorDigest: "processor",
			OutputDigest:    "output",
			OutputSchema:    "parse.v1",
			Codec:           "json",
			Payload:         []byte(`{"fresh":true}`),
			PayloadChecksum: "checksum",
			PayloadSize:     int64(len([]byte(`{"fresh":true}`))),
			CreatedAt:       time.Now(),
			ExpiresAt:       &freshExpiresAt,
		},
	}
	for _, row := range rows {
		inserted, err := repo.PutIfAbsent(ctx, row)
		require.NoError(t, err)
		require.True(t, inserted)
	}

	deleted, err := repo.DeleteExpired(ctx, time.Now(), 100)
	require.NoError(t, err)
	require.Equal(t, int64(1), deleted)

	got, err := repo.Get(ctx, 7, "parse", 1, "fresh")
	require.NoError(t, err)
	require.Equal(t, rows[1].ID, got.ID)
	_, err = repo.Get(ctx, 7, "parse", 1, "expired")
	require.ErrorIs(t, err, ErrProcessingArtifactNotFound)
}
