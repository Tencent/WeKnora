package memory

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMemoryConsistencyRealMessagePaging(t *testing.T) {
	s, db, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	// Only the actual SQL paging contract is under test here.
	require.NoError(t, db.Exec("CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, content TEXT, created_at DATETIME, deleted_at DATETIME)").Error)
	at := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 85; i++ {
		require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)", fmt.Sprintf("m%03d", i), "s", "user", "hello", at).Error)
	}
	require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role, content, created_at) VALUES (?, ?, ?, ?, ?)", "other", "unrelated", "user", "private", at).Error)
	s.messageRepo = repository.NewMessageRepository(db)
	var cursor types.MemoryMessageCursor
	seen := map[string]bool{}
	for {
		rows, err := s.messageRepo.ListMessagesBySessionAfterCursor(ctx, "s", cursor, 40)
		require.NoError(t, err)
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			require.False(t, seen[row.ID])
			require.Equal(t, "s", row.SessionID)
			seen[row.ID] = true
			cursor = types.MemoryMessageCursor{At: row.CreatedAt, ID: row.ID}
		}
	}
	require.Len(t, seen, 85)
}

func TestMemoryConsistencyReplacementRollsBackAsOneOperation(t *testing.T) {
	s, db, tr := newMemoryHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	old, err := s.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Topic: "数据库", Content: "使用 MySQL"})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_supersede BEFORE UPDATE OF status ON memory_items
 WHEN NEW.status = 'superseded' BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error)
	_, err = s.Remember(ctx, types.MemoryItem{Kind: types.MemoryKindFact, Topic: "数据库", Content: "已迁移到 PostgreSQL"})
	require.Error(t, err)
	items, total, err := s.ListItems(ctx, "", 20, 0)
	require.NoError(t, err)
	require.Equal(t, int64(1), total, "failed replacement must roll back the inserted row")
	require.Equal(t, old.ID, items[0].ID)
	require.Equal(t, types.MemoryStatusActive, items[0].Status)
}

func TestMemoryConsistencyLeaseRecoveryRetainsProgress(t *testing.T) {
	s, db, tr := newMemoryHarness(t)
	at := time.Now()
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	_, err := s.repo.EnsureSubject(ctx, scope)
	require.NoError(t, err)
	_, _, err = s.repo.EnqueuePendingSession(ctx, scope, "s", time.Minute)
	require.NoError(t, err)
	first, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "dead-worker", time.Minute)
	require.NoError(t, err)
	cursor := types.MemoryMessageCursor{At: at.Add(-time.Hour), ID: "completed"}
	require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, "dead-worker", first.Sessions[0], cursor, false))
	subject, err := s.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	subject.ExtractionState.LeaseUntil = time.Now().Add(-time.Minute)
	require.NoError(t, db.Model(subject).Update("extraction_state", subject.ExtractionState).Error)
	recovered, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "recovery", time.Minute)
	require.NoError(t, err)
	require.Len(t, recovered.Sessions, 1)
	require.True(t, recovered.Sessions[0].Cursor.At.Equal(cursor.At))
	require.Equal(t, cursor.ID, recovered.Sessions[0].Cursor.ID)
	require.ErrorIs(t, s.repo.FinishExtraction(ctx, scope, "dead-worker"), types.ErrMemoryExtractionLeaseLost)
	require.NoError(t, s.repo.CheckpointExtraction(ctx, scope, "recovery", recovered.Sessions[0], cursor, true))
}

func TestMemoryConsistencyRedeliveryWaitsForCrashedWorkerLease(t *testing.T) {
	s, tr, messages, models, queue := newExtractionHarness(t)
	ctx := enabledCtx(t, tr, 1, "alice")
	scope := scopeFor(t, ctx)
	models.response = `{"memories":[]}`
	messages.set("s", []*types.Message{userMessage("s", "must-survive-restart", time.Now().Add(-time.Hour))})
	s.ScheduleExtraction(ctx, "s", "m", "model")
	original := queue.pop()
	_, err := s.repo.ClaimPendingSessions(ctx, scope, "s", "crashed", time.Minute)
	require.NoError(t, err)
	require.NoError(t, s.Handle(ctx, original))
	require.Zero(t, models.callCount())
	retry := queue.pop()
	require.NotNil(t, retry, "a busy lease must defer redelivery rather than consume the only surviving task")
	require.Greater(t, queue.options[len(queue.options)-1].processIn, 50*time.Second)
	// Simulate the crashed worker's lease being released by recovery.
	require.NoError(t, s.repo.ReleaseExtractionSlot(ctx, scope, "crashed"))
	require.NoError(t, s.Handle(ctx, retry))
	require.Equal(t, 1, models.callCount())
}

func TestMemoryConsistencyMigrationRestoresLegacyPendingTarget(t *testing.T) {
	_, db, _ := newMemoryHarness(t)
	// Recreate just the legacy columns needed by the data migration, then run
	// the real migration against a premature replacement from the old code.
	require.NoError(t, db.Exec("DROP TABLE memory_items").Error)
	require.NoError(t, db.Exec("DROP TABLE memory_subjects").Error)
	require.NoError(t, db.Exec("CREATE TABLE memory_subjects (id TEXT PRIMARY KEY)").Error)
	require.NoError(t, db.Exec(`CREATE TABLE memory_items (
 id TEXT PRIMARY KEY, tenant_id INTEGER, subject_id TEXT, normalized_key TEXT,
 status TEXT, superseded_by TEXT, invalid_at DATETIME, valid_from DATETIME)`).Error)
	for _, row := range []struct{ id, key, status, by string }{
		{"old", "job", "superseded", "proposal"}, {"proposal", "job", "pending", ""},
		{"old2", "database", "superseded", "proposal2"}, {"proposal2", "database", "pending", ""},
		{"newer", "database", "active", ""},
	} {
		require.NoError(t, db.Exec("INSERT INTO memory_items VALUES (?, 1, 'alice', ?, ?, ?, NULL, ?)", row.id, row.key, row.status, row.by, time.Now()).Error)
	}
	up, err := os.ReadFile("../../../../migrations/sqlite/000015_memory_consistency.up.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(up)).Error)
	var old, proposal, untouched types.MemoryItem
	require.NoError(t, db.First(&old, "id = ?", "old").Error)
	require.NoError(t, db.First(&proposal, "id = ?", "proposal").Error)
	require.NoError(t, db.First(&untouched, "id = ?", "old2").Error)
	require.Equal(t, types.MemoryStatusActive, old.Status)
	require.Empty(t, old.SupersededBy)
	require.Equal(t, "old", proposal.ReplacesID)
	require.Equal(t, types.MemoryStatusSuperseded, untouched.Status, "do not override a newer active fact")
	var active int64
	require.NoError(t, db.Model(&types.MemoryItem{}).Where("normalized_key = 'database' AND status = 'active'").Count(&active).Error)
	require.Equal(t, int64(1), active)
	down, err := os.ReadFile("../../../../migrations/sqlite/000015_memory_consistency.down.sql")
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(down)).Error)
	require.False(t, db.Migrator().HasColumn(&types.MemoryItem{}, "replaces_id"))
}
