package retriever

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// cannedEngine implements the three RetrieveEngineService methods the
// composite uses, returning one canned result set per retriever type. The
// embedded interface keeps the remaining methods out of this file; reaching
// one of them would panic on the nil interface, which is loud enough for a
// test.
type cannedEngine struct {
	interfaces.RetrieveEngineService
	engineType types.RetrieverEngineType
	support    []types.RetrieverType
	results    map[types.RetrieverType][]*types.RetrieveResult
	errs       map[types.RetrieverType]error
}

func (e *cannedEngine) EngineType() types.RetrieverEngineType { return e.engineType }

func (e *cannedEngine) Support() []types.RetrieverType { return e.support }

func (e *cannedEngine) Retrieve(_ context.Context, p types.RetrieveParams,
) ([]*types.RetrieveResult, error) {
	if err := e.errs[p.RetrieverType]; err != nil {
		return nil, err
	}
	return e.results[p.RetrieverType], nil
}

func compositeWith(engine *cannedEngine) *CompositeRetrieveEngine {
	return &CompositeRetrieveEngine{engineInfos: []*engineInfo{{
		retrieveEngine: engine,
		retrieverType:  engine.support,
	}}}
}

func keywordsParams(query string) []types.RetrieveParams {
	return []types.RetrieveParams{{
		Query:         query,
		TopK:          10,
		RetrieverType: types.KeywordsRetrieverType,
	}}
}

// A result set that answered partially carries the failure in
// RetrieveResult.Error. The composite must hand both the results and the error
// to its caller: dropping the results loses evidence that was retrieved,
// swallowing the error makes the incomplete answer look complete (#3835).
func TestCompositeRetrieveExposesPartialResultError(t *testing.T) {
	cause := errors.New("qdrant keyword search failed in 1 of 2 matched collections")
	engine := &cannedEngine{
		engineType: types.QdrantRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
		results: map[types.RetrieverType][]*types.RetrieveResult{
			types.KeywordsRetrieverType: {{
				Results:             []*types.IndexWithScore{{ChunkID: "chunk-1", Score: 1}},
				RetrieverEngineType: types.QdrantRetrieverEngineType,
				RetrieverType:       types.KeywordsRetrieverType,
				Error:               cause,
			}},
		},
	}

	results, err := compositeWith(engine).Retrieve(context.Background(), keywordsParams("q"))

	require.Error(t, err, "a partial result set must surface as an error")
	require.ErrorIs(t, err, cause, "the underlying cause must stay reachable")
	require.Len(t, results, 1, "partial results must survive alongside the error")
	require.Len(t, results[0].Results, 1)
	require.Equal(t, "chunk-1", results[0].Results[0].ChunkID)
	require.ErrorIs(t, results[0].Error, cause, "the result set keeps its own failure")
}

// A complete search — including a genuine zero-hit one — must stay quiet.
func TestCompositeRetrieveStaysSilentWhenResultSetsAreComplete(t *testing.T) {
	engine := &cannedEngine{
		engineType: types.QdrantRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
		results: map[types.RetrieverType][]*types.RetrieveResult{
			types.KeywordsRetrieverType: {{
				RetrieverEngineType: types.QdrantRetrieverEngineType,
				RetrieverType:       types.KeywordsRetrieverType,
			}},
		},
	}

	results, err := compositeWith(engine).Retrieve(context.Background(), keywordsParams("q"))

	require.NoError(t, err, "an empty but complete result set is not a failure")
	require.Len(t, results, 1)
	require.Empty(t, results[0].Results)
	require.NoError(t, results[0].Error)
}

// An engine-level error is not a partial result: it still drops everything
// collected so far, so the existing all-or-nothing policy is unchanged.
func TestCompositeRetrieveDropsResultsOnEngineLevelError(t *testing.T) {
	cause := errors.New("vector store unavailable")
	engine := &cannedEngine{
		engineType: types.QdrantRetrieverEngineType,
		support:    []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType},
		results: map[types.RetrieverType][]*types.RetrieveResult{
			types.VectorRetrieverType: {{
				Results:             []*types.IndexWithScore{{ChunkID: "vector-1", Score: 0.9}},
				RetrieverEngineType: types.QdrantRetrieverEngineType,
				RetrieverType:       types.VectorRetrieverType,
			}},
		},
		errs: map[types.RetrieverType]error{types.KeywordsRetrieverType: cause},
	}

	results, err := compositeWith(engine).Retrieve(context.Background(), []types.RetrieveParams{
		{Query: "q", TopK: 10, RetrieverType: types.VectorRetrieverType},
		{Query: "q", TopK: 10, RetrieverType: types.KeywordsRetrieverType},
	})

	require.ErrorIs(t, err, cause)
	require.Nil(t, results, "an engine-level failure still drops all results")
}

// The canned engine must stay usable wherever the registry hands out an
// engine service.
var _ interfaces.RetrieveEngineService = (*cannedEngine)(nil)
