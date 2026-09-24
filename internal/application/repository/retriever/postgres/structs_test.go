package postgres

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// capturedInsert is one INSERT that GORM built for the embeddings table.
type capturedInsert struct {
	sql  string
	vars []any
}

// dryRunDB builds the SQL GORM would send to PostgreSQL without a server, and
// records every INSERT so a test can check which values reach the database.
func dryRunDB(t *testing.T) (*gorm.DB, *[]capturedInsert) {
	t.Helper()
	db, err := gorm.Open(postgres.New(postgres.Config{DSN: "host=localhost dbname=unused"}), &gorm.Config{
		DryRun:                 true,
		DisableAutomaticPing:   true,
		SkipDefaultTransaction: true,
	})
	require.NoError(t, err)

	var inserts []capturedInsert
	require.NoError(t, db.Callback().Create().After("gorm:create").Register("test:capture",
		func(tx *gorm.DB) {
			inserts = append(inserts, capturedInsert{sql: tx.Statement.SQL.String(), vars: tx.Statement.Vars})
		}))
	return db, &inserts
}

// isEnabledValues returns the is_enabled value bound for each inserted row,
// failing the test when the column is missing from the INSERT.
func isEnabledValues(t *testing.T, insert capturedInsert) []any {
	t.Helper()
	open := strings.Index(insert.sql, "(")
	closing := strings.Index(insert.sql, ")")
	require.True(t, open >= 0 && closing > open, "unexpected INSERT: %s", insert.sql)
	columns := strings.Split(insert.sql[open+1:closing], ",")
	col := -1
	for i, c := range columns {
		if strings.Trim(strings.TrimSpace(c), `"`) == "is_enabled" {
			col = i
		}
	}
	require.GreaterOrEqual(t, col, 0, "is_enabled is not written by: %s", insert.sql)

	var values []any
	for row := 0; row*len(columns) < len(insert.vars); row++ {
		v := insert.vars[row*len(columns)+col]
		if p, ok := v.(*bool); ok && p != nil {
			v = *p
		}
		values = append(values, v)
	}
	return values
}

// GORM leaves a zero value out of the INSERT when the column has a default,
// so a disabled index written through a plain bool was stored as enabled.
func TestBatchSaveWritesDisabledState(t *testing.T) {
	db, inserts := dryRunDB(t)
	repo := NewPostgresRetrieveEngineRepository(db)

	infos := []*types.IndexInfo{
		{SourceID: "c1", ChunkID: "c1", Content: "enabled", IsEnabled: true},
		{SourceID: "c2", ChunkID: "c2", Content: "disabled", IsEnabled: false},
	}
	require.NoError(t, repo.BatchSave(context.Background(), infos, nil))

	require.Len(t, *inserts, 1)
	require.Equal(t, []any{true, false}, isEnabledValues(t, (*inserts)[0]))
}

func TestSaveWritesDisabledState(t *testing.T) {
	db, inserts := dryRunDB(t)
	repo := NewPostgresRetrieveEngineRepository(db)

	info := &types.IndexInfo{SourceID: "c1", ChunkID: "c1", Content: "disabled", IsEnabled: false}
	require.NoError(t, repo.Save(context.Background(), info, nil))

	require.Len(t, *inserts, 1)
	require.Equal(t, []any{false}, isEnabledValues(t, (*inserts)[0]))
}

// The chunk_enabled override (used by index copies) must reach the INSERT too.
func TestBatchSaveWritesChunkEnabledOverride(t *testing.T) {
	db, inserts := dryRunDB(t)
	repo := NewPostgresRetrieveEngineRepository(db)

	infos := []*types.IndexInfo{{SourceID: "c1", ChunkID: "c1", Content: "x", IsEnabled: true}}
	params := map[string]any{"chunk_enabled": map[string]bool{"c1": false}}
	require.NoError(t, repo.BatchSave(context.Background(), infos, params))

	require.Len(t, *inserts, 1)
	require.Equal(t, []any{false}, isEnabledValues(t, (*inserts)[0]))
}
