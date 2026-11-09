package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type sqliteSearchService struct {
	interfaces.WikiPageService
	repo interfaces.WikiPageRepository
}

func (s sqliteSearchService) SearchPages(
	ctx context.Context, kbID, query string, limit int,
) ([]*types.WikiPage, error) {
	return s.repo.Search(ctx, kbID, query, limit)
}

func (s sqliteSearchService) SearchPagesLiteral(
	ctx context.Context, kbID, query string, limit int,
) ([]*types.WikiPage, error) {
	return s.repo.SearchLiteral(ctx, kbID, query, limit)
}

func newPublishedPage(id, kbID, slug, title, content string) *types.WikiPage {
	now := time.Now()
	page := &types.WikiPage{}
	page.ID = id
	page.TenantID = 1
	page.KnowledgeBaseID = kbID
	page.Slug = slug
	page.Title = title
	page.Content = content
	page.PageType = types.WikiPageTypeConcept
	page.Status = types.WikiPageStatusPublished
	page.CreatedAt = now
	page.UpdatedAt = now
	return page
}

func TestWikiSearchLiteralSQLiteIntegration(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&types.WikiPage{}); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewWikiPageRepository(db)
	values := []string{
		"C++", "a.b", "pipe|value", "normalword", "100%", "a_b", `dir\file`, "x' OR 1=1 --",
	}
	for i, value := range values {
		page := newPublishedPage(
			fmt.Sprintf("id-%d", i), "kb-1", fmt.Sprintf("concept/item-%d", i),
			value, "How to use "+value,
		)
		if err := repo.Create(context.Background(), page); err != nil {
			t.Fatal(err)
		}
	}
	// A matching title in another tenant and KB must not enter the allowed scope.
	outsider := newPublishedPage("outside", "kb-2", "concept/outside", "C++", "C++")
	outsider.TenantID = 2
	if err := repo.Create(context.Background(), outsider); err != nil {
		t.Fatal(err)
	}
	// Prevent wildcard or regex semantics from matching a decoy.
	decoy := newPublishedPage("decoy", "kb-1", "concept/decoy", "axb", "axb")
	if err := repo.Create(context.Background(), decoy); err != nil {
		t.Fatal(err)
	}
	tool := NewWikiSearchTool(
		sqliteSearchService{repo: repo}, nil,
		NewWikiScopesFromKBIDs([]string{"kb-1"}), NewWikiRouteResolver(),
	)
	for i, value := range values {
		t.Run(value, func(t *testing.T) {
			arg, _ := json.Marshal(map[string]any{"query": value, "regex": false})
			got, err := tool.Execute(context.Background(), arg)
			if err != nil || !got.Success ||
				!strings.Contains(got.Output, fmt.Sprintf("[[concept/item-%d|", i)) ||
				!strings.Contains(got.Output, `<search_results count="1"`) ||
				strings.Contains(got.Output, "concept/outside") ||
				strings.Contains(got.Output, "concept/decoy") {
				t.Fatalf("query=%q got=%+v err=%v", value, got, err)
			}
		})
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"C++"}`))
	if err != nil || !result.Success || !strings.Contains(result.Output, "[[concept/item-0|") {
		t.Fatalf("automatic literal fallback: result=%+v err=%v", result, err)
	}
	// SQLite's existing explicit-regex degradation remains literal LIKE.
	result, err = tool.Execute(
		context.Background(), json.RawMessage(`{"query":"a.b","regex":true}`),
	)
	if err != nil || !result.Success ||
		!strings.Contains(result.Output, "[[concept/item-1|") ||
		strings.Contains(result.Output, "concept/decoy") {
		t.Fatalf("SQLite existing regex behavior changed: result=%+v err=%v", result, err)
	}
}
