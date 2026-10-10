package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

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
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}, &types.MessageArtifactRecord{}))
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

func TestSearchMessagesByKeywordExcludesSessionsBeforeLimit(t *testing.T) {
	db := newMessageSearchDB(t)
	repo := NewMessageRepository(db)
	current := createKeywordSearchSession(t, db, 7, "web_user:alice")
	history := createKeywordSearchSession(t, db, 7, "web_user:alice")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// More current-session hits than the largest search limit used by the
	// history tool must not crowd out older conversations before filtering.
	for i := 0; i < 70; i++ {
		createKeywordSearchMessage(t, db, current.ID, "device current-only", base.Add(time.Duration(100+i)*time.Minute))
	}
	var historyMessages []*types.Message
	for i := 0; i < 25; i++ {
		historyMessages = append(historyMessages,
			createKeywordSearchMessage(t, db, history.ID, "device history", base.Add(time.Duration(i)*time.Minute)))
	}

	for _, limit := range []int{1, 5, 20} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			results, err := repo.SearchMessagesByKeyword(
				context.Background(), 7, "web_user:alice", "device", nil, limit, current.ID)
			require.NoError(t, err)
			require.Len(t, results, limit)
			for i, result := range results {
				require.Equal(t, history.ID, result.SessionID)
				require.Equal(t, historyMessages[len(historyMessages)-1-i].ID, result.ID)
			}
		})
	}

	t.Run("current_only_keyword_returns_empty", func(t *testing.T) {
		results, err := repo.SearchMessagesByKeyword(
			context.Background(), 7, "web_user:alice", "current-only", nil, 20, current.ID)
		require.NoError(t, err)
		require.Empty(t, results)
	})

	t.Run("omitted_exclusion_preserves_current_session_matches", func(t *testing.T) {
		results, err := repo.SearchMessagesByKeyword(
			context.Background(), 7, "web_user:alice", "device", nil, 20)
		require.NoError(t, err)
		require.Len(t, results, 20)
		for _, result := range results {
			require.Equal(t, current.ID, result.SessionID)
		}
	})
}

func TestSearchMessagesByKeywordExclusionIntersectsSessionAllowlist(t *testing.T) {
	db := newMessageSearchDB(t)
	repo := NewMessageRepository(db)
	current := createKeywordSearchSession(t, db, 7, "web_user:alice")
	allowed := createKeywordSearchSession(t, db, 7, "web_user:alice")
	outside := createKeywordSearchSession(t, db, 7, "web_user:alice")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	createKeywordSearchMessage(t, db, current.ID, "device", base.Add(2*time.Minute))
	createKeywordSearchMessage(t, db, outside.ID, "device", base.Add(time.Minute))
	want := createKeywordSearchMessage(t, db, allowed.ID, "device", base)

	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "device", []string{current.ID, allowed.ID}, 1, current.ID)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, want.ID, results[0].ID)

	results, err = repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "device", []string{current.ID}, 20, current.ID)
	require.NoError(t, err)
	require.Empty(t, results)

	// The variadic exclusion can remove several sessions without changing the
	// allowlist's meaning or admitting matches from outside that allowlist.
	results, err = repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "device", []string{current.ID, allowed.ID}, 20,
		current.ID, allowed.ID)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestSearchMessagesByKeywordExclusionPreservesOwnershipAndLegacyScope(t *testing.T) {
	db := newMessageSearchDB(t)
	repo := NewMessageRepository(db)
	current := createKeywordSearchSession(t, db, 7, "web_user:alice")
	own := createKeywordSearchSession(t, db, 7, "web_user:alice")
	legacyEmpty := createKeywordSearchSession(t, db, 7, "")
	legacyNull := createKeywordSearchSession(t, db, 7, "")
	require.NoError(t, db.Model(legacyNull).Update("user_id", nil).Error)
	otherUser := createKeywordSearchSession(t, db, 7, "web_user:bob")
	otherTenant := createKeywordSearchSession(t, db, 8, "web_user:alice")
	otherTenantLegacy := createKeywordSearchSession(t, db, 8, "")
	deletedSession := createKeywordSearchSession(t, db, 7, "web_user:alice")
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	var wantIDs []string
	for i, session := range []*types.Session{own, legacyEmpty, legacyNull} {
		message := createKeywordSearchMessage(t, db, session.ID, "device", base.Add(time.Duration(i)*time.Minute))
		wantIDs = append(wantIDs, message.ID)
	}
	for i, session := range []*types.Session{current, otherUser, otherTenant, otherTenantLegacy, deletedSession} {
		createKeywordSearchMessage(t, db, session.ID, "device", base.Add(time.Duration(100+i)*time.Minute))
	}
	require.NoError(t, db.Delete(deletedSession).Error)
	deletedMessage := createKeywordSearchMessage(t, db, own.ID, "device", base.Add(200*time.Minute))
	require.NoError(t, db.Delete(deletedMessage).Error)

	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "device", nil, 20, current.ID)
	require.NoError(t, err)
	var gotIDs []string
	for _, result := range results {
		gotIDs = append(gotIDs, result.ID)
	}
	require.ElementsMatch(t, wantIDs, gotIDs)
}

func createKeywordSearchSession(t *testing.T, db *gorm.DB, tenantID uint64, ownerID string) *types.Session {
	t.Helper()
	session := &types.Session{TenantID: tenantID, UserID: ownerID, Title: "keyword search"}
	require.NoError(t, db.Create(session).Error)
	return session
}

func createKeywordSearchMessage(
	t *testing.T, db *gorm.DB, sessionID, content string, createdAt time.Time,
) *types.Message {
	t.Helper()
	message := &types.Message{SessionID: sessionID, Role: "user", Content: content, CreatedAt: createdAt}
	require.NoError(t, db.Create(message).Error)
	return message
}
