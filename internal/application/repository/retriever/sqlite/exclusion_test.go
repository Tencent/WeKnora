package sqlite

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRetrieveExcludesKnowledgeBeforeTopK(t *testing.T) {
	for _, retrieverType := range []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType} {
		t.Run(string(retrieverType), func(t *testing.T) {
			repository := newSQLiteRetrieverTestRepository(t)
			if retrieverType == types.KeywordsRetrieverType &&
				!repository.db.Migrator().HasTable("lite_embeddings_fts") {
				t.Skip("FTS5 requires sqlite_fts5 build tag")
			}
			for _, fixture := range []struct {
				chunk, kb, knowledge, tag, content string
				embedding                          []float32
			}{
				{"excluded", "kb", "excluded'knowledge", "tag", "searchterm", []float32{1, 0}},
				{"other-kb", "other", "target", "tag", "searchterm", []float32{1, 0}},
				{"other-knowledge", "kb", "other", "tag", "searchterm", []float32{1, 0}},
				{"other-tag", "kb", "target", "other", "searchterm", []float32{1, 0}},
				{"target", "kb", "target", "tag", "searchterm additional words", []float32{0.8, 0.6}},
			} {
				info := sqliteTestIndex(fixture.chunk, fixture.kb, fixture.knowledge, fixture.tag, true)
				info.Content = fixture.content
				saveSQLiteTestVector(t, repository, info, fixture.embedding)
			}
			params := types.RetrieveParams{
				Query: "searchterm", Embedding: []float32{1, 0}, TopK: 1,
				KnowledgeBaseIDs: []string{"kb"}, KnowledgeIDs: []string{"target", "excluded'knowledge"},
				ExcludeKnowledgeIDs: []string{"excluded'knowledge", "absent"}, TagIDs: []string{"tag"},
				RetrieverType: retrieverType,
			}
			results, err := repository.Retrieve(context.Background(), params)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Len(t, results[0].Results, 1)
			assert.Equal(t, "target", results[0].Results[0].ChunkID)

			// An empty exclusion must preserve the original ranking.
			params.ExcludeKnowledgeIDs = nil
			results, err = repository.Retrieve(context.Background(), params)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Len(t, results[0].Results, 1)
			assert.Equal(t, "excluded", results[0].Results[0].ChunkID)

			// Explicit exclusion wins over explicit inclusion.
			params.ExcludeKnowledgeIDs = params.KnowledgeIDs
			results, err = repository.Retrieve(context.Background(), params)
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Empty(t, results[0].Results)
		})
	}
}
