package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func documentDB(t *testing.T) (*gorm.DB, *taskPendingOpsRepository) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "queue.db") + "?_busy_timeout=5000&_journal_mode=WAL"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	for _, ddl := range []string{
		taskPendingOpsTestDDL, taskDeadLettersTestDDL, taskQueueKnowledgeBaseTestDDL, taskQueueKnowledgeTestDDL,
		`CREATE TABLE tenants (id INTEGER PRIMARY KEY, deleted_at DATETIME)`,
		`CREATE TABLE knowledge_processing_spans (knowledge_id TEXT, attempt INTEGER)`,
		`ALTER TABLE task_pending_ops ADD COLUMN available_at DATETIME`,
		`ALTER TABLE task_pending_ops ADD COLUMN generation VARCHAR(36) NOT NULL DEFAULT ''`,
		`ALTER TABLE task_dead_letters ADD COLUMN generation VARCHAR(36) NOT NULL DEFAULT ''`,
		`CREATE TABLE document_generations (tenant_id BIGINT NOT NULL, knowledge_id TEXT NOT NULL,
 scope_id TEXT NOT NULL, token TEXT NOT NULL, advanced BOOLEAN NOT NULL DEFAULT FALSE,
 PRIMARY KEY(tenant_id,knowledge_id))`,
		`CREATE TABLE document_scheduler_state (id INTEGER PRIMARY KEY, last_tenant_id BIGINT NOT NULL DEFAULT 0)`,
		`INSERT INTO document_scheduler_state(id) VALUES(1)`,
		`CREATE TABLE document_execution_leases (
 tenant_id BIGINT NOT NULL, knowledge_id TEXT NOT NULL, pending_id BIGINT NOT NULL,
 owner TEXT NOT NULL, expires_at DATETIME NOT NULL, PRIMARY KEY(tenant_id,knowledge_id))`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return db, &taskPendingOpsRepository{db: db}
}

func documentSeed(
	t *testing.T, db *gorm.DB, r *taskPendingOpsRepository, tenant uint64, kb, id string, attempt int,
) *types.TaskPendingOp {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO tenants(id) VALUES(?) ON CONFLICT DO NOTHING`, tenant).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id,tenant_id) VALUES(?,?) ON CONFLICT DO NOTHING`,
		kb, tenant).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledges(id,tenant_id,knowledge_base_id,parse_status)
 VALUES(?,?,?,'pending') ON CONFLICT DO NOTHING`, id, tenant, kb).Error)
	payload, _ := json.Marshal(types.DocumentProcessPayload{
		TenantID: tenant, KnowledgeID: id, KnowledgeBaseID: kb, Attempt: attempt,
	})
	op := &types.TaskPendingOp{
		TenantID: tenant, TaskType: types.TypeDocumentProcess, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: kb, DedupKey: id, Op: "process", Payload: payload,
	}
	ok, err := r.EnqueueDocument(context.Background(), op)
	require.NoError(t, err)
	require.True(t, ok)
	return op
}

func TestDocumentTenantRotationAndIndependentCap(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		documentSeed(t, db, r, 1, fmt.Sprintf("a-kb-%d", i), fmt.Sprintf("a-%d", i), 1)
	}
	for i := 0; i < 4; i++ {
		documentSeed(t, db, r, 2, "b-kb", fmt.Sprintf("b-%d", i), 1)
	}
	second := &taskPendingOpsRepository{db: db}
	for _, tenant := range []uint64{1, 2, 1, 2} {
		claim, err := second.ClaimDocument(ctx, 2, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, claim)
		require.Equal(t, tenant, claim.Op.TenantID)
	}
	claim, err := r.ClaimDocument(ctx, 2, time.Minute)
	require.NoError(t, err)
	require.Nil(t, claim)
}

func TestDocumentCancellationKeepsLeaseAndOwnerFence(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	documentSeed(t, db, r, 1, "kb", "old", 1)
	c, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NoError(t, r.DeleteByIDs(ctx, []int64{c.Op.ID}))
	newer := documentSeed(t, db, r, 1, "kb", "new", 1)
	blocked, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Nil(t, blocked)
	wrong := *c
	wrong.Owner = "wrong"
	require.False(t, finishDocumentTest(ctx, t, r, &wrong, "success", time.Time{}, ""))
	blocked, err = r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, finishDocumentTest(ctx, t, r, c, "success", time.Time{}, ""))
	next, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Equal(t, newer.ID, next.Op.ID)
	ok, err := r.RenewDocument(ctx, &wrong, time.Minute)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestDocumentRetryBudgetAndDuplicate(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	first := documentSeed(t, db, r, 1, "kb", "doc", 5)
	for n := 0; n < 4; n++ {
		duplicate := documentSeed(t, db, r, 1, "kb", "doc", 5)
		require.Equal(t, first.ID, duplicate.ID)
		c, err := r.ClaimDocument(ctx, 2, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, c)
		require.Equal(t, n, c.Op.FailCount)
		due := time.Now().Add(time.Hour)
		finishDocumentTest(ctx, t, r, c, "retry", due, "parse failed")
		no, err := r.ClaimDocument(ctx, 2, time.Minute)
		require.NoError(t, err)
		require.Nil(t, no)
		require.NoError(t, db.Exec(`UPDATE task_pending_ops SET available_at=NULL`).Error)
	}
	var letters []types.TaskDeadLetter
	require.NoError(t, db.Find(&letters).Error)
	require.Len(t, letters, 1)
	require.Equal(t, 4, letters[0].FailCount)
	require.JSONEq(t, string(first.Payload), string(letters[0].Payload))
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDocumentConcurrentClaimers(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		documentSeed(t, db, r, 1, "kb", fmt.Sprint(i), 1)
	}
	sqlDB, _ := db.DB()
	var name string
	require.NoError(t, sqlDB.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&name))
	other, err := gorm.Open(sqlite.Open(name+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	otherSQL, err := other.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, otherSQL.Close()) })
	rs := []*taskPendingOpsRepository{r, {db: other}}
	var wg sync.WaitGroup
	claims := make(chan *types.DocumentClaim, 12)
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, e := rs[i%2].ClaimDocument(ctx, 2, time.Minute)
			if e != nil {
				errs <- e
			}
			if c != nil {
				claims <- c
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	close(claims)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, claims, 2)
	seen := map[int64]bool{}
	for c := range claims {
		require.False(t, seen[c.Op.ID])
		seen[c.Op.ID] = true
	}
}

func TestDocumentStaleRecoveryAndNewAttemptFence(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	documentSeed(t, db, r, 1, "kb", "doc", 1)
	old, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	newer := documentSeed(t, db, r, 1, "kb", "doc", 2)
	require.NotEqual(t, old.Op.ID, newer.ID)
	finishDocumentTest(ctx, t, r, old, "retry", time.Time{}, "old failure")
	c, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Equal(t, newer.ID, c.Op.ID)
	require.Zero(t, c.Op.FailCount)
	require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=?`, time.Now().Add(-time.Hour)).Error)
	require.NoError(t, r.RecoverDocuments(ctx))
	require.NoError(t, db.Exec(`UPDATE task_pending_ops SET available_at=NULL`).Error)
	next, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, next.Op.FailCount)
	finishDocumentTest(ctx, t, r, c, "success", time.Time{}, "")
	live, err := r.RenewDocument(ctx, next, time.Minute)
	require.NoError(t, err)
	require.True(t, live)
}

func TestDocumentReconcileDeletedScopeAndFinalizing(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	documentSeed(t, db, r, 1, "deleted", "d1", 1)
	documentSeed(t, db, r, 1, "live", "d2", 1)
	require.NoError(t, db.Exec(`UPDATE knowledge_bases SET deleted_at=? WHERE id='deleted'`, time.Now()).Error)
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status='finalizing' WHERE id='d2'`).Error)
	require.NoError(t, r.RecoverDocuments(ctx))
	tenants, err := r.DocumentTenants(ctx)
	require.NoError(t, err)
	require.Empty(t, tenants)
	c, err := r.ClaimDocument(ctx, 2, time.Minute)
	require.NoError(t, err)
	require.Nil(t, c)
}

func TestDocumentInfrastructureReleaseAndTerminalFailure(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	documentSeed(t, db, r, 1, "kb", "doc", 1)
	c, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	finishDocumentTest(ctx, t, r, c, "release", time.Time{}, "database unavailable")
	c, err = r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Zero(t, c.Op.FailCount)
	finishDocumentTest(ctx, t, r, c, "terminal", time.Time{}, "not retryable")
	var dl types.TaskDeadLetter
	require.NoError(t, db.First(&dl).Error)
	require.Equal(t, "not retryable", dl.LastError)
	require.Equal(t, 1, dl.FailCount)
	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id='doc'`).Scan(&status).Error)
	require.Equal(t, types.ParseStatusFailed, status)
}

func TestDocumentLegacyRetryCountAndTerminalReplay(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	op := documentSeed(t, db, r, 1, "kb", "doc", 1)
	require.NoError(t, r.DeleteByIDs(ctx, []int64{op.ID}))
	op.ID = 0
	op.FailCount = 3
	ok, err := r.EnqueueDocument(ctx, op)
	require.NoError(t, err)
	require.True(t, ok)
	c, err := r.ClaimDocument(ctx, 2, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 3, c.Op.FailCount)
	finishDocumentTest(ctx, t, r, c, "retry", time.Time{}, "exhausted")
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status='pending' WHERE id='doc'`).Error)
	op.ID = 0
	op.FailCount = 0
	ok, err = r.EnqueueDocument(ctx, op)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestDocumentReconcilePreservesLiveFinalizingHandoff(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	documentSeed(t, db, r, 1, "kb", "doc", 1)
	c, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status='finalizing' WHERE id='doc'`).Error)
	require.NoError(t, r.RecoverDocuments(ctx))
	live, err := r.RenewDocument(ctx, c, time.Minute)
	require.NoError(t, err)
	require.True(t, live)
	require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=?`, time.Now().Add(-time.Hour)).Error)
	require.NoError(t, r.RecoverDocuments(ctx))
	ids, err := r.DocumentKnowledgeIDs(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)
	var count int64
	require.NoError(t, db.Model(&types.TaskDeadLetter{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDocumentAbandonedExecutionBudget(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	op := documentSeed(t, db, r, 1, "kb", "doc", 7)
	for attempt := 0; attempt < 4; attempt++ {
		claim, err := r.ClaimDocument(ctx, 1, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, claim)
		require.Equal(t, attempt, claim.Op.FailCount)
		require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=?`,
			time.Now().Add(-time.Minute)).Error)
		require.NoError(t, r.RecoverDocuments(ctx))
		require.NoError(t, db.Exec(`UPDATE task_pending_ops SET available_at=NULL`).Error)
	}
	claim, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Nil(t, claim)
	var letters []types.TaskDeadLetter
	require.NoError(t, db.Find(&letters).Error)
	require.Len(t, letters, 1)
	require.Equal(t, 4, letters[0].FailCount)
	require.JSONEq(t, string(op.Payload), string(letters[0].Payload))
}

func TestDocumentSQLiteMigrationRoundTrip(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	for _, direction := range []string{"up", "down"} {
		path := filepath.Join("..", "..", "..", "migrations", "sqlite",
			"000053_document_tenant_scheduler."+direction+".sql")
		migration, err := os.ReadFile(path)
		require.NoError(t, err)
		require.NoError(t, db.Exec(string(migration)).Error)
		if direction == "up" {
			var count int64
			require.NoError(t, db.Table("document_scheduler_state").Count(&count).Error)
			require.EqualValues(t, 1, count)
			require.True(t, db.Migrator().HasColumn("task_pending_ops", "available_at"))
			require.True(t, db.Migrator().HasTable("document_generations"))
			require.True(t, db.Migrator().HasColumn("task_pending_ops", "generation"))
			require.True(t, db.Migrator().HasColumn("task_dead_letters", "generation"))
		} else {
			require.False(t, db.Migrator().HasTable("document_execution_leases"))
			require.False(t, db.Migrator().HasColumn("task_pending_ops", "available_at"))
			require.False(t, db.Migrator().HasTable("document_generations"))
			require.False(t, db.Migrator().HasColumn("task_pending_ops", "generation"))
			require.False(t, db.Migrator().HasColumn("task_dead_letters", "generation"))
		}
	}
}

func finishDocumentTest(ctx context.Context, t *testing.T, r *taskPendingOpsRepository, claim *types.DocumentClaim,
	outcome string, next time.Time, message string,
) bool {
	t.Helper()
	settled, err := r.FinishDocument(ctx, claim, outcome, next, message)
	require.NoError(t, err)
	return settled
}

// TestDocumentPostgresAdmission uses only an explicitly supplied test database.
// Every run creates and drops its own schema, including the actual migration.
func TestDocumentPostgresAdmission(t *testing.T) {
	dsn := os.Getenv("DOCUMENT_SCHEDULER_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DOCUMENT_SCHEDULER_POSTGRES_DSN is not set")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	schema := fmt.Sprintf("document_scheduler_test_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() {
		require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
		require.NoError(t, adminSQL.Close())
	})
	scoped := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		parsed, err := url.Parse(dsn)
		require.NoError(t, err)
		q := parsed.Query()
		q.Set("search_path", schema)
		parsed.RawQuery = q.Encode()
		scoped = parsed.String()
	}
	db, err := gorm.Open(postgres.Open(scoped), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	for _, ddl := range []string{
		taskPendingOpsTestDDL, taskDeadLettersTestDDL, taskQueueKnowledgeBaseTestDDL, taskQueueKnowledgeTestDDL,
		`CREATE TABLE tenants(id BIGINT PRIMARY KEY, deleted_at TIMESTAMPTZ)`,
		`CREATE TABLE knowledge_processing_spans (knowledge_id TEXT, attempt INTEGER)`,
	} {
		ddl = strings.ReplaceAll(ddl, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
		ddl = strings.ReplaceAll(ddl, "DATETIME", "TIMESTAMPTZ")
		ddl = strings.ReplaceAll(ddl, "payload     TEXT", "payload     JSONB")
		require.NoError(t, db.Exec(ddl).Error)
	}
	migrationDir := filepath.Join("..", "..", "..", "migrations", "versioned")
	migration, err := os.ReadFile(filepath.Join(migrationDir, "000137_document_tenant_scheduler.up.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	other, err := gorm.Open(postgres.Open(scoped), &gorm.Config{})
	require.NoError(t, err)
	otherSQL, err := other.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, otherSQL.Close()) })
	r := &taskPendingOpsRepository{db: db}
	for i := 0; i < 12; i++ {
		documentSeed(t, db, r, 1, fmt.Sprintf("a-kb-%d", i), fmt.Sprintf("a-%d", i), 1)
	}
	for i := 0; i < 4; i++ {
		documentSeed(t, db, r, 2, "b-kb", fmt.Sprintf("b-%d", i), 1)
	}
	rs := []*taskPendingOpsRepository{r, {db: other}}
	ctx := context.Background()
	claims := make(chan *types.DocumentClaim, 12)
	errs := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claim, err := rs[i%2].ClaimDocument(ctx, 2, time.Minute)
			if err != nil {
				errs <- err
			}
			if claim != nil {
				claims <- claim
			}
		}(i)
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Len(t, claims, 4)
	counts := map[uint64]int{}
	seen := map[int64]bool{}
	for claim := range claims {
		counts[claim.Op.TenantID]++
		require.False(t, seen[claim.Op.ID])
		seen[claim.Op.ID] = true
		require.True(t, finishDocumentTest(ctx, t, r, claim, "success", time.Time{}, ""))
	}
	require.Equal(t, map[uint64]int{1: 2, 2: 2}, counts)
	require.NoError(t, db.Exec(`UPDATE document_scheduler_state SET last_tenant_id=0 WHERE id=1`).Error)
	for _, tenant := range []uint64{1, 2, 1, 2} {
		claim, err := r.ClaimDocument(ctx, 2, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, claim)
		require.Equal(t, tenant, claim.Op.TenantID)
	}
	for _, attempt := range []int{1} {
		for _, recovery := range []bool{false, true} {
			name := fmt.Sprintf("attempt=%d/recovery=%t", attempt, recovery)
			t.Run(name, func(t *testing.T) {
				tenantID := uint64(10 + attempt*2)
				if recovery {
					tenantID++
				}
				id := fmt.Sprintf("fence-%d", tenantID)
				documentSeed(t, db, r, tenantID, id, id, attempt)
				require.NoError(t, db.Exec(`UPDATE task_pending_ops SET fail_count=3 WHERE dedup_key=?`, id).Error)
				claim, err := r.ClaimDocument(ctx, 2, time.Minute)
				require.NoError(t, err)
				require.NotNil(t, claim)
				require.Equal(t, tenantID, claim.Op.TenantID)
				require.NoError(t, db.Exec(
					`INSERT INTO knowledge_processing_spans(knowledge_id,attempt) VALUES(?,2)`, id).Error)
				if recovery {
					require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=? WHERE tenant_id=?`,
						time.Now().Add(-time.Minute), tenantID).Error)
					failures, err := r.RecoverDocumentFailures(ctx)
					require.NoError(t, err)
					require.Len(t, failures, 1)
					require.Equal(t, id, failures[0].DedupKey)
				} else {
					require.True(t, finishDocumentTest(ctx, t, r, claim, "terminal", time.Time{}, "old attempt"))
				}
				var status string
				require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id=?`, id).Scan(&status).Error)
				require.Equal(t, types.ParseStatusPending, status)
				replacement := documentSeed(t, db, r, tenantID, id, id, 2)
				next, err := r.ClaimDocument(ctx, 2, time.Minute)
				require.NoError(t, err)
				require.NotNil(t, next)
				require.Equal(t, replacement.ID, next.Op.ID)
			})
		}
	}
	migration, err = os.ReadFile(filepath.Join(migrationDir, "000137_document_tenant_scheduler.down.sql"))
	require.NoError(t, err)
	require.NoError(t, db.Exec(string(migration)).Error)
	require.False(t, db.Migrator().HasTable("document_execution_leases"))
}

func TestDocumentRecoveryReturnsOnlyNewOwnedTerminalFailures(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	documentSeed(t, db, r, 1, "kb", "doc", 8)
	require.NoError(t, db.Exec(`UPDATE task_pending_ops SET fail_count=3`).Error)
	claim, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=?`, time.Now().Add(-time.Hour)).Error)
	failures, err := r.RecoverDocumentFailures(ctx)
	require.NoError(t, err)
	require.Len(t, failures, 1)
	require.Equal(t, claim.Op.ID, failures[0].ID)
	require.Equal(t, 4, failures[0].FailCount)
	require.JSONEq(t, string(claim.Op.Payload), string(failures[0].Payload))
	failures, err = r.RecoverDocumentFailures(ctx)
	require.NoError(t, err)
	require.Empty(t, failures)
}

func TestDocumentRejectsPayloadScopeMismatch(t *testing.T) {
	db, r := documentDB(t)
	op := documentSeed(t, db, r, 1, "kb", "doc", 1)
	var payload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(op.Payload, &payload))
	payload.KnowledgeBaseID = "another-kb"
	op.Payload, _ = json.Marshal(payload)
	accepted, err := r.EnqueueDocument(context.Background(), op)
	require.Error(t, err)
	require.False(t, accepted)
}

func TestDocumentMoveScopeReplacesPendingButPreservesLease(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	oldOp := documentSeed(t, db, r, 1, "old-kb", "doc", 3)
	old, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id,tenant_id) VALUES('new-kb',1)`).Error)
	token, err := r.BeginDocumentGeneration(ctx, 1, "doc", "old-kb", "new-kb")
	require.NoError(t, err)
	require.NoError(t, db.Exec(`UPDATE knowledges
		SET knowledge_base_id='new-kb',parse_status='pending' WHERE id='doc'`).Error)
	payload, _ := json.Marshal(types.DocumentProcessPayload{TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "new-kb"})
	nextOp := &types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeDocumentProcess, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "new-kb", DedupKey: "doc", Op: "process", Payload: payload,
	}
	accepted, err := r.EnqueueDocumentGeneration(ctx, nextOp, token)
	require.NoError(t, err)
	require.True(t, accepted)
	require.NotEqual(t, oldOp.ID, nextOp.ID)
	require.Equal(t, "new-kb", nextOp.ScopeID)
	blocked, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.False(t, finishDocumentTest(ctx, t, r, old, "success", time.Time{}, ""))
	accepted, err = r.EnqueueDocument(ctx, oldOp)
	require.NoError(t, err)
	require.False(t, accepted)
	next, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, nextOp.ID, next.Op.ID)
	require.Equal(t, "new-kb", next.Op.ScopeID)
}

func TestDocumentTerminalSettlementFencesPersistedNewAttempt(t *testing.T) {
	for _, attempt := range []int{1} {
		for _, recovery := range []bool{false, true} {
			t.Run(fmt.Sprintf("attempt=%d/recovery=%t", attempt, recovery), func(t *testing.T) {
				db, r := documentDB(t)
				ctx := context.Background()
				original := documentSeed(t, db, r, 1, "kb", "doc", attempt)
				require.NoError(t, db.Exec(`UPDATE task_pending_ops SET fail_count=3`).Error)
				claim, err := r.ClaimDocument(ctx, 1, time.Minute)
				require.NoError(t, err)
				// Reparse opens its next persisted attempt and resets status before enqueue.
				require.NoError(t, db.Exec(`INSERT INTO knowledge_processing_spans(knowledge_id,attempt)
 VALUES('doc',2)`).Error)
				require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status='pending',error_message=''
 WHERE id='doc'`).Error)
				if recovery {
					require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=?`,
						time.Now().Add(-time.Minute)).Error)
					failures, err := r.RecoverDocumentFailures(ctx)
					require.NoError(t, err)
					require.Len(t, failures, 1)
					require.JSONEq(t, string(original.Payload), string(failures[0].Payload))
				} else {
					require.True(t, finishDocumentTest(ctx, t, r, claim, "terminal", time.Time{}, "old parse failed"))
				}
				var status string
				require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id='doc'`).Scan(&status).Error)
				require.Equal(t, types.ParseStatusPending, status)
				var letters []types.TaskDeadLetter
				require.NoError(t, db.Find(&letters).Error)
				require.Len(t, letters, 1)
				require.JSONEq(t, string(original.Payload), string(letters[0].Payload))
				require.Equal(t, 4, letters[0].FailCount)
				replacement := documentSeed(t, db, r, 1, "kb", "doc", 2)
				next, err := r.ClaimDocument(ctx, 1, time.Minute)
				require.NoError(t, err)
				require.NotNil(t, next)
				require.Equal(t, replacement.ID, next.Op.ID)
				require.Zero(t, next.Op.FailCount)
			})
		}
	}
}

func TestDocumentSnapshotCleanupCannotDeleteReplacement(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	oldOp := documentSeed(t, db, r, 1, "kb", "doc", 1)
	old, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	ids, err := r.SnapshotDocumentPending(ctx, 1, "doc")
	require.NoError(t, err)
	require.Equal(t, []int64{oldOp.ID}, ids)
	replacement := documentSeed(t, db, r, 1, "kb", "doc", 2)
	require.NoError(t, r.DeleteDocumentPending(ctx, 1, "doc", ids))
	var rows []types.TaskPendingOp
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, replacement.ID, rows[0].ID)
	blocked, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Nil(t, blocked)
	require.NoError(t, r.DeleteDocumentPending(ctx, 2, "doc", []int64{replacement.ID}))
	require.NoError(t, r.DeleteDocumentPending(ctx, 1, "wrong-doc", []int64{replacement.ID}))
	require.False(t, finishDocumentTest(ctx, t, r, old, "success", time.Time{}, ""))
	next, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, next)
	require.Equal(t, replacement.ID, next.Op.ID)
}
