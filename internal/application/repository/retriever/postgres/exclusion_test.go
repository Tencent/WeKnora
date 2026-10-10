package postgres

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestRetrieveExcludesKnowledgeBeforeTopK(t *testing.T) {
	for _, retrieverType := range []types.RetrieverType{types.VectorRetrieverType, types.KeywordsRetrieverType} {
		for _, exclude := range []bool{false, true} {
			name := string(retrieverType) + "/no-exclusion"
			if exclude {
				name = string(retrieverType) + "/exclusion"
			}
			t.Run(name, func(t *testing.T) {
				matcher := sqlmock.QueryMatcherFunc(func(expected, actual string) error {
					if expected != "retrieve" {
						return sqlmock.QueryMatcherEqual.Match(expected, actual)
					}
					// Both retrieval paths must retain inclusion constraints and apply
					// exclusions within the candidate query, before its first LIMIT.
					query := strings.ReplaceAll(actual, `"`, "")
					assert.Contains(t, query, "knowledge_base_id IN (")
					assert.Contains(t, query, "knowledge_id IN (")
					assert.Contains(t, query, "tag_id IN (")
					assert.NotContains(t, query, "excluded'knowledge", "IDs must remain bound parameters")
					if exclude {
						index := strings.Index(query, "knowledge_id NOT IN (")
						assert.GreaterOrEqual(t, index, 0)
						assert.Less(t, index, strings.Index(query, "LIMIT"))
					} else {
						assert.NotContains(t, query, "NOT IN")
					}
					return nil
				})
				sqlDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
				require.NoError(t, err)
				t.Cleanup(func() { _ = sqlDB.Close() })
				db, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: sqlDB}), &gorm.Config{})
				require.NoError(t, err)
				params := types.RetrieveParams{
					Query: "searchterm", Embedding: []float32{1, 0}, TopK: 1,
					KnowledgeBaseIDs: []string{"kb", "kb2"}, KnowledgeIDs: []string{"target", "excluded'knowledge"},
					TagIDs: []string{"tag", "tag2"}, RetrieverType: retrieverType,
				}
				var args []driver.Value
				if retrieverType == types.VectorRetrieverType {
					mock.ExpectBegin()
					mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnResult(sqlmock.NewResult(0, 0))
					mock.ExpectExec("SET LOCAL hnsw.iterative_scan = strict_order").
						WillReturnResult(sqlmock.NewResult(0, 0))
					args = append(args, "[1,0]", 2)
				}
				args = append(args, "kb", "kb2", "target", "excluded'knowledge")
				if exclude {
					params.ExcludeKnowledgeIDs = []string{"excluded'knowledge", "absent"}
					args = append(args, "excluded'knowledge", "absent")
				}
				args = append(args, "tag", "tag2")
				if retrieverType == types.VectorRetrieverType {
					args = append(args, true, 100, 1.0, 1)
				} else {
					args = append(args, "searchterm", true, 1)
				}
				mock.ExpectQuery("retrieve").WithArgs(args...).
					WillReturnRows(sqlmock.NewRows([]string{"id", "knowledge_id"}))
				if retrieverType == types.VectorRetrieverType {
					mock.ExpectCommit()
				}
				_, err = (&pgRepository{db: db}).Retrieve(context.Background(), params)
				require.NoError(t, err)
				require.NoError(t, mock.ExpectationsWereMet())
			})
		}
	}
}
