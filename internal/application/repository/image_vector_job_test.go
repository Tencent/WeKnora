package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestImageVectorJobsLeaseReplayAndSourceRemoval(t *testing.T) {
	imageAssetBackends(t, func(t *testing.T, db *gorm.DB) {
		require.NoError(t, db.AutoMigrate(&types.Knowledge{}))
		migration := "../../../migrations/sqlite/000034_image_vector_jobs.up.sql"
		if db.Name() == "postgres" {
			migration = "../../../migrations/versioned/000115_image_vector_jobs.up.sql"
		}
		sql, err := os.ReadFile(migration)
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(sql)).Error)
		fx := newImageFixture(t, db)
		source := fx.add("text", 0, fixtureImage{URL: "resource://original"})
		knowledge := &types.Knowledge{ID: source.KnowledgeID, TenantID: 1, KnowledgeBaseID: fx.kbID, Title: "source"}
		require.NoError(t, db.Create(knowledge).Error)
		fx.add("image_caption", 1, fixtureImage{URL: "resource://original", Caption: "caption"})
		jobs := NewImageVectorRepository(db)
		ctx := context.Background()
		rows, err := jobs.Sources(ctx, 1, fx.kbID, "", 0, 100)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		other, err := jobs.Sources(ctx, 2, fx.kbID, "", 0, 100)
		require.NoError(t, err)
		require.Empty(t, other)
		job := &types.ImageVectorJob{
			ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: fx.kbID, KnowledgeID: source.KnowledgeID,
			SourceChunkID: rows[0].ChunkID, ImageURL: rows[0].ImageURL, Fingerprint: "v1",
			ChunkID: uuid.NewString(), Status: "pending",
		}
		queued, err := jobs.Ensure(ctx, job)
		require.NoError(t, err)
		require.True(t, queued)
		claimed, err := jobs.Claim(ctx, job, "worker-a")
		require.NoError(t, err)
		require.True(t, claimed)
		claimed, err = jobs.Claim(ctx, job, "worker-b")
		require.NoError(t, err)
		require.False(t, claimed)
		require.NoError(t, jobs.Finish(ctx, job, "worker-b", "completed", ""))
		current, err := jobs.Get(ctx, 1, job.ID)
		require.NoError(t, err)
		require.Equal(t, "running", current.Status)
		coverage, err := jobs.Coverage(ctx, 1, fx.kbID, "v1")
		require.NoError(t, err)
		require.EqualValues(t, 1, coverage.Pending)
		require.NoError(t, jobs.Finish(ctx, job, "worker-a", "failed", "provider_failure"))
		queued, err = jobs.Ensure(ctx, job)
		require.NoError(t, err)
		require.True(t, queued)
		claimed, err = jobs.Claim(ctx, job, "worker-c")
		require.NoError(t, err)
		require.True(t, claimed)
		chunk := &types.Chunk{
			ID: job.ChunkID, TenantID: 1, KnowledgeID: job.KnowledgeID, KnowledgeBaseID: fx.kbID,
			ChunkType: types.ChunkTypeImageVector, ImageInfo: source.ImageInfo,
			IsEnabled: true, Status: int(types.ChunkStatusIndexed),
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		require.NoError(t, jobs.SaveVectorChunk(ctx, job, chunk))

		require.NoError(t, jobs.RefreshMetadata(ctx, 1, fx.kbID, source.KnowledgeID, types.ImageInfo{
			URL: "resource://original", OriginalURL: "resource://original", Caption: "late caption",
		}))
		var refreshed types.Chunk
		require.NoError(t, db.First(&refreshed, "id = ?", job.ChunkID).Error)
		require.Equal(t, "late caption", refreshed.Content)
		require.Equal(t, chunk.SeqID, refreshed.SeqID)
		require.NoError(t, jobs.Finish(ctx, job, "worker-c", "completed", ""))
		queued, err = jobs.Ensure(ctx, job)
		require.NoError(t, err)
		require.False(t, queued)
		coverage, err = jobs.Coverage(ctx, 1, fx.kbID, "v1")
		require.NoError(t, err)
		require.EqualValues(t, 1, coverage.Completed)
		coverage, err = jobs.Coverage(ctx, 1, fx.kbID, "v2")
		require.NoError(t, err)
		require.EqualValues(t, 1, coverage.Missing)
		job.Fingerprint = "v2"
		queued, err = jobs.Ensure(ctx, job)
		require.NoError(t, err)
		require.True(t, queued)
		require.NoError(t, jobs.SaveVectorChunk(ctx, job, chunk))
		var total int64
		require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", job.ChunkID).Count(&total).Error)
		require.EqualValues(t, 1, total)
		require.NoError(t, db.Delete(knowledge).Error)
		_, err = jobs.Source(ctx, job)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		require.Error(t, jobs.SaveVectorChunk(ctx, job, chunk))
		coverage, err = jobs.Coverage(ctx, 1, fx.kbID, "v2")
		require.NoError(t, err)
		require.Zero(t, coverage.Total)
	})
}
