package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupWikiRecovery(t *testing.T) (WikiRecoveryRepository, *gorm.DB) {
	t.Helper()
	db := setupTaskQueueTestDB(t)
	for _, ddl := range []string{
		taskQueueKnowledgeBaseTestDDL, taskQueueKnowledgeTestDDL, spansTestDDL, wikiPagesTestDDL,
		`ALTER TABLE knowledge_bases ADD COLUMN updated_at DATETIME`,
		`ALTER TABLE knowledges ADD COLUMN title TEXT`,
		`ALTER TABLE knowledges ADD COLUMN created_at DATETIME`,
		`ALTER TABLE knowledges ADD COLUMN custom_metadata TEXT`,
		`CREATE TABLE tenants (id INTEGER PRIMARY KEY, deleted_at DATETIME)`,
		`CREATE TABLE chunks (id TEXT PRIMARY KEY, tenant_id INTEGER, knowledge_base_id TEXT,
			knowledge_id TEXT, chunk_type TEXT, content TEXT, index_status TEXT, deleted_at DATETIME)`,
		`INSERT INTO tenants(id) VALUES (1), (2)`,
		`INSERT INTO knowledge_bases(id,tenant_id) VALUES ('kb',1),('other-kb',2)`,
	} {
		require.NoError(t, db.Exec(ddl).Error)
	}
	return NewWikiRecoveryRepository(db), db
}

func seedWikiFailure(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO knowledges(
        id,tenant_id,knowledge_base_id,parse_status,title,custom_metadata)
		VALUES (?,1,'kb','completed',?,'{}')`, id, "Document "+id).Error)
	require.NoError(t, db.Exec(`INSERT INTO chunks(
        id,tenant_id,knowledge_base_id,knowledge_id,chunk_type,content,index_status)
		VALUES (?,1,'kb',?,'text','Original source bytes','ready')`, "chunk-"+id, id).Error)
	payload, err := json.Marshal(map[string]interface{}{
		"op": "ingest", "knowledge_id": id, "attempt": 1, "language": "zh-CN",
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.TaskDeadLetter{
		TenantID: 1, TaskType: types.TypeWikiIngest,
		Scope: types.TaskScopeKnowledgeBase, ScopeID: "kb", RelatedID: id,
		Payload: payload, FailCount: 6, LastError: "model connection refused",
	}).Error)
	require.NoError(t, db.Create(&types.KnowledgeProcessingSpan{
		KnowledgeID: id, Attempt: 1, SpanID: "wiki-" + id,
		Name: "postprocess.wiki", Kind: types.SpanKindSubSpan, Status: types.SpanStatusFailed,
		ErrorMessage: "dial tcp: connection refused",
	}).Error)
}

func TestWikiRecoveryRetainsArtifactsAndDeduplicates(t *testing.T) {
	r, db := setupWikiRecovery(t)
	seedWikiFailure(t, db, "doc")
	require.NoError(t, db.Exec(`INSERT INTO wiki_pages(id,tenant_id,knowledge_base_id,slug,title,source_refs)
		VALUES ('page',1,'kb','entity/existing','Existing page','["doc"]')`).Error)
	ctx := context.Background()
	before, err := r.ListDocuments(ctx, 1, "kb", "failed", 1, 20)
	require.NoError(t, err)
	require.Len(t, before.Documents, 1)
	assert.Equal(t, int64(1), before.FailedTotal)
	assert.Equal(t, "completed", before.Documents[0].ParseStatus)
	assert.Contains(t, before.Documents[0].LastError, "connection refused")
	for _, want := range []int{1, 0, 0} {
		n, err := r.RetryDocuments(ctx, 1, "kb", []string{"doc", "doc"})
		require.NoError(t, err)
		assert.Equal(t, want, n)
	}
	var kn struct {
		ParseStatus          string
		PendingSubtasksCount int
	}
	require.NoError(t, db.Table("knowledges").Where("id = ?", "doc").Take(&kn).Error)
	assert.Equal(t, "finalizing", kn.ParseStatus)
	assert.Equal(t, 1, kn.PendingSubtasksCount)
	var pending []types.TaskPendingOp
	require.NoError(t, db.Find(&pending).Error)
	require.Len(t, pending, 1)
	assert.Equal(t, 0, pending[0].FailCount)
	assert.JSONEq(t, `{"op":"ingest","knowledge_id":"doc","attempt":1,"language":"zh-CN"}`, string(pending[0].Payload))
	var content string
	require.NoError(t, db.Table("chunks").Select("content").Where("id = ?", "chunk-doc").Scan(&content).Error)
	assert.Equal(t, "Original source bytes", content)
	var page types.WikiPage
	require.NoError(t, db.First(&page).Error)
	assert.Equal(t, "Existing page", page.Title)
	after, err := r.ListDocuments(ctx, 1, "kb", "pending", 1, 20)
	require.NoError(t, err)
	require.Len(t, after.Documents, 1)
	assert.Zero(t, after.FailedTotal)
}

func TestWikiRecoveryRejectsInvalidSourcesAtomically(t *testing.T) {
	for _, test := range []struct{ name, sql string }{
		{"missing chunks", `UPDATE chunks SET deleted_at=CURRENT_TIMESTAMP WHERE knowledge_id='z-bad'`},
		{"index failure", `UPDATE chunks SET index_status='failed' WHERE knowledge_id='z-bad'`},
		{"cancelled", `UPDATE knowledges SET parse_status='cancelled' WHERE id='z-bad'`},
		{"source removed", `UPDATE knowledges SET deleted_at=CURRENT_TIMESTAMP WHERE id='z-bad'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			r, db := setupWikiRecovery(t)
			seedWikiFailure(t, db, "a-good")
			seedWikiFailure(t, db, "z-bad")
			require.NoError(t, db.Exec(test.sql).Error)
			n, err := r.RetryDocuments(context.Background(), 1, "kb", []string{"a-good", "z-bad"})
			require.Error(t, err)
			assert.Zero(t, n)
			var count int64
			require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
			assert.Zero(t, count)
			require.NoError(t, db.Model(&types.TaskDeadLetter{}).Count(&count).Error)
			assert.Equal(t, int64(2), count)
		})
	}
}

func TestWikiRecoveryIsolationAndSupersededAttempts(t *testing.T) {
	r, db := setupWikiRecovery(t)
	seedWikiFailure(t, db, "doc")
	ctx := context.Background()
	list, err := r.ListDocuments(ctx, 2, "kb", "", 1, 20)
	require.NoError(t, err)
	assert.Empty(t, list.Documents)
	_, err = r.RetryDocuments(ctx, 2, "kb", []string{"doc"})
	require.Error(t, err)
	require.NoError(t, db.Create(&types.KnowledgeProcessingSpan{
		KnowledgeID: "doc",
		Attempt:     2,
		SpanID:      "new-run",
		Name:        "attempt",
		Kind:        types.SpanKindRoot,
		Status:      types.SpanStatusDone,
	}).Error)
	list, err = r.ListDocuments(ctx, 1, "kb", "failed", 1, 20)
	require.NoError(t, err)
	assert.Zero(t, list.Total)
	n, err := r.RetryDocuments(ctx, 1, "kb", []string{"doc"})
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestWikiRecoveryBatchPagination(t *testing.T) {
	r, db := setupWikiRecovery(t)
	for i := 0; i < 25; i++ {
		seedWikiFailure(t, db, fmt.Sprintf("doc-%02d", i))
	}
	ctx := context.Background()
	ids := []string{}
	for page := 1; page <= 3; page++ {
		list, err := r.ListDocuments(ctx, 1, "kb", "failed", page, 10)
		require.NoError(t, err)
		assert.Equal(t, int64(25), list.Total)
		for _, doc := range list.Documents {
			ids = append(ids, doc.KnowledgeID)
		}
	}
	require.Len(t, ids, 25)
	n, err := r.RetryDocuments(ctx, 1, "kb", ids)
	require.NoError(t, err)
	assert.Equal(t, 25, n)
	n, err = r.RetryDocuments(ctx, 1, "kb", ids)
	require.NoError(t, err)
	assert.Zero(t, n)
}

func TestList_SourceRefPreservesDocumentIdentity(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	for i, refs := range []types.StringArray{{"doc"}, {"doc|Source title"}, {"doc-other|Different"}, {"another"}} {
		require.NoError(t, db.Create(&types.WikiPage{
			ID:              fmt.Sprint(i),
			TenantID:        1,
			KnowledgeBaseID: "kb",
			Slug:            fmt.Sprintf("entity/%d", i),
			Title:           "Page",
			SourceRefs:      refs,
		}).Error)
	}
	pages, total, err := repo.List(context.Background(), &types.WikiPageListRequest{
		KnowledgeBaseID: "kb", SourceRef: "doc", Page: 1, PageSize: 1,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	assert.Len(t, pages, 1)
	pages, total, err = repo.List(context.Background(), &types.WikiPageListRequest{
		KnowledgeBaseID: "kb", SourceRef: "doc-other", Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, pages, 1)
	assert.Equal(t, "2", pages[0].ID)
}

func TestWikiRecoveryPreservesRetractsAndResetsOnlyWikiCounter(t *testing.T) {
	r, db := setupWikiRecovery(t)
	seedWikiFailure(t, db, "doc")
	require.NoError(t, db.Exec(`UPDATE knowledges
        SET parse_status='finalizing', pending_subtasks_count=2 WHERE id='doc'`).Error)
	require.NoError(t, db.Create(&types.TaskDeadLetter{
		TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "kb", RelatedID: "doc", Payload: []byte(`{"op":"retract","knowledge_id":"doc"}`), FailCount: 6,
	}).Error)
	n, err := r.RetryDocuments(context.Background(), 1, "kb", []string{"doc"})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	var count int
	require.NoError(t, db.Table("knowledges").Select("pending_subtasks_count").
		Where("id = ?", "doc").Scan(&count).Error)
	assert.Equal(t, 3, count)
	var letters []types.TaskDeadLetter
	require.NoError(t, db.Find(&letters).Error)
	require.Len(t, letters, 1)
	assert.Contains(t, string(letters[0].Payload), "retract")
}

// The production restore transaction must serialize overlapping requests across
// independent connections, including after the caller retries a timed-out API.
func TestWikiRecoveryPostgresConcurrent(t *testing.T) {
	dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL recovery locking")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	schema := "wiki_recovery_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	db, err := gorm.Open(postgres.Open(dsn+"&search_path="+schema), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Tenant{}, &types.KnowledgeBase{}, &types.Knowledge{}, &types.Chunk{},
		&types.KnowledgeProcessingSpan{}, &types.TaskPendingOp{}, &types.TaskDeadLetter{},
	))
	require.NoError(t, db.Exec("INSERT INTO tenants(id) VALUES (1)").Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id,tenant_id) VALUES ('kb',1)").Error)
	ids := []string{}
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("doc-%02d", i)
		seedWikiFailure(t, db, id)
		ids = append(ids, id)
	}
	r := NewWikiRecoveryRepository(db)
	list, err := r.ListDocuments(context.Background(), 1, "kb", "failed", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(10), list.Total)
	var wg sync.WaitGroup
	counts := make(chan int, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := r.RetryDocuments(context.Background(), 1, "kb", ids)
			counts <- n
			errs <- err
		}()
	}
	wg.Wait()
	close(counts)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	sum := 0
	for n := range counts {
		sum += n
	}
	assert.Equal(t, 10, sum)
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	assert.Equal(t, int64(10), count)
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("pending_subtasks_count = 1 AND parse_status = 'finalizing'").Count(&count).Error)
	assert.Equal(t, int64(10), count)

	require.NoError(t, db.AutoMigrate(&types.WikiPage{}))
	require.NoError(t, db.Exec(`ALTER TABLE wiki_pages
        ALTER COLUMN source_refs TYPE jsonb USING source_refs::jsonb`).Error)
	require.NoError(t, db.Create(&types.WikiPage{
		ID:              "existing",
		TenantID:        1,
		KnowledgeBaseID: "kb",
		Slug:            "summary/existing",
		PageType:        types.WikiPageTypeSummary,
		Status:          types.WikiPageStatusPublished,
		Content:         "Original contribution",
		SourceRefs:      types.StringArray{"doc-01|Source title"},
	}).Error)
	pages := NewWikiPageRepository(db)
	existing, err := pages.ListBySourceRef(context.Background(), "kb", "doc-01")
	require.NoError(t, err)
	require.Len(t, existing, 1)
	slugs, err := pages.ListSlugsBySourceRef(context.Background(), "kb", "doc-01")
	require.NoError(t, err)
	assert.Equal(t, []string{"summary/existing"}, slugs)
	summaries, err := pages.ListSummariesByKnowledgeIDs(context.Background(), "kb", []string{"doc-01", "doc-02"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"doc-01": "Original contribution"}, summaries)
	// Retain the isolated schema as failure evidence; it belongs to the disposable container.
	t.Logf("PostgreSQL evidence schema: %s", schema)
}

func TestWikiRecoveryReadsExistingSourceArtifacts(t *testing.T) {
	db := setupWikiPagesTestDB(t)
	repo := NewWikiPageRepository(db)
	for i, refs := range []types.StringArray{{"doc"}, {"doc|Source title"}, {"doc-other|Different"}} {
		require.NoError(t, db.Create(&types.WikiPage{
			ID:              fmt.Sprint(i),
			TenantID:        1,
			KnowledgeBaseID: "kb",
			Slug:            fmt.Sprintf("summary/%d", i),
			Title:           "Summary",
			PageType:        types.WikiPageTypeSummary,
			Status:          types.WikiPageStatusPublished,
			Content:         "Existing contribution",
			SourceRefs:      refs,
		}).Error)
	}
	pages, err := repo.ListBySourceRef(context.Background(), "kb", "doc")
	require.NoError(t, err)
	assert.Len(t, pages, 2)
	slugs, err := repo.ListSlugsBySourceRef(context.Background(), "kb", "doc")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"summary/0", "summary/1"}, slugs)
	summaries, err := repo.ListSummariesByKnowledgeIDs(context.Background(), "kb", []string{"doc", "absent"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"doc": "Existing contribution"}, summaries)
}

func TestWikiRecoveryReportsActiveDocumentAsRunning(t *testing.T) {
	r, db := setupWikiRecovery(t)
	seedWikiFailure(t, db, "doc")
	_, err := r.RetryDocuments(context.Background(), 1, "kb", []string{"doc"})
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeProcessingSpan{}).
		Where("knowledge_id = ? AND name = ?", "doc", "postprocess.wiki").
		Update("status", types.SpanStatusRunning).Error)
	result, err := r.ListDocuments(context.Background(), 1, "kb", "running", 1, 20)
	require.NoError(t, err)
	require.Len(t, result.Documents, 1)
	assert.Equal(t, "running", result.Documents[0].WikiStatus)
	result, err = r.ListDocuments(context.Background(), 1, "kb", "pending", 1, 20)
	require.NoError(t, err)
	assert.Empty(t, result.Documents)
}
