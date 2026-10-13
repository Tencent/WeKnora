package service

import (
	"bytes"
	"context"
	stderrors "errors"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureServiceLogs redirects the process logger for one test, so the WARN
// raised for a partial retrieve can be asserted on. Not parallel-safe: the
// logger output is process-wide.
func captureServiceLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	return &buf
}

func keywordsRetrieveParams(query string) []types.RetrieveParams {
	return []types.RetrieveParams{{
		Query:         query,
		TopK:          50,
		RetrieverType: types.KeywordsRetrieverType,
	}}
}

// A store that answered only partially used to reach the search layer as a
// complete result: some collections failed, the hits from the rest came back
// with a nil error, and nothing said the answer was incomplete. The failure now
// travels with the results (RetrieveResult.Error -> CompositeRetrieveEngine)
// and is logged here, without discarding what was retrieved (#3835).
func TestRetrieveFromStores_LogsPartialFailureAndKeepsResults(t *testing.T) {
	logs := captureServiceLogs(t)
	cause := stderrors.New("qdrant keyword search failed in 1 of 2 matched collections")
	fake := &fakeRetrieveEngineService{
		engineType:      types.QdrantRetrieverEngineType,
		support:         []types.RetrieverType{types.KeywordsRetrieverType},
		canned:          []*types.IndexWithScore{{ChunkID: "c1", Score: 0.5}},
		cannedResultErr: cause,
	}
	g := &storeGroup{
		StoreID:    "store-x",
		KBIDs:      []string{"kb-1"},
		Engine:     buildBoundComposite(t, fake),
		BaseParams: keywordsRetrieveParams("q"),
		TopK:       50,
	}

	res, err := (&knowledgeBaseService{}).retrieveFromStores(
		context.Background(), []*storeGroup{g}, retriever.EngineAwareNormalizer{})

	require.NoError(t, err, "a partial failure must not fail the search")
	require.Len(t, res, 1)
	require.Len(t, res[0].Results, 1)
	assert.Equal(t, "c1", res[0].Results[0].ChunkID)
	assert.ErrorIs(t, res[0].Error, cause, "the result set must keep its partial failure")
	assert.Contains(t, logs.String(), "retrieve returned partial results")
	assert.Contains(t, logs.String(), "1 of 2 matched collections")
}

// The multi-store path must keep partial results too, instead of cancelling
// siblings and failing the whole search.
func TestRetrieveFromStores_MultiGroupKeepsPartialResults(t *testing.T) {
	logs := captureServiceLogs(t)
	cause := stderrors.New("milvus keyword search failed in 2 of 3 matched collections")
	partial := &fakeRetrieveEngineService{
		engineType:      types.MilvusRetrieverEngineType,
		support:         []types.RetrieverType{types.KeywordsRetrieverType},
		canned:          []*types.IndexWithScore{{ChunkID: "partial-1", Score: 0.4}},
		cannedResultErr: cause,
	}
	clean := &fakeRetrieveEngineService{
		engineType: types.MilvusRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "clean-1", Score: 0.6}},
	}
	groups := []*storeGroup{
		{
			StoreID:    "store-partial",
			KBIDs:      []string{"kb-partial"},
			Engine:     buildBoundComposite(t, partial),
			BaseParams: keywordsRetrieveParams("q"),
			TopK:       50,
		},
		{
			StoreID:    "store-clean",
			KBIDs:      []string{"kb-clean"},
			Engine:     buildBoundComposite(t, clean),
			BaseParams: keywordsRetrieveParams("q"),
			TopK:       50,
		},
	}

	res, err := (&knowledgeBaseService{}).retrieveFromStores(
		context.Background(), groups, retriever.EngineAwareNormalizer{})

	require.NoError(t, err, "one degraded store must not fail the whole search")
	require.Len(t, res, 2)
	assert.Contains(t, logs.String(), "retrieve returned partial results")
	assert.Contains(t, logs.String(), "2 of 3 matched collections")
}

// A complete search that legitimately hit nothing must stay quiet: no partial
// warning, nil error.
func TestRetrieveFromStores_CompleteEmptySearchStaysQuiet(t *testing.T) {
	logs := captureServiceLogs(t)
	fake := &fakeRetrieveEngineService{
		engineType: types.QdrantRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
	}
	g := &storeGroup{
		StoreID:    "store-x",
		KBIDs:      []string{"kb-1"},
		Engine:     buildBoundComposite(t, fake),
		BaseParams: keywordsRetrieveParams("q"),
		TopK:       50,
	}

	res, err := (&knowledgeBaseService{}).retrieveFromStores(
		context.Background(), []*storeGroup{g}, retriever.EngineAwareNormalizer{})

	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Empty(t, res[0].Results)
	assert.NoError(t, res[0].Error)
	assert.NotContains(t, logs.String(), "partial", "a complete zero-hit search must not warn")
}

// An engine-level failure still fails the search: exposing partial results must
// not weaken the all-or-nothing policy for stores that did not answer at all.
func TestRetrieveFromStores_EngineLevelErrorStillFails(t *testing.T) {
	logs := captureServiceLogs(t)
	cause := stderrors.New("vector store unavailable")
	fake := &fakeRetrieveEngineService{
		engineType: types.QdrantRetrieverEngineType,
		support:    []types.RetrieverType{types.KeywordsRetrieverType},
		canned:     []*types.IndexWithScore{{ChunkID: "never-returned", Score: 0.5}},
		cannedErr:  cause,
	}
	g := &storeGroup{
		StoreID:    "store-x",
		KBIDs:      []string{"kb-1"},
		Engine:     buildBoundComposite(t, fake),
		BaseParams: keywordsRetrieveParams("q"),
		TopK:       50,
	}

	res, err := (&knowledgeBaseService{}).retrieveFromStores(
		context.Background(), []*storeGroup{g}, retriever.EngineAwareNormalizer{})

	require.ErrorIs(t, err, cause)
	assert.Nil(t, res, "an engine-level failure still returns no results")
	assert.NotContains(t, logs.String(), "partial")
}
