package repository

import (
	"context"
	"runtime"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCreateChunks_SQLiteConcurrentCallsAssignUniqueSeqIDs(t *testing.T) {
	db := setupChunkTestDB(t)

	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Match the runtime pool configuration. A single connection serializes SQL
	// statements, but the transaction is what makes allocation and insertion one
	// indivisible operation for concurrent callers.
	sqlDB.SetMaxOpenConns(1)

	previousMaxProcs := runtime.GOMAXPROCS(8)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(previousMaxProcs)
	})

	repo := NewChunkRepository(db)
	ctx := context.Background()

	const workers = 64
	start := make(chan struct{})
	results := make(chan error, workers)
	chunks := make([]*types.Chunk, workers)

	for i := 0; i < workers; i++ {
		chunks[i] = makeChunk(
			uuid.NewString(),
			uuid.NewString(),
			"text",
		)
		go func(chunk *types.Chunk) {
			<-start
			results <- repo.CreateChunks(
				ctx,
				[]*types.Chunk{chunk},
			)
		}(chunks[i])
	}

	close(start)

	for i := 0; i < workers; i++ {
		require.NoError(t, <-results)
	}

	seqIDs := make(map[int64]struct{}, workers)
	for _, chunk := range chunks {
		saved, err := repo.GetChunkByID(ctx, chunk.TenantID, chunk.ID)
		require.NoError(t, err)
		require.NotZero(t, saved.SeqID)
		require.NotContains(t, seqIDs, saved.SeqID)
		seqIDs[saved.SeqID] = struct{}{}
	}
	require.Len(t, seqIDs, workers)
}
