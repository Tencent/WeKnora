package repository

import (
	"context"
	"os"
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
			testExpiredDocumentReferences(t, db)
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
		wantRetained  bool
	}{
		{name: "own conversation", sessionID: "parent", tenantID: 7, attachmentID: "document", wantRetained: true},
		{name: "fork without parent", sessionID: "fork", tenantID: 7, attachmentID: "document", wantRetained: true},
		{name: "deleted conversation", sessionID: "parent", tenantID: 7, attachmentID: "document", deleteSession: true},
		{name: "deleted message", sessionID: "parent", tenantID: 7, attachmentID: "document", deleteMessage: true},
		{name: "other workspace", sessionID: "foreign", tenantID: 8, attachmentID: "document"},
		{name: "different document", sessionID: "parent", tenantID: 7, attachmentID: "other-document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Roll back schema and data together: no changes survive this case.
			db := db.Begin()
			require.NoError(t, db.Error)
			t.Cleanup(func() { require.NoError(t, db.Rollback().Error) })
			require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}, &types.TemporaryDocument{}))
			session := &types.Session{ID: tc.sessionID, TenantID: tc.tenantID}
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(session).Error)
			message := &types.Message{
				SessionID: session.ID, Role: "user",
				Attachments: types.MessageAttachments{{ID: tc.attachmentID}},
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
