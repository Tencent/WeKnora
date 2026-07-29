package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSaveChunkRevisionIsAtomicAndOptimistic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{}, &types.Chunk{}, &types.ChunkRevision{}, &types.TaskPendingOp{},
	))
	repo := NewChunkRepository(db)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "knowledge", TenantID: 1, KnowledgeBaseID: "kb",
		ParseStatus: types.ParseStatusCompleted,
	}).Error)
	chunk := &types.Chunk{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		Content: "before", SourceContent: "before", ChunkType: types.ChunkTypeText,
		IsEnabled: true, IndexStatus: "ready", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))

	snapshot := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		ChunkID: chunk.ID, Revision: 0, Content: "before", IsEnabled: true,
		EditSource: "user", EditedAt: now, CreatedAt: now,
	}
	chunk.Content = "after"
	chunk.ContentRevision = 1
	require.NoError(t, repo.SaveChunkRevision(ctx, chunk, snapshot, 0))

	stored, err := repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "after", stored.Content)
	require.Equal(t, 1, stored.ContentRevision)
	revisions, err := repo.ListChunkRevisions(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Len(t, revisions, 1)
	require.Equal(t, "before", revisions[0].Content)

	stale := *chunk
	stale.Content = "stale write"
	stale.ContentRevision = 1
	staleSnapshot := *snapshot
	staleSnapshot.ID = uuid.NewString()
	require.ErrorIs(t, repo.SaveChunkRevision(ctx, &stale, &staleSnapshot, 0), ErrChunkRevisionConflict)

	stored, err = repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "after", stored.Content)
	count := int64(0)
	require.NoError(t, db.Model(&types.ChunkRevision{}).Count(&count).Error)
	require.Equal(t, int64(1), count)
	require.False(t, errors.Is(gorm.ErrRecordNotFound, ErrChunkRevisionConflict))
}

func TestSaveChunkRevisionWithPendingOpIsAtomic(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{}, &types.Chunk{}, &types.ChunkRevision{}, &types.TaskPendingOp{},
	))
	repo := NewChunkRepository(db)
	outboxRepo, ok := repo.(interfaces.ChunkRevisionOutboxRepository)
	require.True(t, ok)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "knowledge", TenantID: 1, KnowledgeBaseID: "kb",
		ParseStatus: types.ParseStatusCompleted,
	}).Error)
	chunk := &types.Chunk{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		Content: "before", SourceContent: "before", ChunkType: types.ChunkTypeText,
		IsEnabled: true, IndexStatus: "ready", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))

	revision := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge",
		ChunkID: chunk.ID, Revision: 0, Content: "before", IsEnabled: true,
		EditSource: "user", EditedAt: now, CreatedAt: now,
	}
	chunk.Content = "after"
	chunk.ContentRevision = 1
	op := &types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeWikiIngest,
		Scope: types.TaskScopeKnowledgeBase, ScopeID: "kb",
		Op: "ingest", DedupKey: "knowledge", Payload: []byte(`{"op":"ingest"}`),
	}
	require.NoError(t, outboxRepo.SaveChunkRevisionWithPendingOp(ctx, chunk, revision, 0, op))

	var pending types.TaskPendingOp
	require.NoError(t, db.First(&pending).Error)
	require.Equal(t, "knowledge", pending.DedupKey)
	stored, err := repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "after", stored.Content)

	staleRevision := *revision
	staleRevision.ID = uuid.NewString()
	staleOp := *op
	staleOp.ID = 0
	staleOp.Payload = []byte(`{"op":"stale"}`)
	err = outboxRepo.SaveChunkRevisionWithPendingOp(ctx, chunk, &staleRevision, 0, &staleOp)
	require.ErrorIs(t, err, ErrChunkRevisionConflict)
	var pendingCount int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&pendingCount).Error)
	require.Equal(t, int64(1), pendingCount, "outbox row must roll back with a stale chunk write")

	// Force the final outbox INSERT to fail after the chunk/revision writes.
	// The transaction must restore both of those earlier writes as well.
	rolledBackRevision := *revision
	rolledBackRevision.ID = uuid.NewString()
	rolledBackRevision.Revision = 1
	rolledBackRevision.Content = "after"
	duplicateOp := *op // Retain the already-persisted primary key on purpose.
	chunk.Content = "must roll back"
	chunk.ContentRevision = 2
	err = outboxRepo.SaveChunkRevisionWithPendingOp(
		ctx, chunk, &rolledBackRevision, 1, &duplicateOp,
	)
	require.Error(t, err)
	stored, err = repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "after", stored.Content)
	require.Equal(t, 1, stored.ContentRevision)
	var revisionCount int64
	require.NoError(t, db.Model(&types.ChunkRevision{}).Count(&revisionCount).Error)
	require.Equal(t, int64(1), revisionCount, "revision must roll back with a failed outbox insert")
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&pendingCount).Error)
	require.Equal(t, int64(1), pendingCount)
}

func TestSaveChunkRevisionWithPendingOpRejectsDeletingKnowledge(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Knowledge{}, &types.Chunk{}, &types.ChunkRevision{}, &types.TaskPendingOp{},
	))
	repo := NewChunkRepository(db)
	outboxRepo := repo.(interfaces.ChunkRevisionOutboxRepository)
	ctx := context.Background()
	now := time.Now()
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "knowledge-deleting", TenantID: 1, KnowledgeBaseID: "kb",
		ParseStatus: types.ParseStatusDeleting,
	}).Error)
	chunk := &types.Chunk{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "knowledge-deleting",
		Content: "before", SourceContent: "before", ChunkType: types.ChunkTypeText,
		IsEnabled: true, IndexStatus: "ready", CreatedAt: now, UpdatedAt: now,
	}
	require.NoError(t, repo.CreateChunks(ctx, []*types.Chunk{chunk}))
	revision := &types.ChunkRevision{
		ID: uuid.NewString(), TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: chunk.KnowledgeID,
		ChunkID: chunk.ID, Revision: 0, Content: "before", IsEnabled: true,
		EditSource: "user", EditedAt: now, CreatedAt: now,
	}
	chunk.Content = "must not persist"
	chunk.ContentRevision = 1
	op := &types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeWikiIngest,
		Scope: types.TaskScopeKnowledgeBase, ScopeID: "kb",
		Op: "ingest", DedupKey: chunk.KnowledgeID, Payload: []byte(`{"op":"ingest"}`),
	}

	err = outboxRepo.SaveChunkRevisionWithPendingOp(ctx, chunk, revision, 0, op)
	require.ErrorIs(t, err, ErrChunkRevisionConflict)
	stored, err := repo.GetChunkByID(ctx, 1, chunk.ID)
	require.NoError(t, err)
	require.Equal(t, "before", stored.Content)
	require.Zero(t, stored.ContentRevision)
	var count int64
	require.NoError(t, db.Model(&types.ChunkRevision{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	require.Zero(t, count)
}
