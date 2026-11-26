package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHousekeepingDefersRecoveryWhenHeartbeatReadFails(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	for _, state := range []string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing} {
		insertKnowledge(t, db, "active-"+state, state, stale)
		insertSpan(t, db, "active-"+state, 1, "span-"+state, types.SpanStatusRunning, time.Now())
	}
	insertKnowledge(t, db, "orphan", types.ParseStatusProcessing, stale)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	conn, err := sqlDB.Conn(context.Background())
	require.NoError(t, err)
	var denied atomic.Bool
	require.NoError(t, conn.Raw(func(driverConn any) error {
		driverConn.(*sqlite3.SQLiteConn).RegisterAuthorizer(func(op int, table, column, _ string) int {
			if op == sqlite3.SQLITE_READ && table == "knowledge_processing_spans" &&
				column == "updated_at" && denied.CompareAndSwap(false, true) {
				return sqlite3.SQLITE_DENY
			}
			return sqlite3.SQLITE_OK
		})
		return nil
	}))
	require.NoError(t, conn.Close())

	svc.runSweep(context.Background())
	require.True(t, denied.Load(), "the real SQLite heartbeat read must have failed")
	for _, state := range []string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing} {
		got, _ := readKnowledgeStatus(t, db, "active-"+state)
		assert.Equal(t, state, got, "a failed liveness read does not establish a stalled task")
	}
	got, _ := readKnowledgeStatus(t, db, "orphan")
	assert.Equal(t, types.ParseStatusProcessing, got, "defer the whole candidate batch until liveness is known")

	// The fault was one-shot. The next sweep protects active work and still
	// recovers a genuinely orphaned task without any manual reset.
	svc.runSweep(context.Background())
	for _, state := range []string{types.ParseStatusPending, types.ParseStatusProcessing, types.ParseStatusFinalizing} {
		got, _ := readKnowledgeStatus(t, db, "active-"+state)
		assert.Equal(t, state, got)
	}
	got, _ = readKnowledgeStatus(t, db, "orphan")
	assert.Equal(t, types.ParseStatusFailed, got)
}
