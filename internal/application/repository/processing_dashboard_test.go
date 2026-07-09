package repository

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupProcessingDashboardRepo(t *testing.T) (ProcessingDashboardRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+url.QueryEscape(t.Name())+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	schema := []string{
		`CREATE TABLE knowledge_bases (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			name TEXT,
			type TEXT,
			is_temporary BOOLEAN DEFAULT FALSE,
			deleted_at DATETIME
		)`,
		`CREATE TABLE knowledges (
			id TEXT PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id TEXT,
			title TEXT,
			file_name TEXT,
			parse_status TEXT,
			pending_subtasks_count INTEGER DEFAULT 0,
			error_message TEXT,
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		)`,
		`CREATE TABLE knowledge_processing_spans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			knowledge_id TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 1,
			span_id TEXT NOT NULL,
			parent_span_id TEXT,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			input TEXT,
			output TEXT,
			metadata TEXT,
			error_code TEXT,
			error_message TEXT,
			error_detail TEXT,
			started_at DATETIME,
			finished_at DATETIME,
			duration_ms INTEGER,
			created_at DATETIME,
			updated_at DATETIME
		)`,
		`CREATE TABLE task_pending_ops (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL,
			task_type TEXT,
			scope TEXT,
			scope_id TEXT,
			op TEXT,
			dedup_key TEXT,
			payload TEXT,
			fail_count INTEGER DEFAULT 0,
			enqueued_at DATETIME,
			claimed_at DATETIME
		)`,
	}
	for _, stmt := range schema {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatal(err)
		}
	}
	return NewProcessingDashboardRepository(db), db
}

func TestProcessingDashboardRepositoryCandidatesAndSpans(t *testing.T) {
	repo, db := setupProcessingDashboardRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	insertKB(t, db, "kb1", 1, "Knowledge One", types.KnowledgeBaseTypeDocument)
	insertKB(t, db, "kb2", 1, "Hidden", types.KnowledgeBaseTypeDocument)
	insertKB(t, db, "kb3", 2, "Shared", types.KnowledgeBaseTypeDocument)
	insertKnowledge(t, db, "active", 1, "kb1", "Active Doc", "active.pdf", types.ParseStatusProcessing, now, nil)
	insertKnowledge(t, db, "completed", 1, "kb1", "Done Doc", "done.pdf", types.ParseStatusCompleted, now, nil)
	insertKnowledge(t, db, "shared", 2, "kb3", "Shared Doc", "shared.pdf", types.ParseStatusFinalizing, now, nil)
	deletedAt := now
	insertKnowledge(t, db, "deleted", 1, "kb1", "Deleted Doc", "deleted.pdf", types.ParseStatusProcessing, now, &deletedAt)

	filter := types.ProcessingDashboardFilter{
		AccessibleScopes: []types.KnowledgeSearchScope{
			{TenantID: 1, KBID: "kb1"},
			{TenantID: 2, KBID: "kb3"},
		},
	}
	rows, err := repo.ListCandidateKnowledge(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("candidate rows = %d, want 2: %#v", len(rows), rows)
	}

	filter.Keyword = "shared"
	rows, err = repo.ListCandidateKnowledge(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "shared" || rows[0].KnowledgeBaseName != "Shared" {
		t.Fatalf("keyword rows = %#v, want shared row", rows)
	}

	insertSpanRow(t, db, "active", 1, "docreader", types.StageDocReader, types.SpanStatusDone, now)
	insertSpanRow(t, db, "active", 1, "img-old", "multimodal.image[0]", types.SpanStatusFailed, now)
	insertSpanRow(t, db, "active", 1, "img-new", "multimodal.image[0]", types.SpanStatusDone, now.Add(time.Minute))
	insertSpanRow(t, db, "active", 2, "q", "postprocess.question.batch[0]", types.SpanStatusRunning, now)

	attempts, err := repo.ListLatestAttempts(ctx, []string{"active", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 1 || attempts[0].KnowledgeID != "active" || attempts[0].Attempt != 2 {
		t.Fatalf("attempts = %#v, want active attempt 2", attempts)
	}

	spans, err := repo.ListCanonicalAndParentSpans(ctx, []string{"active"})
	if err != nil {
		t.Fatal(err)
	}
	for _, sp := range spans {
		if sp.Name == "multimodal.image[0]" {
			t.Fatalf("canonical span query materialized fanout child: %#v", sp)
		}
	}
	buckets, details, err := repo.AggregateFanoutSpans(ctx, []string{"active"})
	if err != nil {
		t.Fatal(err)
	}
	foundDoneImage := false
	for _, bucket := range buckets {
		if bucket.KnowledgeID == "active" && bucket.Stage == types.ProcessingStageMultimodal && bucket.Status == types.SpanStatusDone && bucket.Count == 1 {
			foundDoneImage = true
		}
	}
	if !foundDoneImage {
		t.Fatalf("fanout aggregate buckets = %#v, want latest image done count", buckets)
	}
	if len(details) != 1 || details[0].Name != "postprocess.question.batch[0]" || details[0].Status != types.SpanStatusRunning {
		t.Fatalf("fanout details = %#v, want only running question detail", details)
	}
}

func TestProcessingDashboardRepositoryBatchAndWikiPending(t *testing.T) {
	repo, db := setupProcessingDashboardRepo(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 21, 10, 0, 0, 0, time.UTC)
	insertKB(t, db, "kb1", 1, "Knowledge One", types.KnowledgeBaseTypeDocument)
	ids := make([]string, 0, 501)
	for i := 0; i < 501; i++ {
		id := fmt.Sprintf("kid-%03d", i)
		ids = append(ids, id)
		insertKnowledge(t, db, id, 1, "kb1", id, id+".pdf", types.ParseStatusProcessing, now, nil)
		insertSpanRow(t, db, id, 1, "root", "root", types.SpanStatusRunning, now)
	}
	filter := types.ProcessingDashboardFilter{
		AccessibleScopes: []types.KnowledgeSearchScope{{TenantID: 1, KBID: "kb1"}},
	}
	rows, err := repo.ListKnowledgeRowsByIDs(ctx, filter, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 501 {
		t.Fatalf("rows = %d, want 501", len(rows))
	}
	attempts, err := repo.ListLatestAttempts(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 501 {
		t.Fatalf("attempts = %d, want 501", len(attempts))
	}

	insertWikiPending(t, db, 1, "kb1", "kid-000", 1, now.Add(2*time.Minute))
	insertWikiPending(t, db, 1, "kb1", "kid-000", 3, now)
	insertWikiPending(t, db, 1, "kb1", "kid-001", 2, now.Add(time.Minute))
	pending, err := repo.ListWikiPendingOps(ctx, filter, []string{"kid-000", "kid-001"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 {
		t.Fatalf("pending = %#v, want two folded knowledge rows", pending)
	}
	for _, row := range pending {
		if row.KnowledgeID == "kid-000" {
			if !row.QueuedAt.Equal(now) || row.FailCount != 3 {
				t.Fatalf("folded kid-000 = %#v, want earliest queued and max fail_count", row)
			}
		}
	}
}

func insertKB(t *testing.T, db *gorm.DB, id string, tenant uint64, name string, kbType string) {
	t.Helper()
	if err := db.Exec(`INSERT INTO knowledge_bases (id, tenant_id, name, type, is_temporary) VALUES (?, ?, ?, ?, FALSE)`,
		id, tenant, name, kbType).Error; err != nil {
		t.Fatal(err)
	}
}

func insertKnowledge(t *testing.T, db *gorm.DB, id string, tenant uint64, kbID, title, fileName, status string, ts time.Time, deletedAt *time.Time) {
	t.Helper()
	if err := db.Exec(`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, title, file_name, parse_status, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, tenant, kbID, title, fileName, status, ts, ts, deletedAt).Error; err != nil {
		t.Fatal(err)
	}
}

func insertSpanRow(t *testing.T, db *gorm.DB, kid string, attempt int, spanID, name, status string, ts time.Time) {
	t.Helper()
	if err := db.Exec(`INSERT INTO knowledge_processing_spans (knowledge_id, attempt, span_id, name, kind, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'subspan', ?, ?, ?)`,
		kid, attempt, spanID, name, status, ts, ts).Error; err != nil {
		t.Fatal(err)
	}
}

func insertWikiPending(t *testing.T, db *gorm.DB, tenant uint64, kbID, kid string, failCount int, queuedAt time.Time) {
	t.Helper()
	if err := db.Exec(`INSERT INTO task_pending_ops (tenant_id, task_type, scope, scope_id, dedup_key, fail_count, enqueued_at)
		VALUES (?, ?, 'knowledge_base', ?, ?, ?, ?)`,
		tenant, types.TypeWikiIngest, kbID, kid, failCount, queuedAt).Error; err != nil {
		t.Fatal(err)
	}
}
