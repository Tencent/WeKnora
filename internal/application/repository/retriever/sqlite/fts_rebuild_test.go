//go:build sqlite_fts5

// Package sqlite implements SQLite retrieval storage and index lifecycle contracts.
package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKeywordScoresAfterDeleteAndRebuild(t *testing.T) {
	r := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()
	texts := []string{"alpha alpha beta", "alpha gamma gamma gamma", "beta gamma"}
	load := func() {
		for i, text := range texts {
			info := sqliteTestIndex(string(rune('a'+i)), "kb", "doc", "tag", true)
			info.Content = text
			require.NoError(t, r.Save(ctx, info, nil))
		}
	}
	query := func() map[string]float64 {
		results, err := r.keywordsRetrieve(ctx, types.RetrieveParams{Query: "alpha", TopK: 10})
		require.NoError(t, err)
		out := map[string]float64{}
		for _, result := range results[0].Results {
			out[result.Content] = result.Score
		}
		return out
	}
	load()
	want := query()
	for round := 0; round < 3; round++ {
		require.NoError(t, r.DeleteByKnowledgeIDList(ctx, []string{"doc"}, 0, ""))
		load()
		require.Equal(t, want, query(), "identical corpus rebuild %d", round)
	}
}

func TestDeleteIndexedRowsPreservesOtherKnowledgeBase(t *testing.T) {
	r := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()
	for _, kb := range []string{"keep", "remove"} {
		info := sqliteTestIndex(kb, kb, kb, "tag", true)
		info.Content = "alpha document"
		saveSQLiteTestVector(t, r, info, []float32{1, 0})
	}
	require.NoError(t, r.DeleteByChunkIDList(ctx, []string{"remove"}, 0, ""))
	fts, err := r.keywordsRetrieve(ctx, types.RetrieveParams{Query: "alpha", TopK: 10})
	require.NoError(t, err)
	require.Len(t, fts[0].Results, 1)
	require.Equal(t, "keep", fts[0].Results[0].KnowledgeBaseID)
	vec, err := r.vectorRetrieve(ctx, types.RetrieveParams{Embedding: []float32{1, 0}, TopK: 10})
	require.NoError(t, err)
	require.Len(t, vec[0].Results, 1)
	require.Equal(t, "keep", vec[0].Results[0].KnowledgeBaseID)
}

func TestDeleteIndexedRowsRollsBackIndexResetOnMetadataFailure(t *testing.T) {
	r := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()
	info := sqliteTestIndex("chunk", "kb", "doc", "tag", true)
	info.Content = "alpha document"
	saveSQLiteTestVector(t, r, info, []float32{1, 0})
	require.NoError(t, r.db.Exec(`CREATE TRIGGER refuse_delete BEFORE DELETE ON lite_embeddings
		BEGIN SELECT RAISE(ABORT, 'fixture metadata failure'); END`).Error)
	require.ErrorContains(t, r.DeleteBySourceIDList(ctx, []string{"source-chunk"}, 0, ""), "fixture metadata failure")
	fts, err := r.keywordsRetrieve(ctx, types.RetrieveParams{Query: "alpha", TopK: 10})
	require.NoError(t, err)
	require.Len(t, fts[0].Results, 1)
	vec, err := r.vectorRetrieve(ctx, types.RetrieveParams{Embedding: []float32{1, 0}, TopK: 10})
	require.NoError(t, err)
	require.Len(t, vec[0].Results, 1)
}
