package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProcessingArtifactRepository(t *testing.T) (*gorm.DB, *processingArtifactRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.ProcessingArtifact{}))
	return db, &processingArtifactRepository{db: db}
}

func processingArtifactCandidate(t *testing.T, tenantID uint64, payload string) *types.ProcessingArtifact {
	t.Helper()
	key, err := artifact.BuildKey(tenantID, artifact.KeyMaterial{
		KeyVersion: 1,
		Stage:      "embedding",
		DirectInputs: []artifact.DirectInput{{
			Role:   "text",
			Digest: artifact.SHA256Hex([]byte("input")),
		}},
		Processor:            artifact.ProcessorIdentity{ModelID: "model"},
		RenderedRequest:      "exact input",
		Options:              map[string]any{"dimensions": 3},
		CanonicalizerVersion: artifact.CanonicalJSONVersion,
		OutputSchemaVersion:  "embedding.float32.v1",
	})
	require.NoError(t, err)
	candidate, err := artifact.NewInlineArtifact(key, artifact.CodecFloat32BEV1, []byte(payload))
	require.NoError(t, err)
	return candidate
}

func TestProcessingArtifactPutIfAbsentKeepsWinner(t *testing.T) {
	_, repository := setupProcessingArtifactRepository(t)
	ctx := context.Background()
	first := processingArtifactCandidate(t, 1, "first")
	second := processingArtifactCandidate(t, 1, "second")

	winner, created, err := repository.PutIfAbsent(ctx, first)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, "first", string(winner.Payload))

	winner, created, err = repository.PutIfAbsent(ctx, second)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, "first", string(winner.Payload))
}

func TestProcessingArtifactTenantBoundaryAndBatchGet(t *testing.T) {
	_, repository := setupProcessingArtifactRepository(t)
	ctx := context.Background()
	first := processingArtifactCandidate(t, 1, "tenant-one")
	second := processingArtifactCandidate(t, 2, "tenant-two")

	winners, err := repository.PutManyIfAbsent(ctx, []*types.ProcessingArtifact{first, second})
	require.NoError(t, err)
	require.Len(t, winners, 2)
	assert.Equal(t, first.ArtifactKey, second.ArtifactKey)
	assert.Equal(t, "tenant-one", string(winners[first.Lookup()].Payload))
	assert.Equal(t, "tenant-two", string(winners[second.Lookup()].Payload))
}

func TestProcessingArtifactDeleteCorruptIsConditional(t *testing.T) {
	db, repository := setupProcessingArtifactRepository(t)
	ctx := context.Background()
	candidate := processingArtifactCandidate(t, 1, "value")
	_, _, err := repository.PutIfAbsent(ctx, candidate)
	require.NoError(t, err)

	require.NoError(t, repository.DeleteCorrupt(ctx, candidate.Lookup(), artifact.SHA256Hex([]byte("other"))))
	var count int64
	require.NoError(t, db.Model(&types.ProcessingArtifact{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	require.NoError(t, repository.DeleteCorrupt(ctx, candidate.Lookup(), candidate.PayloadChecksum))
	require.NoError(t, db.Model(&types.ProcessingArtifact{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestProcessingArtifactSuccessfulEmptyPayloadRoundTrips(t *testing.T) {
	_, repository := setupProcessingArtifactRepository(t)
	candidate := processingArtifactCandidate(t, 1, "")

	winner, created, err := repository.PutIfAbsent(context.Background(), candidate)
	require.NoError(t, err)
	require.True(t, created)
	require.NotNil(t, winner.Payload)
	assert.Empty(t, winner.Payload)

	reloaded, err := repository.Get(context.Background(), candidate.Lookup())
	require.NoError(t, err)
	require.NotNil(t, reloaded.Payload)
	assert.Empty(t, reloaded.Payload)
}
