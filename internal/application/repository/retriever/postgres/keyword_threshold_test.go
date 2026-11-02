package postgres

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestKeywordsRetrieveThreshold(t *testing.T) {
	for _, scoreFunction := range []string{"paradedb.score", "pgsearch.score"} {
		t.Run(scoreFunction, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				scores    []float64
				threshold float64
				want      []float64
			}{
				{"zero keeps candidates", []float64{8, 4, 2}, 0, []float64{8, 4, 2}},
				{"negative disables filtering", []float64{8, 4, 2}, -1, []float64{8, 4, 2}},
				{"normalized cutoff includes equality", []float64{8, 4, 2}, 0.5, []float64{8, 4}},
				{"above boundary", []float64{8, 4, 2}, 0.50001, []float64{8}},
				{"one keeps best ties", []float64{8, 8, 4}, 1, []float64{8, 8}},
				{"above one rejects all", []float64{8, 4, 2}, 1.01, []float64{}},
				{"bounded scores are not inflated", []float64{0.8, 0.4, 0.2}, 0.5, []float64{0.8}},
				{"weak matches can all be rejected", []float64{0.8, 0.4}, 1, []float64{}},
				{"empty", nil, 0.5, []float64{}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					repo, mock := newKeywordTestRepository(t)
					mock.ExpectQuery(regexp.QuoteMeta(keywordScoreFunctionSQL)).
						WillReturnRows(sqlmock.NewRows([]string{"score_function"}).AddRow(scoreFunction))
					rows := sqlmock.NewRows([]string{"id", "chunk_id", "score"})
					for i, score := range tc.scores {
						rows.AddRow(i+1, fmt.Sprintf("chunk-%d", i), score)
					}
					mock.ExpectQuery(`SELECT `+regexp.QuoteMeta(scoreFunction)+`\(id\) as score.*FROM "embeddings".*content \|\|\|.*ORDER BY "score" DESC LIMIT`).
						WithArgs("kb-1", "少女", true, 10).WillReturnRows(rows)
					got, err := repo.Retrieve(context.Background(), types.RetrieveParams{
						Query: "少女", KnowledgeBaseIDs: []string{"kb-1"}, TopK: 10,
						RetrieverType: types.KeywordsRetrieverType, Threshold: tc.threshold,
					})
					require.NoError(t, err)
					require.Len(t, got, 1)
					scores := make([]float64, 0, len(got[0].Results))
					for _, hit := range got[0].Results {
						scores = append(scores, hit.Score)
						require.Equal(t, types.MatchTypeKeywords, hit.MatchType)
					}
					// Preserve raw BM25 for retrieval tracing and the existing fusion logic.
					require.Equal(t, tc.want, scores)
					require.NoError(t, mock.ExpectationsWereMet())
				})
			}
		})
	}
}
