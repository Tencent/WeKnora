package retriever

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type recordingRepository struct {
	saveOnlyRepository
	rows       []*types.IndexInfo
	embeddings map[string][]float32
}

func (r *recordingRepository) BatchSave(
	_ context.Context, indexInfoList []*types.IndexInfo, params map[string]any,
) error {
	r.rows = append(r.rows, indexInfoList...)
	if m, ok := params["embedding"].(map[string][]float32); ok {
		if r.embeddings == nil {
			r.embeddings = map[string][]float32{}
		}
		for k, v := range m {
			r.embeddings[k] = v
		}
	}
	return nil
}

func compositeOver(repo *recordingRepository, retrievers ...types.RetrieverType) *CompositeRetrieveEngine {
	return &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
		retrieveEngine: &KeywordsVectorHybridRetrieveEngineService{indexRepository: repo},
		retrieverType:  retrievers,
	}}}
}

func TestBatchIndexVectorsStoresTheGivenVectorsNotTheTextOnes(t *testing.T) {
	repo := &recordingRepository{}
	c := compositeOver(repo, types.KeywordsRetrieverType, types.VectorRetrieverType)
	// The model would embed the caption as text; the stored vector must be
	// the image's.
	model := &capturingEmbedder{}
	rows := []*types.IndexInfo{
		{SourceID: "a", ChunkID: "a", Content: "a red bicycle", SourceType: types.ImageSourceType},
		{SourceID: "b", ChunkID: "b", Content: "a blue car", SourceType: types.ImageSourceType},
	}
	require.NoError(t, c.BatchIndexVectors(context.Background(), model, rows, [][]float32{{0.1, 0.2}, {0.3, 0.4}}))

	assert.Empty(t, model.batchTexts, "the text embedder is never asked")
	assert.Equal(t, map[string][]float32{"a": {0.1, 0.2}, "b": {0.3, 0.4}}, repo.embeddings)
	require.Len(t, repo.rows, 2)
	assert.Equal(t, types.ImageSourceType, repo.rows[0].SourceType)
}

func TestBatchIndexVectorsRefusesWhatWouldMisalignVectors(t *testing.T) {
	c := compositeOver(&recordingRepository{}, types.VectorRetrieverType)
	rows := []*types.IndexInfo{{SourceID: "a"}, {SourceID: "a"}}
	assert.ErrorContains(t,
		c.BatchIndexVectors(context.Background(), &capturingEmbedder{}, rows, [][]float32{{1}, {2}}),
		"appears twice")
	assert.ErrorContains(t,
		c.BatchIndexVectors(context.Background(), &capturingEmbedder{}, rows[:1], [][]float32{{1}, {2}}),
		"2 vectors for 1")
}
