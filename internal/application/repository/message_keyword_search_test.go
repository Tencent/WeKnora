package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SearchMessagesByKeyword used to emit `messages.content ILIKE ?` unconditionally —
// Postgres-only syntax that made the message search endpoint fail with a syntax
// error on every request on the SQLite build (issue #3324). These tests run on
// SQLite so a regression fails here instead of in a Lite deployment.

func newKeywordSearchDB(t *testing.T, name string) (*gorm.DB, map[string]string) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&types.Session{}, &types.Message{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	sessions := map[string]*types.Session{
		"alice": {TenantID: 7, UserID: "web_user:alice", Title: "Alice 的会话"},
		"bob":   {TenantID: 7, UserID: "web_user:bob", Title: "Bob 的会话"},
		"other": {TenantID: 8, UserID: "web_user:alice", Title: "别的工作区"},
	}
	ids := make(map[string]string, len(sessions))
	for label, session := range sessions {
		if err := db.Create(session).Error; err != nil {
			t.Fatalf("insert session: %v", err)
		}
		ids[label] = session.ID
	}
	messages := []*types.Message{
		{ID: "m-1", SessionID: ids["alice"], Role: "user", Content: "Hello WORLD from alice"},
		{ID: "m-2", SessionID: ids["alice"], Role: "assistant", Content: "unrelated answer"},
		{ID: "m-3", SessionID: ids["bob"], Role: "user", Content: "hello from bob"},
		{ID: "m-4", SessionID: ids["other"], Role: "user", Content: "hello from elsewhere"},
	}
	for _, m := range messages {
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("insert message: %v", err)
		}
	}
	return db, ids
}

func TestSearchMessagesByKeywordWorksOnSQLite(t *testing.T) {
	db, _ := newKeywordSearchDB(t, "kw-search-sqlite")
	repo := NewMessageRepository(db)

	// Before the dialect fix this call failed with `near "ILIKE": syntax error`.
	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "hello world", nil, 20)
	if err != nil {
		t.Fatalf("keyword search on sqlite: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1 (alice's message, case-insensitive)", len(results))
	}
	if results[0].Content != "Hello WORLD from alice" {
		t.Fatalf("content = %q", results[0].Content)
	}
	if results[0].SessionTitle != "Alice 的会话" {
		t.Fatalf("session title = %q, want joined title", results[0].SessionTitle)
	}
}

func TestSearchMessagesByKeywordScopesOwnerAndTenant(t *testing.T) {
	db, _ := newKeywordSearchDB(t, "kw-search-scope")
	repo := NewMessageRepository(db)

	// Owner scoping: bob's and legacy-free sessions stay out of alice's results.
	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "hello", nil, 20)
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	if len(results) != 1 || results[0].Content != "Hello WORLD from alice" {
		t.Fatalf("owner scope leaked: %+v", results)
	}

	// Tenant scoping: same keyword, foreign tenant stays invisible.
	results, err = repo.SearchMessagesByKeyword(
		context.Background(), 8, "web_user:alice", "hello", nil, 20)
	if err != nil {
		t.Fatalf("keyword search: %v", err)
	}
	if len(results) != 1 || results[0].Content != "hello from elsewhere" {
		t.Fatalf("tenant scope wrong: %+v", results)
	}
}

func TestSearchMessagesByKeywordNoMatchIsNotAnError(t *testing.T) {
	db, _ := newKeywordSearchDB(t, "kw-search-empty")
	repo := NewMessageRepository(db)

	results, err := repo.SearchMessagesByKeyword(
		context.Background(), 7, "web_user:alice", "zebra", nil, 20)
	if err != nil {
		t.Fatalf("keyword search without matches must not error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %d, want 0", len(results))
	}
}
