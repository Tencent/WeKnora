package postgres

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	pg "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGalleryChunkFilterPrecedesVectorCandidateLimit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	gormDB, err := gorm.Open(pg.New(pg.Config{Conn: db}), &gorm.Config{})
	require.NoError(t, err)
	mock.ExpectBegin()
	mock.ExpectExec("SET LOCAL hnsw.ef_search = 100").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET LOCAL hnsw.iterative_scan = strict_order").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`(?s)FROM embeddings WHERE .*chunk_id IN \(\$4\).*ORDER BY .* LIMIT \$6`).
		WithArgs(sqlmock.AnyArg(), 2, "kb", "image-only", true, 100, 1.0, 1).
		WillReturnRows(sqlmock.NewRows([]string{"chunk_id", "knowledge_base_id", "score"}).
			AddRow("image-only", "kb", 0.8))
	mock.ExpectCommit()
	repo := &pgRepository{db: gormDB}
	result, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{
		Embedding: []float32{1, 0}, KnowledgeBaseIDs: []string{"kb"}, ChunkIDs: []string{"image-only"}, TopK: 1,
	})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "image-only", result[0].Results[0].ChunkID)
	require.NoError(t, mock.ExpectationsWereMet())
}
