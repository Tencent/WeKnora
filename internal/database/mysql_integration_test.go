package database_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	mysqlmigrate "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// TestMySQLBusinessPrimaryIntegration exercises the real MySQL migration and
// representative business repositories. It is opt-in so normal unit tests do
// not require Docker:
//
//	MYSQL_TEST_DSN='user:pass@tcp(127.0.0.1:3306)/WeKnora?parseTime=true&loc=UTC' \
//	  go test ./internal/database -run TestMySQLBusinessPrimaryIntegration
func TestMySQLBusinessPrimaryIntegration(t *testing.T) {
	dsn := os.Getenv("MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set MYSQL_TEST_DSN to run the MySQL business-primary integration test")
	}

	migrationDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	if err := migrationDB.Ping(); err != nil {
		t.Fatalf("ping MySQL: %v", err)
	}

	if cfg, err := mysqlmigrate.WithInstance(migrationDB, &mysqlmigrate.Config{}); err == nil {
		root := repositoryRoot(t)
		m, err := migrate.NewWithDatabaseInstance(
			"file://"+filepath.ToSlash(filepath.Join(root, "migrations", "mysql")),
			"mysql",
			cfg,
		)
		if err != nil {
			t.Fatalf("create MySQL migrator: %v", err)
		}
		if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			t.Fatalf("migrate MySQL up: %v", err)
		}
		version, dirty, err := m.Version()
		if err != nil {
			t.Fatalf("read migration version: %v", err)
		}
		if version != 70 || dirty {
			t.Fatalf("migration version = %d dirty=%v, want 70 clean", version, dirty)
		}
		_, _ = m.Close()
	} else {
		_ = migrationDB.Close()
		t.Fatalf("create MySQL migration driver: %v", err)
	}

	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("reopen MySQL after migration: %v", err)
	}
	defer sqlDB.Close()
	assertTableExists(t, sqlDB, "vector_stores", true)
	assertTableExists(t, sqlDB, "embeddings", false)
	assertTableExists(t, sqlDB, "organization_members", false)
	assertBusinessTableCount(t, sqlDB, 50)

	gormDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("open GORM MySQL: %v", err)
	}

	ctx := context.Background()
	const scopeID = "mysql-integration-scope"
	const wikiKBID = "mysql-integration-kb"
	wikiPageIDs := []string{"mysql-integration-wiki-1", "mysql-integration-wiki-2"}
	if err := gormDB.Where("scope_id = ?", scopeID).Delete(&types.TaskPendingOp{}).Error; err != nil {
		t.Fatalf("clean task rows: %v", err)
	}
	if err := gormDB.Where("scope_id = ?", scopeID).Delete(&types.TaskDeadLetter{}).Error; err != nil {
		t.Fatalf("clean dead-letter rows: %v", err)
	}
	if err := gormDB.Unscoped().Where("id IN ?", wikiPageIDs).Delete(&types.WikiPage{}).Error; err != nil {
		t.Fatalf("clean wiki rows: %v", err)
	}
	t.Cleanup(func() {
		_ = gormDB.Where("scope_id = ?", scopeID).Delete(&types.TaskPendingOp{}).Error
		_ = gormDB.Where("scope_id = ?", scopeID).Delete(&types.TaskDeadLetter{}).Error
		_ = gormDB.Unscoped().Where("id IN ?", wikiPageIDs).Delete(&types.WikiPage{}).Error
		_ = gormDB.Where("`key` = ?", "mysql.integration").Delete(&types.SystemSetting{}).Error
	})

	settingRepo := repository.NewSystemSettingRepository(gormDB)
	setting := &types.SystemSetting{
		Key:         "mysql.integration",
		Value:       types.JSON(`{"enabled":true}`),
		ValueType:   "json",
		Category:    "test",
		Description: "MySQL integration",
	}
	if err := settingRepo.Upsert(ctx, setting); err != nil {
		t.Fatalf("upsert system setting: %v", err)
	}
	setting.Description = "updated"
	if err := settingRepo.Upsert(ctx, setting); err != nil {
		t.Fatalf("update system setting: %v", err)
	}
	gotSetting, err := settingRepo.Get(ctx, setting.Key)
	if err != nil || gotSetting == nil || gotSetting.Description != "updated" {
		t.Fatalf("get updated system setting = %#v, err=%v", gotSetting, err)
	}

	taskRepo := repository.NewTaskPendingOpsRepository(gormDB)
	for _, key := range []string{"doc-a", "doc-b", "doc-c", "doc-d"} {
		if err := taskRepo.Enqueue(ctx, &types.TaskPendingOp{
			TenantID: 1,
			TaskType: "mysql-integration",
			Scope:    "knowledge",
			ScopeID:  scopeID,
			Op:       "ingest",
			DedupKey: key,
		}); err != nil {
			t.Fatalf("enqueue %s: %v", key, err)
		}
	}

	var wg sync.WaitGroup
	results := make(chan []*types.TaskPendingOp, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows, err := taskRepo.ClaimBatch(
				ctx,
				"mysql-integration",
				"knowledge",
				scopeID,
				2,
				time.Now().UTC().Add(-time.Hour),
			)
			results <- rows
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ClaimBatch: %v", err)
		}
	}
	seen := make(map[int64]struct{})
	for rows := range results {
		for _, row := range rows {
			if _, duplicate := seen[row.ID]; duplicate {
				t.Fatalf("task row %d was claimed twice", row.ID)
			}
			seen[row.ID] = struct{}{}
		}
	}
	if len(seen) != 4 {
		t.Fatalf("claimed %d distinct rows, want 4", len(seen))
	}
	var claimedID int64
	for id := range seen {
		claimedID = id
		break
	}
	for want := 1; want <= 2; want++ {
		got, err := taskRepo.IncrFailCount(ctx, claimedID)
		if err != nil || got != want {
			t.Fatalf("increment fail count = %d, err=%v, want %d", got, err, want)
		}
	}

	deadLetterRepo := repository.NewTaskDeadLetterRepository(gormDB)
	deadLetter := &types.TaskDeadLetter{
		TenantID:  1,
		TaskType:  "mysql-integration",
		Scope:     "knowledge",
		ScopeID:   scopeID,
		RelatedID: "doc-a",
		LastError: "integration failure",
		FailCount: 3,
	}
	if err := deadLetterRepo.Insert(ctx, deadLetter); err != nil {
		t.Fatalf("insert dead letter: %v", err)
	}
	if deadLetter.FailedAt.IsZero() {
		t.Fatal("dead-letter FailedAt was not normalized")
	}
	deadLetters, nextCursor, err := deadLetterRepo.ListByScope(ctx, "knowledge", scopeID, "", 10)
	if err != nil || len(deadLetters) != 1 || nextCursor != "" {
		t.Fatalf("list dead letters = %d rows, cursor=%q, err=%v", len(deadLetters), nextCursor, err)
	}

	wikiRepo := repository.NewWikiPageRepository(gormDB)
	wikiPages := []*types.WikiPage{
		{
			ID:              wikiPageIDs[0],
			TenantID:        1,
			KnowledgeBaseID: wikiKBID,
			Slug:            "entity/mysql-regex-sentinel",
			Title:           "MySQL Regex Sentinel",
			PageType:        types.WikiPageTypeEntity,
			Status:          types.WikiPageStatusPublished,
			Content:         "The business database is MySQL while retrieval remains in Qdrant.",
			Summary:         "MySQL business-primary integration page",
			Aliases:         types.StringArray{"Database Sentinel"},
			CategoryPath:    types.StringArray{"Databases", "MySQL"},
			WikiPath:        "entity/Databases/MySQL/MySQL Regex Sentinel",
			Depth:           2,
			SourceRefs:      types.StringArray{"mysql-source|Integration document"},
			ChunkRefs:       types.StringArray{},
			InLinks:         types.StringArray{},
			OutLinks:        types.StringArray{},
			PageMetadata:    types.JSON(`{"source":"integration"}`),
			Version:         1,
		},
		{
			ID:              wikiPageIDs[1],
			TenantID:        1,
			KnowledgeBaseID: wikiKBID,
			Slug:            "concept/qdrant-separation",
			Title:           "Qdrant Separation",
			PageType:        types.WikiPageTypeConcept,
			Status:          types.WikiPageStatusPublished,
			Content:         "External vector retrieval stays independent from the business database.",
			Summary:         "Qdrant retrieval integration page",
			Aliases:         types.StringArray{},
			CategoryPath:    types.StringArray{"Databases", "Qdrant"},
			WikiPath:        "concept/Databases/Qdrant/Qdrant Separation",
			Depth:           2,
			SourceRefs:      types.StringArray{},
			ChunkRefs:       types.StringArray{},
			InLinks:         types.StringArray{},
			OutLinks:        types.StringArray{},
			PageMetadata:    types.JSON(`{}`),
			Version:         1,
		},
	}
	for _, page := range wikiPages {
		if err := wikiRepo.Create(ctx, page); err != nil {
			t.Fatalf("create wiki page %s: %v", page.ID, err)
		}
	}

	searchResults, err := wikiRepo.Search(ctx, wikiKBID, "sentinel", 10)
	if err != nil || len(searchResults) != 1 || searchResults[0].ID != wikiPageIDs[0] {
		t.Fatalf("wiki regex search = %#v, err=%v", searchResults, err)
	}
	listResults, total, err := wikiRepo.List(ctx, &types.WikiPageListRequest{
		KnowledgeBaseID: wikiKBID,
		Query:           "database sentinel",
		CategoryPath:    types.StringArray{"Databases", "MySQL"},
		Page:            1,
		PageSize:        10,
	})
	if err != nil || total != 1 || len(listResults) != 1 || listResults[0].ID != wikiPageIDs[0] {
		t.Fatalf("wiki filtered list = %#v, total=%d, err=%v", listResults, total, err)
	}
	sourceResults, err := wikiRepo.ListBySourceRef(ctx, wikiKBID, "mysql-source")
	if err != nil || len(sourceResults) != 1 || sourceResults[0].ID != wikiPageIDs[0] {
		t.Fatalf("wiki source-ref lookup = %#v, err=%v", sourceResults, err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func assertTableExists(t *testing.T, db *sql.DB, table string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
		table,
	).Scan(&count); err != nil {
		t.Fatalf("inspect table %s: %v", table, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("table %s exists=%v, want %v", table, got, want)
	}
}

func assertBusinessTableCount(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name <> 'schema_migrations'",
	).Scan(&count); err != nil {
		t.Fatalf("count business tables: %v", err)
	}
	if count != want {
		t.Fatalf("business table count = %d, want %d", count, want)
	}
}
