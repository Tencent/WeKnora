package retriever

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/testutil/modelcount"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/require"
)

// embeddingObservationRepository records the vectors that reach the repository
// after the real BatchIndex embedding path has completed.
//
// It does not persist anything. The test only needs to verify that every input
// item received a vector before BatchIndex returned.
type embeddingObservationRepository struct {
	interfaces.RetrieveEngineRepository

	mu sync.Mutex

	batchSaveCount int
	vectorCounts   []int
}

// BatchSave records one repository batch without writing to an external vector
// database.
func (r *embeddingObservationRepository) BatchSave(
	_ context.Context,
	_ []*types.IndexInfo,
	params map[string]any,
) error {
	embeddingMap, _ := params["embedding"].(map[string][]float32)

	r.mu.Lock()
	defer r.mu.Unlock()

	r.batchSaveCount++
	r.vectorCounts = append(
		r.vectorCounts,
		len(embeddingMap),
	)

	return nil
}

// Snapshot returns immutable copies of the recorded repository observations.
func (r *embeddingObservationRepository) Snapshot() (
	int,
	[]int,
) {
	r.mu.Lock()
	defer r.mu.Unlock()

	vectorCounts := make(
		[]int,
		len(r.vectorCounts),
	)
	copy(vectorCounts, r.vectorCounts)

	return r.batchSaveCount, vectorCounts
}

func TestEmbedding_PreCacheBaseline_RecomputesSameContent(
	t *testing.T,
) {
	// WeKnora currently defaults to five embedding inputs per provider
	// request. Set it explicitly so the test remains deterministic even when
	// the developer's environment has a different value.
	t.Setenv("BATCH_EMBED_SIZE", "5")

	pool, err := ants.NewPool(4)
	require.NoError(t, err)
	t.Cleanup(pool.Release)

	countingEmbedder := modelcount.NewCountingEmbedder(
		modelcount.CountingEmbedderOptions{
			ModelID:    "embedding-test",
			ModelName:  "test-embedding-model",
			Dimensions: 4,
			Pooler: embedding.NewBatchEmbedder(
				pool,
			),
		},
	)

	repository := &embeddingObservationRepository{}
	service := &KeywordsVectorHybridRetrieveEngineService{
		indexRepository: repository,
		engineType:      types.SQLiteRetrieverEngineType,
	}

	indexInfoList := makeEmbeddingObservationIndexInfoList(
		12,
	)

	ctx := types.WithIngestionOperation(
		context.Background(),
		types.IngestionOperationEmbeddingChunk,
	)

	err = service.BatchIndex(
		ctx,
		countingEmbedder,
		indexInfoList,
		[]types.RetrieverType{
			types.VectorRetrieverType,
		},
	)
	require.NoError(t, err)

	firstSnapshot := countingEmbedder.Snapshot()

	// Twelve inputs with BATCH_EMBED_SIZE=5 must produce three actual
	// BatchEmbed requests: 5 + 5 + 2.
	require.Equal(t, 3, firstSnapshot.RequestCount)
	require.ElementsMatch(
		t,
		[]int{5, 5, 2},
		firstSnapshot.BatchSizes,
	)
	require.Equal(
		t,
		12,
		firstSnapshot.TotalInputItems,
	)
	require.Equal(
		t,
		4,
		firstSnapshot.Dimensions,
	)
	require.Len(
		t,
		firstSnapshot.Calls,
		3,
	)

	for _, call := range firstSnapshot.Calls {
		require.Equal(
			t,
			types.IngestionOperationEmbeddingChunk,
			call.Operation,
		)
	}

	firstBatchSaveCount, firstVectorCounts :=
		repository.Snapshot()

	require.Equal(t, 1, firstBatchSaveCount)
	require.Equal(
		t,
		[]int{12},
		firstVectorCounts,
	)

	// Run the exact same indexing input again. This is the pre-cache
	// baseline: the current implementation must call the embedder again
	// because no reusable embedding cache exists yet.
	err = service.BatchIndex(
		ctx,
		countingEmbedder,
		indexInfoList,
		[]types.RetrieverType{
			types.VectorRetrieverType,
		},
	)
	require.NoError(t, err)

	secondSnapshot := countingEmbedder.Snapshot()

	// The second execution adds another three provider-adapter requests.
	// A future embedding cache should change this second-run delta from
	// three to zero.
	require.Equal(t, 6, secondSnapshot.RequestCount)
	require.ElementsMatch(
		t,
		[]int{
			5,
			5,
			2,
			5,
			5,
			2,
		},
		secondSnapshot.BatchSizes,
	)
	require.Equal(
		t,
		24,
		secondSnapshot.TotalInputItems,
	)
	require.Len(
		t,
		secondSnapshot.Calls,
		6,
	)

	for _, call := range secondSnapshot.Calls {
		require.Equal(
			t,
			types.IngestionOperationEmbeddingChunk,
			call.Operation,
		)
	}

	secondRunRequestDelta :=
		secondSnapshot.RequestCount -
			firstSnapshot.RequestCount
	secondRunItemDelta :=
		secondSnapshot.TotalInputItems -
			firstSnapshot.TotalInputItems

	require.Equal(t, 3, secondRunRequestDelta)
	require.Equal(t, 12, secondRunItemDelta)

	secondBatchSaveCount, secondVectorCounts :=
		repository.Snapshot()

	require.Equal(t, 2, secondBatchSaveCount)
	require.Equal(
		t,
		[]int{12, 12},
		secondVectorCounts,
	)
}

// makeEmbeddingObservationIndexInfoList creates stable indexing inputs.
//
// The second BatchIndex call receives the same source IDs and content, which is
// important: the test is proving that unchanged content is recomputed before a
// cache is introduced.
func makeEmbeddingObservationIndexInfoList(
	count int,
) []*types.IndexInfo {
	indexInfoList := make(
		[]*types.IndexInfo,
		0,
		count,
	)

	for i := 0; i < count; i++ {
		sourceID := fmt.Sprintf(
			"source-%02d",
			i,
		)
		content := fmt.Sprintf(
			"stable embedding input %02d",
			i,
		)

		indexInfoList = append(
			indexInfoList,
			&types.IndexInfo{
				ID:              sourceID,
				Content:         content,
				SourceID:        sourceID,
				SourceType:      types.ChunkSourceType,
				ChunkID:         sourceID,
				KnowledgeID:     "knowledge-test",
				KnowledgeBaseID: "kb-test",
				KnowledgeType:   "document",
				IsEnabled:       true,
			},
		)
	}

	return indexInfoList
}
