package repository

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestExpiredDocumentsKeepLiveConversationReferences(t *testing.T) {
	for _, backend := range []string{"sqlite", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			dialect := sqlite.Open(":memory:")
			if backend == "postgres" {
				dsn := os.Getenv("WEKNORA_REPOSITORY_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("set WEKNORA_REPOSITORY_TEST_POSTGRES_DSN for PostgreSQL attachment retention tests")
				}
				dialect = postgres.Open(dsn)
			}
			db, err := gorm.Open(dialect, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}, &types.TemporaryDocument{}))
			migration := "../../../migrations/sqlite/000036_messages_attachment_lookup.up.sql"
			if backend == "postgres" {
				migration = "../../../migrations/versioned/000117_messages_attachment_lookup.up.sql"
			}
			sql, err := os.ReadFile(migration)
			require.NoError(t, err)
			// PostgreSQL's concurrent index creation must run outside the cases'
			// transactions. Exercise the real migration, including idempotence.
			require.NoError(t, db.Exec(string(sql)).Error)
			require.NoError(t, db.Exec(string(sql)).Error)
			testExpiredDocumentReferences(t, db)
			t.Run("query plan", func(t *testing.T) { testExpiredDocumentQueryPlan(t, db) })
			t.Run("last reference removed", func(t *testing.T) { testExpiredDocumentReferenceRemoval(t, db) })
		})
	}
}

func testExpiredDocumentReferences(t *testing.T, db *gorm.DB) {
	for _, tc := range []struct {
		name          string
		sessionID     string
		tenantID      uint64
		deleteSession bool
		deleteMessage bool
		attachmentID  string
		role          string
		wantRetained  bool
	}{
		{name: "own conversation", sessionID: "parent", tenantID: 7, attachmentID: "document", wantRetained: true},
		{name: "fork without parent", sessionID: "fork", tenantID: 7, attachmentID: "document", wantRetained: true},
		{name: "deleted conversation", sessionID: "parent", tenantID: 7, attachmentID: "document", deleteSession: true},
		{name: "deleted message", sessionID: "parent", tenantID: 7, attachmentID: "document", deleteMessage: true},
		{name: "other workspace", sessionID: "foreign", tenantID: 8, attachmentID: "document"},
		{name: "different document", sessionID: "parent", tenantID: 7, attachmentID: "other-document"},
		{name: "assistant reference", sessionID: "parent", tenantID: 7, attachmentID: "document", role: "assistant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// No fixture data survives this case.
			db := db.Begin()
			require.NoError(t, db.Error)
			t.Cleanup(func() { require.NoError(t, db.Rollback().Error) })
			session := &types.Session{ID: tc.sessionID, TenantID: tc.tenantID}
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(session).Error)
			message := &types.Message{
				SessionID: session.ID, Role: "user",
				Attachments: types.MessageAttachments{{ID: tc.attachmentID}},
			}
			if tc.role != "" {
				message.Role = tc.role
			}
			require.NoError(t, db.Create(message).Error)
			if tc.deleteMessage {
				require.NoError(t, db.Delete(message).Error)
			}
			if tc.deleteSession {
				require.NoError(t, db.Delete(session).Error)
			}
			now := time.Now()
			for _, doc := range []*types.TemporaryDocument{
				{ID: "document", TenantID: 7, SessionID: "parent", ExpiresAt: now.Add(-2 * time.Hour)},
				{ID: "unsent", TenantID: 7, SessionID: "parent", ExpiresAt: now.Add(-time.Hour)},
				{ID: "fresh", TenantID: 7, SessionID: "parent", ExpiresAt: now.Add(time.Hour)},
			} {
				require.NoError(t, db.Create(doc).Error)
			}
			docs, err := NewTemporaryDocumentRepository(db).ListExpired(context.Background(), now, 1)
			require.NoError(t, err)
			require.Len(t, docs, 1, "retained rows must not starve cleanup with a small batch limit")
			if tc.wantRetained {
				require.Equal(t, "unsent", docs[0].ID)
			} else {
				require.Equal(t, "document", docs[0].ID)
			}
		})
	}
}

func testExpiredDocumentQueryPlan(t *testing.T, db *gorm.DB) {
	t.Helper()
	db = db.Begin()
	require.NoError(t, db.Error)
	t.Cleanup(func() { require.NoError(t, db.Rollback().Error) })
	session := &types.Session{ID: "plan-session", TenantID: 7}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(session).Error)
	var messages []*types.Message
	var documents []*types.TemporaryDocument
	now := time.Now()
	for i := range 2000 {
		id := fmt.Sprintf("plan-document-%d", i)
		messages = append(messages, &types.Message{
			ID: fmt.Sprintf("plan-message-%d", i), SessionID: session.ID, Role: "user",
			Attachments: types.MessageAttachments{{ID: id}},
		})
		messages = append(messages, &types.Message{
			ID: fmt.Sprintf("plan-assistant-%d", i), SessionID: session.ID, Role: "assistant",
		})
		if i < 200 {
			documents = append(documents, &types.TemporaryDocument{
				ID: id, TenantID: 7, SessionID: session.ID, ExpiresAt: now.Add(-time.Hour),
			})
		}
	}
	documents = append(documents, &types.TemporaryDocument{
		ID: "plan-unsent", TenantID: 7, SessionID: session.ID, ExpiresAt: now.Add(-time.Minute),
	})
	require.NoError(t, db.CreateInBatches(messages, 100).Error)
	require.NoError(t, db.CreateInBatches(documents, 100).Error)
	for _, table := range []string{"messages", "sessions", "temporary_documents"} {
		require.NoError(t, db.Exec("ANALYZE "+table).Error)
	}
	repository := &temporaryDocumentRepository{db: db}
	got, err := repository.ListExpired(context.Background(), now, 1)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "plan-unsent", got[0].ID)
	query := repository.expiredQuery(context.Background(), now, 1).
		Session(&gorm.Session{DryRun: true}).Find(&[]types.TemporaryDocument{}).Statement
	sql := db.Explain(query.SQL.String(), query.Vars...)
	if db.Name() == "postgres" {
		var plan string
		require.NoError(t, db.Raw("EXPLAIN (ANALYZE, FORMAT JSON) "+sql).Scan(&plan).Error)
		require.Contains(t, plan, "idx_messages_live_attachments", "cleanup must use indexed JSON containment")
		t.Log(plan)
	} else {
		var plan []struct{ Detail string }
		require.NoError(t, db.Raw("EXPLAIN QUERY PLAN "+sql).Scan(&plan).Error)
		var details []string
		for _, row := range plan {
			details = append(details, row.Detail)
		}
		joined := strings.Join(details, "\n")
		require.Contains(t, joined, "LIST SUBQUERY", "expand references once into a membership set")
		require.NotContains(t, joined, "CORRELATED", "do not rescan message JSON for each candidate")
		t.Log(joined)
	}
}

func testExpiredDocumentReferenceRemoval(t *testing.T, db *gorm.DB) {
	t.Helper()
	db = db.Begin()
	require.NoError(t, db.Error)
	t.Cleanup(func() { require.NoError(t, db.Rollback().Error) })
	for _, id := range []string{"parent", "fork"} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Session{ID: id, TenantID: 7}).Error)
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&types.Message{
			ID: id + "-message", SessionID: id, Role: "user", Attachments: types.MessageAttachments{{ID: "document"}},
		}).Error)
	}
	now := time.Now()
	require.NoError(t, db.Create(&types.TemporaryDocument{
		ID: "document", TenantID: 7, SessionID: "parent", ExpiresAt: now.Add(-time.Hour),
	}).Error)
	// Missing/null attachment IDs must not poison SQLite's NOT IN set.
	require.NoError(t, db.Exec("INSERT INTO messages (id, session_id, role, attachments) VALUES (?, ?, ?, ?)",
		"legacy", "fork", "user", `[{},{"id":null}]`).Error)
	require.NoError(t, db.Where("id = ?", "parent").Delete(&types.Session{}).Error)
	repo := NewTemporaryDocumentRepository(db)
	got, err := repo.ListExpired(context.Background(), now, 100)
	require.NoError(t, err)
	require.Empty(t, got, "the fork still keeps the document alive")
	require.NoError(t, NewMessageRepository(db).DeleteMessage(context.Background(), "fork", "fork-message"))
	got, err = repo.ListExpired(context.Background(), now, 100)
	require.NoError(t, err)
	require.Len(t, got, 1, "removing the final live reference returns the document to cleanup")
	require.Equal(t, "document", got[0].ID)
}
