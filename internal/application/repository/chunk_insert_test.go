package repository

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestCreateChunks_MySQLBoundsSequenceReadback(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}),
		&gorm.Config{SkipDefaultTransaction: true})
	require.NoError(t, err)

	chunks := make([]*types.Chunk, updateByIDsBatchSize+1)
	for i := range chunks {
		chunks[i] = makeChunk("kb", "document", types.ChunkTypeText)
	}
	mock.ExpectBegin()
	for start := 0; start < len(chunks); start += createChunksBatchSize {
		size := min(createChunksBatchSize, len(chunks)-start)
		mock.ExpectExec("INSERT INTO `chunks`").WillReturnResult(sqlmock.NewResult(1, int64(size)))
	}
	for start := 0; start < len(chunks); start += updateByIDsBatchSize {
		end := min(start+updateByIDsBatchSize, len(chunks))
		args := make([]driver.Value, end-start)
		rows := sqlmock.NewRows([]string{"id", "seq_id"})
		for i := start; i < end; i++ {
			args[i-start] = chunks[i].ID
			// Deliberately non-contiguous and unordered: readback must match
			// UUIDs, rather than infer sequences from LastInsertId or row order.
			j := end - 1 - (i - start)
			rows.AddRow(chunks[j].ID, int64(3*j+101))
		}
		mock.ExpectQuery("SELECT .*FROM `chunks` WHERE id IN").WithArgs(args...).WillReturnRows(rows)
	}
	mock.ExpectCommit()
	require.NoError(t, NewChunkRepository(db).CreateChunks(context.Background(), chunks))
	for i, chunk := range chunks {
		require.EqualValues(t, 3*i+101, chunk.SeqID)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

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
