package tools

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"

	sqlite3 "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

var registerGrepChunksRecallWeightSQLite sync.Once
var grepChunksRecallWeightDatabaseID atomic.Uint64

func TestGrepChunksRanksMatchingChunksByRecallWeightExactlyOnce(t *testing.T) {
	const driverName = "grep_chunks_recall_weight_test"
	registerGrepChunksRecallWeightSQLite.Do(func() {
		sql.Register(driverName, &sqlite3.SQLiteDriver{
			ConnectHook: func(conn *sqlite3.SQLiteConn) error {
				return conn.RegisterFunc("regexp", func(pattern, value string) (bool, error) {
					return regexp.MatchString("(?i)"+pattern, value)
				}, true)
			},
		})
	})

	db, err := gorm.Open(
		sqlite.Dialector{
			DriverName: driverName,
			DSN: fmt.Sprintf(
				"file:%s-%d?mode=memory&cache=shared",
				t.Name(),
				grepChunksRecallWeightDatabaseID.Add(1),
			),
		},
		&gorm.Config{},
	)
	require.NoError(t, err)

	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL,
			deleted_at DATETIME
		)
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE chunks (
			id TEXT PRIMARY KEY,
			content TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			knowledge_id TEXT NOT NULL,
			knowledge_base_id TEXT NOT NULL,
			tenant_id INTEGER NOT NULL,
			chunk_type TEXT NOT NULL,
			metadata TEXT,
			created_at DATETIME NOT NULL,
			is_enabled BOOLEAN NOT NULL,
			deleted_at DATETIME,
			recall_weight REAL NOT NULL DEFAULT 1.0
		)
	`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, title) VALUES (?, ?)",
		"knowledge-1", "Recall weight test",
	).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO chunks (
			id, content, chunk_index, knowledge_id, knowledge_base_id,
			tenant_id, chunk_type, metadata, created_at, is_enabled, recall_weight
		) VALUES
			('penalized', 'needle penalized candidate', 0, 'knowledge-1', 'kb-1', 7, 'text', '{}', CURRENT_TIMESTAMP, 1, 0.5),
			('boosted', 'needle boosted candidate', 1, 'knowledge-1', 'kb-1', 7, 'text', '{}', CURRENT_TIMESTAMP, 1, 2.0)
	`).Error)

	tool := NewGrepChunksTool(db, types.SearchTargets{{
		Type:            types.SearchTargetTypeKnowledgeBase,
		KnowledgeBaseID: "kb-1",
		TenantID:        7,
	}})
	args := json.RawMessage(`{"query":"needle"}`)

	assertWeightedOrder := func() {
		result, executeErr := tool.Execute(context.Background(), args)
		require.NoError(t, executeErr)
		require.True(t, result.Success)

		chunks, ok := result.Data["chunk_results"].([]grepChunkResult)
		require.True(t, ok)
		require.Len(t, chunks, 2)
		require.Equal(t, "boosted", chunks[0].ChunkID)
		require.InDelta(t, 2.0, chunks[0].Score, 0.000001)
		require.Equal(t, "penalized", chunks[1].ChunkID)
		require.InDelta(t, 0.5, chunks[1].Score, 0.000001)

		require.Len(t, result.KnowledgeReferences, 2)
		require.Equal(t, "boosted", result.KnowledgeReferences[0].ID)
		require.Equal(t, "kb-1", result.KnowledgeReferences[0].KnowledgeBaseID)
		require.Equal(t, types.MatchTypeKeywords, result.KnowledgeReferences[0].MatchType)
		require.InDelta(t, 2.0, result.KnowledgeReferences[0].Score, 0.000001)
		require.Equal(t, "penalized", result.KnowledgeReferences[1].ID)
	}

	assertWeightedOrder()
	assertWeightedOrder()
}
