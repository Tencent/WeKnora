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
	"gorm.io/gorm"
)

func TestDocumentGenerationFencesUnversionedRedelivery(t *testing.T) {
	db, r := documentDB(t)
	testDocumentGenerationRedelivery(t, db, r)
}

func testDocumentGenerationRedelivery(t *testing.T, db *gorm.DB, r *taskPendingOpsRepository) {
	t.Helper()
	ctx := context.Background()
	original := documentSeed(t, db, r, 1, "kb", "doc", 0)
	initial, err := r.InitialDocumentGeneration(ctx, 1, "doc", "kb")
	require.NoError(t, err)
	require.NotEmpty(t, initial)
	claim, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	token, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	require.NotEqual(t, initial, token)
	changed, err := r.UpdateDocumentGeneration(ctx, 1, "doc", token, map[string]any{"parse_status": "pending"})
	require.NoError(t, err)
	require.True(t, changed)
	adopted, err := r.InitialDocumentGeneration(ctx, 1, "doc", "kb")
	require.NoError(t, err)
	require.Empty(t, adopted)
	accepted, err := r.EnqueueDocument(ctx, original)
	require.NoError(t, err)
	require.False(t, accepted)
	accepted, err = r.EnqueueDocumentGeneration(ctx, original, initial)
	require.NoError(t, err)
	require.False(t, accepted)
	require.False(t, finishDocumentTest(ctx, t, r, claim, "terminal", time.Time{}, "old zero failed"))
	accepted, err = r.EnqueueDocumentGeneration(ctx, original, token)
	require.NoError(t, err)
	require.True(t, accepted)
	next, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Equal(t, token, next.Generation)
	require.True(t, finishDocumentTest(ctx, t, r, next, "terminal", time.Time{}, "current zero failed"))
	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id='doc'`).Scan(&status).Error)
	require.Equal(t, types.ParseStatusFailed, status)
	nextToken, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	changed, err = r.UpdateDocumentGeneration(ctx, 1, "doc", nextToken, map[string]any{"parse_status": "pending"})
	require.NoError(t, err)
	require.True(t, changed)
	accepted, err = r.EnqueueDocumentGeneration(ctx, original, nextToken)
	require.NoError(t, err)
	require.True(t, accepted)
	next, err = r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Zero(t, next.Op.FailCount)
	var payload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(original.Payload, &payload))
	failed, err := r.FailLegacyDocumentGeneration(ctx, &payload, "late archived legacy")
	require.NoError(t, err)
	require.False(t, failed)
}

func TestDocumentGenerationRecoveryRetainsReplacement(t *testing.T) {
	db, r := documentDB(t)
	testDocumentGenerationRecovery(t, db, r)
}

func testDocumentGenerationRecovery(t *testing.T, db *gorm.DB, r *taskPendingOpsRepository) {
	t.Helper()
	ctx := context.Background()
	old := documentSeed(t, db, r, 1, "kb", "doc", 0)
	require.NoError(t, db.Exec(`UPDATE task_pending_ops SET fail_count=3`).Error)
	_, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	token, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	accepted, err := r.EnqueueDocumentGeneration(ctx, old, token)
	require.NoError(t, err)
	require.True(t, accepted)
	require.NoError(t, db.Exec(`UPDATE document_execution_leases SET expires_at=?`, time.Now().Add(-time.Minute)).Error)
	failures, err := r.RecoverDocumentFailures(ctx)
	require.NoError(t, err)
	require.Empty(t, failures)
	next, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Equal(t, token, next.Generation)
	require.Zero(t, next.Op.FailCount)
}

func TestDocumentGenerationGuardsCentralKnowledgeMutations(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(`DROP TABLE knowledges`).Error)
	require.NoError(t, db.Exec(knowledgesTestDDL).Error)
	documentSeed(t, db, r, 1, "kb", "doc", 0)
	oldToken, err := r.InitialDocumentGeneration(ctx, 1, "doc", "kb")
	require.NoError(t, err)
	oldCtx := types.WithDocumentGeneration(ctx, 1, "doc", oldToken)
	token, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	repo := NewKnowledgeRepository(db)
	require.ErrorIs(t, repo.UpdateKnowledgeColumn(oldCtx, "doc", "parse_status", "failed"),
		types.ErrDocumentGenerationSuperseded)
	require.ErrorIs(t, repo.UpdateKnowledgeColumns(oldCtx, "doc", map[string]any{"parse_status": "failed"}),
		types.ErrDocumentGenerationSuperseded)
	var knowledge types.Knowledge
	require.NoError(t, db.First(&knowledge, "id=?", "doc").Error)
	knowledge.ParseStatus = types.ParseStatusFailed
	require.ErrorIs(t, repo.UpdateKnowledge(oldCtx, &knowledge), types.ErrDocumentGenerationSuperseded)
	currentCtx := types.WithDocumentGeneration(ctx, 1, "doc", token)
	knowledge.ParseStatus = types.ParseStatusPending
	knowledge.Title = "current"
	knowledge.PendingSubtasksCount = 99
	require.NoError(t, repo.UpdateKnowledge(currentCtx, &knowledge))
	var stored types.Knowledge
	require.NoError(t, db.First(&stored, "id=?", "doc").Error)
	require.Equal(t, "current", stored.Title)
	require.Zero(t, stored.PendingSubtasksCount)
	require.Equal(t, stored.UpdatedAt, knowledge.UpdatedAt)
	require.NoError(t, db.Delete(&types.Knowledge{}, "id=?", "doc").Error)
	require.ErrorIs(t, repo.UpdateKnowledge(currentCtx, &knowledge), types.ErrDocumentGenerationSuperseded)
}

func TestDocumentGenerationConcurrentLateEnqueue(t *testing.T) {
	db, r := documentDB(t)
	testDocumentGenerationConcurrent(t, db, r)
}

func testDocumentGenerationConcurrent(t *testing.T, db *gorm.DB, r *taskPendingOpsRepository) {
	t.Helper()
	ctx := context.Background()
	op := documentSeed(t, db, r, 1, "kb", "doc", 0)
	old, err := r.InitialDocumentGeneration(ctx, 1, "doc", "kb")
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			copyOp := *op
			accepted, err := r.EnqueueDocumentGeneration(ctx, &copyOp, old)
			if err == nil && accepted {
				err = fmt.Errorf("stale generation admitted")
			}
			results <- err
		}()
	}
	token, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	accepted, err := r.EnqueueDocumentGeneration(ctx, op, token)
	require.NoError(t, err)
	require.True(t, accepted)
	current, err := r.CheckDocumentGeneration(ctx, 1, "doc", old)
	require.NoError(t, err)
	require.False(t, current)
	current, err = r.CheckDocumentGeneration(ctx, 1, "doc", token)
	require.NoError(t, err)
	require.True(t, current)
}

func TestDocumentGenerationTransferCheckpoint(t *testing.T) {
	db, r := documentDB(t)
	ctx := context.Background()
	require.NoError(t, db.Exec(`DROP TABLE knowledges`).Error)
	require.NoError(t, db.Exec(knowledgesTestDDL).Error)
	documentSeed(t, db, r, 1, "source", "doc", 0)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_bases(id,tenant_id) VALUES('target',1)`).Error)
	token, err := r.BeginDocumentGeneration(ctx, 1, "doc", "source", "target")
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id=?", "doc").Update("updated_at", time.Now()).Error)
	repo := NewKnowledgeRepository(db)
	before, err := repo.GetKnowledgeByID(ctx, 1, "doc")
	require.NoError(t, err)
	after := *before
	after.KnowledgeBaseID = "target"
	guarded := types.WithDocumentGeneration(ctx, 1, "doc", token)
	require.NoError(t, repo.UpdateKnowledgeForTransfer(guarded, before, &after))
	_, err = r.BeginDocumentGeneration(ctx, 1, "doc", "target", "target")
	require.NoError(t, err)
	final := after
	final.ParseStatus = types.ParseStatusFailed
	require.ErrorIs(t, repo.UpdateKnowledgeForTransfer(guarded, &after, &final), types.ErrDocumentGenerationSuperseded)
}

func TestDocumentGenerationPostgres(t *testing.T) {
	dsn := os.Getenv("DOCUMENT_SCHEDULER_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("DOCUMENT_SCHEDULER_POSTGRES_DSN is not set")
	}
	cases := map[string]func(*testing.T, *gorm.DB, *taskPendingOpsRepository){
		"legacy_duplicate": testDocumentGenerationLegacyFailurePreservesDurableWork,
		"redelivery":       testDocumentGenerationRedelivery, "recovery": testDocumentGenerationRecovery,
		"concurrent": testDocumentGenerationConcurrent,
		"attempt":    testDocumentGenerationAttemptAllocation,
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
			require.NoError(t, err)
			adminSQL, err := admin.DB()
			require.NoError(t, err)
			schema := fmt.Sprintf("document_generation_test_%d", time.Now().UnixNano())
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
				`CREATE TABLE tenants(id BIGINT PRIMARY KEY,deleted_at TIMESTAMPTZ)`,
				`CREATE TABLE knowledge_processing_spans(knowledge_id TEXT,attempt INTEGER)`,
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
			run(t, db, &taskPendingOpsRepository{db: db})
		})
	}
}

func TestDocumentGenerationFencesAttemptAllocation(t *testing.T) {
	db, r := documentDB(t)
	testDocumentGenerationAttemptAllocation(t, db, r)
}

func testDocumentGenerationAttemptAllocation(t *testing.T, db *gorm.DB, r *taskPendingOpsRepository) {
	t.Helper()
	ctx := context.Background()
	require.NoError(t, db.Exec(`DROP TABLE knowledge_processing_spans`).Error)
	ddl := spansTestDDL
	if db.Name() == "postgres" {
		ddl = strings.ReplaceAll(ddl, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
		ddl = strings.ReplaceAll(ddl, "DATETIME", "TIMESTAMPTZ")
	}
	require.NoError(t, db.Exec(ddl).Error)
	documentSeed(t, db, r, 1, "kb", "doc", 0)
	old, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	current, err := r.BeginDocumentGeneration(ctx, 1, "doc", "kb", "kb")
	require.NoError(t, err)
	spans := &knowledgeSpanRepository{db: db}
	root := &types.KnowledgeProcessingSpan{
		KnowledgeID: "doc", SpanID: "current-root", Name: "knowledge_processing",
		Kind: types.SpanKindRoot, Status: types.SpanStatusRunning,
	}
	attempt, err := spans.OpenDocumentGenerationAttempt(types.WithDocumentGeneration(ctx, 1, "doc", current), root)
	require.NoError(t, err)
	require.Equal(t, 1, attempt)
	stale := &types.KnowledgeProcessingSpan{
		KnowledgeID: "doc", SpanID: "old-root", Name: "knowledge_processing",
		Kind: types.SpanKindRoot, Status: types.SpanStatusRunning,
	}
	_, err = spans.OpenDocumentGenerationAttempt(types.WithDocumentGeneration(ctx, 1, "doc", old), stale)
	require.ErrorIs(t, err, types.ErrDocumentGenerationSuperseded)
	rows, err := spans.ListByAttempt(ctx, "doc", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "current-root", rows[0].SpanID)
	require.Equal(t, types.SpanStatusRunning, rows[0].Status)
}

func TestDocumentGenerationLegacyFailurePreservesDurableWork(t *testing.T) {
	db, r := documentDB(t)
	testDocumentGenerationLegacyFailurePreservesDurableWork(t, db, r)
}

func testDocumentGenerationLegacyFailurePreservesDurableWork(
	t *testing.T, db *gorm.DB, r *taskPendingOpsRepository,
) {
	t.Helper()
	ctx := context.Background()
	op := documentSeed(t, db, r, 1, "kb", "doc", 1)
	var payload types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(op.Payload, &payload))
	require.NoError(t, db.Exec(`ALTER TABLE knowledge_processing_spans ADD COLUMN status TEXT`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledge_processing_spans(knowledge_id,attempt,status)
 VALUES('doc',1,'running')`).Error)
	settled, err := r.FailLegacyDocumentGeneration(ctx, &payload, "duplicate transport failed")
	require.NoError(t, err)
	require.False(t, settled)
	var status string
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id='doc'`).Scan(&status).Error)
	require.Equal(t, types.ParseStatusPending, status)
	claim, err := r.ClaimDocument(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NotNil(t, claim)
	require.NoError(t, db.Exec(`UPDATE knowledges SET parse_status='processing' WHERE id='doc'`).Error)
	settled, err = r.FailLegacyDocumentGeneration(ctx, &payload, "duplicate transport failed again")
	require.NoError(t, err)
	require.False(t, settled)
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id='doc'`).Scan(&status).Error)
	require.Equal(t, types.ParseStatusProcessing, status)
	require.NoError(t, db.Raw(`SELECT status FROM knowledge_processing_spans WHERE knowledge_id='doc'`).
		Scan(&status).Error)
	require.Equal(t, types.SpanStatusRunning, status)
	renewed, err := r.RenewDocument(ctx, claim, time.Minute)
	require.NoError(t, err)
	require.True(t, renewed)
	var pending types.TaskPendingOp
	require.NoError(t, db.First(&pending, op.ID).Error)
	require.Zero(t, pending.FailCount)
	require.True(t, finishDocumentTest(ctx, t, r, claim, "success", time.Time{}, ""))
	settled, err = r.FailLegacyDocumentGeneration(ctx, &payload, "original transport failure without durable owner")
	require.NoError(t, err)
	require.True(t, settled)
	require.NoError(t, db.Raw(`SELECT parse_status FROM knowledges WHERE id='doc'`).Scan(&status).Error)
	require.Equal(t, types.ParseStatusFailed, status)
}
