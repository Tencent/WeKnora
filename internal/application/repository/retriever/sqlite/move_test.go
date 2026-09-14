package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMoveIndicesPreservesVectorsAndExactScopeAcrossRetry(t *testing.T) {
	repo := newSQLiteRetrieverTestRepository(t)
	ctx := context.Background()
	for _, row := range []struct{ chunk, kb, doc string }{
		{
			"text",
			"source",
			"doc",
		},
		{
			"image",
			"source",
			"doc",
		},
		{
			"other",
			"source",
			"other-doc",
		},
		{
			"foreign",
			"third",
			"doc",
		},
	} {
		saveSQLiteTestVector(t, repo, sqliteTestIndex(row.chunk, row.kb, row.doc, "old-tag", true), []float32{1, 0})
	}
	var before []sqliteEmbedding
	require.NoError(t, repo.db.Order("id").Find(&before).Error)
	for range 2 {
		require.NoError(
			t,
			repo.MoveKnowledgeIndices(ctx, "source", "target", "doc", []string{"text", "image"}, 2, "document"),
		)
	}
	var after []sqliteEmbedding
	require.NoError(t, repo.db.Order("id").Find(&after).Error)
	require.Len(t, after, len(before))
	for i, row := range after {
		require.Equal(t, before[i].ID, row.ID)
		require.Equal(t, before[i].SourceID, row.SourceID)
		if row.KnowledgeID == "doc" && before[i].KnowledgeBaseID == "source" {
			require.Equal(t, "target", row.KnowledgeBaseID)
			require.Empty(t, row.TagID)
		} else {
			require.Equal(t, before[i].KnowledgeBaseID, row.KnowledgeBaseID)
			require.Equal(t, "old-tag", row.TagID)
		}
	}
	results, err := repo.vectorRetrieve(
		ctx,
		types.RetrieveParams{
			Embedding:        []float32{1, 0},
			TopK:             10,
			KnowledgeBaseIDs: []string{"target"},
			RetrieverType:    types.VectorRetrieverType,
		},
	)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, results[0].Results, 2, "target vectors remain searchable after an identity-preserving move")
}
