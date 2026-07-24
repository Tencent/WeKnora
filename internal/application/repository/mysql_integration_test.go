//go:build mysql_integration

// Package repository contains integration tests that exercise dialect-specific
// SQL branches against a real MySQL 8.0 database.
//
// These tests require a MySQL instance accessible via environment variables
// (DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME). If none is set, the
// tests gracefully skip.
//
// Quick start with test containers:
//
//	docker compose -f docker-compose.mysql.yml --profile mysql-test up -d mysql redis
//	go test -v -run TestMySQL ./internal/application/repository/ -tags=mysql_integration
//
// Or with an existing MySQL:
//
//	DB_HOST=127.0.0.1 DB_PORT=3306 DB_USER=root DB_PASSWORD=pass DB_NAME=WeKnora_test \
//	  go test -v -run TestMySQL ./internal/application/repository/ -tags=mysql_integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	mysqlConfig "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// mysqlDSN constructs a GORM DSN from environment variables.
func mysqlDSN() string {
	host := envOrDefault("DB_HOST", "127.0.0.1")
	port := envOrDefault("DB_PORT", "3306")
	user := envOrDefault("DB_USER", "root")
	pass := envOrDefault("DB_PASSWORD", "weknora_test")
	name := envOrDefault("DB_NAME", "WeKnora")

	cfg := mysqlConfig.NewConfig()
	cfg.Net = "tcp"
	cfg.Addr = host + ":" + port
	cfg.User = user
	cfg.Passwd = pass
	cfg.DBName = name
	cfg.Params = map[string]string{
		"charset":   "utf8mb4",
		"parseTime": "true",
		"loc":       "UTC",
	}
	cfg.InterpolateParams = true
	return cfg.FormatDSN()
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// setupMySQLTestDB opens a real MySQL connection, migrates the schema, and
// returns the gorm.DB. Tests that call this are serialised by the package-
// level mutex because they share the same physical database.
//
//nolint:unparam
func setupMySQLTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := mysqlDSN()
	rawDB, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Skipf("MySQL not available (DSN %s): %v", maskPassword(dsn), err)
	}
	if err := rawDB.Ping(); err != nil {
		t.Skipf("MySQL not reachable (DSN %s): %v", maskPassword(dsn), err)
	}
	rawDB.Close()

	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	require.NoError(t, err)

	// Migrate all core types
	require.NoError(t, db.AutoMigrate(
		&types.Tenant{},
		&types.User{},
		&types.KnowledgeBase{},
		&types.Chunk{},
		&types.KnowledgeTag{},
		&types.Message{},
		&types.Session{},
		&types.SystemSetting{},
		&types.TaskPendingOp{},
		&types.Organization{},
		&types.WikiPage{},
		&types.DataSource{},
		&types.SyncLog{},
	))

	return db
}

func maskPassword(dsn string) string {
	// Replace password in "user:pass@tcp(host:port)/dbname" DSN
	if idx := strings.Index(dsn, ":"); idx > 0 {
		if atIdx := strings.Index(dsn, "@"); atIdx > idx {
			return dsn[:idx+1] + "****" + dsn[atIdx:]
		}
	}
	return dsn
}

// ---------------------------------------------------------------------------
// SystemSettingRepository — tests reserved word handling for "key" column
// ---------------------------------------------------------------------------

func TestMySQL_SystemSettingKey(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewSystemSettingRepository(db)
	ctx := context.Background()

	// Upsert and Get by "key" — tests clause.Column quoting
	s := &types.SystemSetting{
		Key:   "mysql_test_key_" + uuid.New().String()[:8],
		Value: types.JSON(`"hello"`),
	}
	err := repo.Upsert(ctx, s)
	require.NoError(t, err, "Upsert with key=%q should not fail on MySQL reserved word", s.Key)

	got, err := repo.Get(ctx, s.Key)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, s.Key, got.Key)

	// List with ORDER BY "key"
	list, err := repo.List(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, list)

	// Delete by "key"
	deleted, err := repo.Delete(ctx, s.Key)
	require.NoError(t, err)
	assert.True(t, deleted, "Delete should return true for existing key")
}

// ---------------------------------------------------------------------------
// OrganizationRepository — tests LOWER LIKE fallback for ILIKE
// ---------------------------------------------------------------------------

func TestMySQL_OrganizationSearch(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewOrganizationRepository(db)
	ctx := context.Background()

	org := &types.Organization{
		Name:        "MySQL_Test_Org_" + uuid.New().String()[:8],
		Description: "测试组织 for MySQL integration",
		TenantID:    1,
		Searchable:  true,
	}
	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Case-insensitive search via LOWER LIKE
	results, err := repo.ListSearchable(ctx, "mysql_test_org", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results, "should find org by lowercase name")
}

// ---------------------------------------------------------------------------
// UserRepository — tests LOWER LIKE fallback for user search
// ---------------------------------------------------------------------------

func TestMySQL_UserSearch(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewUserRepository(db)
	ctx := context.Background()

	user := &types.User{
		ID:       uuid.New().String(),
		Username: "mysql_test_user_" + uuid.New().String()[:8],
		Email:    "mysql_test_" + uuid.New().String()[:8] + "@example.com",
		IsActive: true,
		Source:   "password",
	}
	err := repo.Create(ctx, user)
	require.NoError(t, err)

	// Search by username (case-insensitive)
	results, err := repo.SearchUsers(ctx, "MYSQL_TEST_USER", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results, "should find user by case-insensitive username")

	// Search by email (case-insensitive)
	results, err = repo.SearchUsers(ctx, "MYSQL_TEST", 10)
	require.NoError(t, err)
	assert.NotEmpty(t, results, "should find user by case-insensitive email")
}

// ---------------------------------------------------------------------------
// TaskPendingOpsRepository — tests RETURNING alternative (UPDATE + SELECT)
// ---------------------------------------------------------------------------

func TestMySQL_TaskIncrFailCount(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	op := &types.TaskPendingOp{
		OpName:  "mysql_test",
		Payload: []byte(`{"test":true}`),
	}
	err := repo.Create(ctx, op)
	require.NoError(t, err)
	require.NotZero(t, op.ID, "ID should be auto-assigned")

	// Increment fail count — uses transaction (UPDATE + SELECT) on MySQL
	newCount, err := repo.IncrFailCount(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, newCount, "fail_count should be 1 after first increment")

	newCount, err = repo.IncrFailCount(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, newCount, "fail_count should be 2 after second increment")
}

// ---------------------------------------------------------------------------
// MessageRepository — tests LOWER LIKE search fallback
// ---------------------------------------------------------------------------

func TestMySQL_MessageSearchByKeyword(t *testing.T) {
	db := setupMySQLTestDB(t)

	// Create a session with a thread
	session := &types.Session{
		ID:       uuid.New().String(),
		TenantID: 1,
		Title:    "MySQL Test Session",
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(session).Error)

	msg := &types.Message{
		ID:        uuid.New().String(),
		SessionID: session.ID,
		Role:      "user",
		Content:   "Hello MySQL 集成测试消息",
		CreatedAt: time.Now(),
	}
	require.NoError(t, db.Create(msg).Error)

	repo := NewMessageRepository(db)
	ctx := context.Background()

	// Search by keyword (case-insensitive, different case)
	results, raw, err := repo.SearchMessagesByKeyword(ctx, 1, "mysql 集成", nil, 10, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, results, "should find messages by case-insensitive keyword")
	assert.Equal(t, int64(1), raw, "should have 1 raw match")

	// Also verify session title is joined correctly
	assert.Equal(t, "MySQL Test Session", results[0].SessionTitle)
}

// ---------------------------------------------------------------------------
// KnowledgeRepository — tests JSON_UNQUOTE(JSON_EXTRACT) fallback
// ---------------------------------------------------------------------------

func TestMySQL_KnowledgeFindByMetadataKey(t *testing.T) {
	db := setupMySQLTestDB(t)

	kb := &types.KnowledgeBase{
		ID:            uuid.New().String(),
		TenantID:      1,
		KnowledgeBaseID: uuid.New().String(),
		Name:          "MySQL Test KB",
	}
	require.NoError(t, db.Create(kb).Error)

	repo := NewKnowledgeRepository(db)
	ctx := context.Background()

	// Test FindByMetadataKey on an existing knowledge base
	found, err := repo.FindByMetadataKey(ctx, 1, kb.KnowledgeBaseID, "provider", "__pending_env__")
	require.NoError(t, err)
	// May be nil if no metadata with that key — that's fine, the test is
	// that the query doesn't error (tests JSON_UNQUOTE(JSON_EXTRACT(...)) syntax)
	t.Logf("FindByMetadataKey result: %v", found)
}

// ---------------------------------------------------------------------------
// WikiPageRepository — tests JSON_CONTAINS, LOWER LIKE, REGEXP, CAST('[]' AS JSON)
// ---------------------------------------------------------------------------

func TestMySQL_WikiPageJSONOperations(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewWikiPageRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	slug := "mysql-test-" + uuid.New().String()[:8]

	page := &types.WikiPage{
		ID:             uuid.New().String(),
		KnowledgeBaseID: kbID,
		Slug:           slug,
		Title:          "MySQL Integration Test Page",
		PageType:       types.WikiPageTypeDoc,
		Status:         types.WikiPageStatusPublished,
		Content:        "This page tests MySQL compatibility for JSON operations.",
		SourceRefs:     types.StringArray{`["` + kbID + `"]`},
		InLinks:        types.StringArray{},
	}
	require.NoError(t, repo.Create(ctx, page))

	t.Run("ListBySourceRef uses JSON_CONTAINS", func(t *testing.T) {
		pages, err := repo.ListBySourceRef(ctx, kbID, kbID)
		require.NoError(t, err)
		assert.NotEmpty(t, pages, "should find page by source ref JSON_CONTAINS")
	})

	t.Run("CountOrphans uses CAST('[]' AS JSON)", func(t *testing.T) {
		count, err := repo.CountOrphans(ctx, kbID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, int64(1), "page with empty in_links should count as orphan")
	})

	t.Run("Search uses REGEXP operator", func(t *testing.T) {
		pages, err := repo.Search(ctx, kbID, "MySQL", 10)
		require.NoError(t, err)
		assert.NotEmpty(t, pages, "should find page by REGEXP search")
	})

	t.Run("FindSimilarPages uses LIKE fallback", func(t *testing.T) {
		pages, err := repo.FindSimilarPages(ctx, kbID, "mysql", []string{types.WikiPageTypeDoc}, 10)
		require.NoError(t, err)
		assert.NotEmpty(t, pages, "should find similar page by LIKE fallback")
	})
}

// ---------------------------------------------------------------------------
// SystemSettingRepository — tests ORDER BY "key" reserved word and DELETE
// ---------------------------------------------------------------------------

func TestMySQL_SystemSettingSortAndDelete(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewSystemSettingRepository(db)
	ctx := context.Background()

	// Insert settings with keys that sort differently
	keys := []string{"zzz_last", "aaa_first", "mmm_middle"}
	for _, k := range keys {
		err := repo.Upsert(ctx, &types.SystemSetting{
			Key:   "mysql_" + k,
			Value: types.JSON(fmt.Sprintf(`"%s"`, k)),
		})
		require.NoError(t, err)
	}

	// List should not error (tests ORDER BY "key")
	list, err := repo.List(ctx)
	require.NoError(t, err)
	assert.NotEmpty(t, list)

	// Delete each setting
	for _, k := range keys {
		deleted, err := repo.Delete(ctx, "mysql_"+k)
		require.NoError(t, err)
		assert.True(t, deleted, "should delete setting %q", "mysql_"+k)
	}
}

// ---------------------------------------------------------------------------
// SyncLogRepository — tests DATE_SUB interval syntax
// ---------------------------------------------------------------------------

func TestMySQL_SyncLogCleanup(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewSyncLogRepository(db)
	ctx := context.Background()

	// Create a stale sync log
	log := &types.SyncLog{
		KnowledgeBaseID: uuid.New().String(),
		Status:          "success",
		StartedAt:       time.Now().Add(-90 * 24 * time.Hour), // 90 days ago
	}
	require.NoError(t, repo.Create(ctx, log))

	// Cleanup logs older than 30 days — uses DATE_SUB(NOW(), INTERVAL ? DAY)
	err := repo.CleanupOldLogs(ctx, 30)
	require.NoError(t, err, "CleanupOldLogs should work with MySQL DATE_SUB syntax")
}

// ---------------------------------------------------------------------------
// ChunkRepository — tests MySQL CASE expressions and FAQ duplicate check
// ---------------------------------------------------------------------------

func TestMySQL_ChunkCRUD(t *testing.T) {
	db := setupMySQLTestDB(t)
	repo := NewChunkRepository(db)
	ctx := context.Background()

	kbID := uuid.New().String()
	knowledgeID := uuid.New().String()

	// Create chunks
	chunks := []*types.Chunk{
		{
			ID:              uuid.New().String(),
			TenantID:        1,
			KnowledgeBaseID: kbID,
			KnowledgeID:     knowledgeID,
			Content:         "MySQL test chunk 1",
			ChunkType:       "faq",
			IsEnabled:       true,
			Status:          1,
		},
		{
			ID:              uuid.New().String(),
			TenantID:        1,
			KnowledgeBaseID: kbID,
			KnowledgeID:     knowledgeID,
			Content:         "MySQL test chunk 2",
			ChunkType:       "faq",
			IsEnabled:       true,
			Status:          1,
		},
	}
	err := repo.CreateChunks(ctx, chunks)
	require.NoError(t, err)

	// Verify chunks have seq_id assigned
	var saved []types.Chunk
	require.NoError(t, db.Where("knowledge_base_id = ?", kbID).Order("seq_id").Find(&saved).Error)
	assert.Len(t, saved, 2)
	for i, c := range saved {
		assert.Equal(t, int64(i+1), c.SeqID, "seq_id should be sequential")
	}

	// Update chunks with MySQL CASE expressions
	chunks[0].Content = "Updated MySQL test chunk 1"
	chunks[0].IsEnabled = false
	chunks[0].Flags = 42
	chunks[1].Content = "Updated MySQL test chunk 2"
	err = repo.UpdateChunks(ctx, chunks)
	require.NoError(t, err, "UpdateChunks with MySQL CASE expressions should work")

	// Verify updates
	var updated []types.Chunk
	require.NoError(t, db.Where("knowledge_base_id = ?", kbID).Find(&updated).Error)
	for _, c := range updated {
		if c.ID == chunks[0].ID {
			assert.Equal(t, "Updated MySQL test chunk 1", c.Content)
			assert.False(t, c.IsEnabled)
			assert.Equal(t, 42, c.Flags)
		} else {
			assert.Equal(t, "Updated MySQL test chunk 2", c.Content)
		}
	}
}
