package repository

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCreateChunks_PostgresPreservesEnabledStateAndReturnsSeqIDs(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	require.NoError(t, err)

	chunks := []*types.Chunk{
		makeChunk("kb", "faq", types.ChunkTypeFAQ),
		makeChunk("kb", "faq", types.ChunkTypeFAQ),
	}
	chunks[1].IsEnabled = false
	// Check the bound values for both rows, not only the caller's structs.
	stmt := &gorm.Statement{DB: db}
	require.NoError(t, stmt.Parse(&types.Chunk{}))
	var args []driver.Value
	for _, chunk := range chunks {
		for _, name := range stmt.Schema.DBNames {
			if name == "seq_id" {
				continue // PostgreSQL generates these and returns them to the embedded chunks.
			}
			if name == "is_enabled" {
				args = append(args, chunk.IsEnabled)
			} else {
				args = append(args, sqlmock.AnyArg())
			}
		}
	}
	mock.ExpectBegin()
	mock.ExpectQuery(`INSERT INTO "chunks" .*RETURNING "seq_id"`).WithArgs(args...).
		WillReturnRows(sqlmock.NewRows([]string{"seq_id"}).AddRow(101).AddRow(102))
	mock.ExpectCommit()

	require.NoError(t, NewChunkRepository(db).CreateChunks(context.Background(), chunks))
	require.EqualValues(t, 101, chunks[0].SeqID)
	require.EqualValues(t, 102, chunks[1].SeqID)
	require.True(t, chunks[0].IsEnabled)
	require.False(t, chunks[1].IsEnabled)
	require.False(t, chunks[0].CreatedAt.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}
