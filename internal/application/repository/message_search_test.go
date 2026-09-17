package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SearchMessagesByKeyword runs on every supported main-database dialect —
// the Lite build serves POST /messages/search from SQLite. The query used to
// be Postgres-only ILIKE, which is a syntax error everywhere else.

func newMessageSearchDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}))
	return db
}

func TestSearchMessagesByKeywordMatchesOnSQLite(t *testing.T) {
	db := newMessageSearchDB(t)
	repo := NewMessageRepository(db)

	session := &types.Session{TenantID: 7, UserID: "web_user:alice", Title: "chat"}
	require.NoError(t, db.Create(session).Error)
	require.NoError(t, db.Create(&types.Message{
		SessionID: session.ID,
		Role:      "user",
		Content:   "How do I reset the DEVICE password?",
	}).Error)

	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "device", nil, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, session.ID, results[0].SessionID)
}

func TestSearchMessagesByKeywordTreatsWildcardsLiterally(t *testing.T) {
	db := newMessageSearchDB(t)
	repo := NewMessageRepository(db)

	session := &types.Session{TenantID: 7, UserID: "web_user:alice", Title: "chat"}
	require.NoError(t, db.Create(session).Error)
	require.NoError(t, db.Create(&types.Message{
		SessionID: session.ID,
		Role:      "user",
		Content:   "the discount is 100% today",
	}).Error)
	require.NoError(t, db.Create(&types.Message{
		SessionID: session.ID,
		Role:      "user",
		Content:   "no percent sign here",
	}).Error)

	// A keyword containing % must match the literal character, not act as a
	// LIKE wildcard. escapeLikeKeyword emits \%, which only works when the
	// query declares ESCAPE '\' — SQLite has no default escape character.
	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "100%", nil, 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Contains(t, results[0].Content, "100%")
}
