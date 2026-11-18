package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type kbListTestService struct {
	interfaces.KnowledgeBaseService
	rows     []*types.KnowledgeBase
	enriched int
}

func (s *kbListTestService) ListKnowledgeBasesForDashboard(
	_ context.Context, selectRows func([]*types.KnowledgeBase) []*types.KnowledgeBase,
) ([]*types.KnowledgeBase, error) {
	selected := selectRows(s.rows)
	s.enriched = len(selected)
	return selected, nil
}

func (s *kbListTestService) ListKnowledgeBases(_ context.Context) ([]*types.KnowledgeBase, error) {
	return s.rows, nil
}

func TestListKnowledgeBasesDashboardPage(t *testing.T) {
	svc := &kbListTestService{rows: []*types.KnowledgeBase{
		{ID: "outside-scope", CreatorID: "user"},
		{ID: "visible-first", CreatorID: "user", IsPinned: true},
		{ID: "other-creator", CreatorID: "other"},
		{ID: "visible-second", CreatorID: "user", ExtractConfig: &types.ExtractConfig{Enabled: true}},
		{ID: "legacy"},
	}}
	h := &KnowledgeBaseHandler{service: svc}
	router := gin.New()
	router.GET("/knowledge-bases", func(c *gin.Context) {
		c.Set(types.UserIDContextKey.String(), "user")
		scope := types.TenantAPIKeyScope{KnowledgeBaseIDs: types.StringArray{
			"visible-first", "visible-second", "other-creator", "legacy",
		}}
		c.Request = c.Request.WithContext(types.WithTenantAPIKeyScope(c.Request.Context(), scope))
		h.ListKnowledgeBases(c)
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet,
		"/knowledge-bases?view=list&creator=mine&page=2&page_size=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body)
	}
	var result struct {
		Data       []map[string]json.RawMessage `json:"data"`
		Pagination kbListPage                   `json:"pagination"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Pagination != (kbListPage{Page: 2, PageSize: 1, Total: 2}) {
		t.Fatalf("pagination = %+v", result.Pagination)
	}
	if svc.enriched != 1 || len(result.Data) != 1 {
		t.Fatalf("enriched %d rows, response has %d", svc.enriched, len(result.Data))
	}
	card := result.Data[0]
	if string(card["id"]) != `"visible-second"` || string(card["extract_config"]) != `{"enabled":true}` {
		t.Fatalf("unexpected card: %v", card)
	}
	if _, ok := card["chunking_config"]; ok {
		t.Fatal("dashboard card includes full chunking configuration")
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/knowledge-bases", nil))
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &legacy); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || legacy["pagination"] != nil {
		t.Fatalf("legacy response changed: %s", response.Body)
	}
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal(legacy["data"], &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0]["chunking_config"] == nil {
		t.Fatalf("legacy list must still return all visible rows with full configuration: %s", response.Body)
	}
}

func TestKBListPageBounds(t *testing.T) {
	if _, err := parseKBListPage("0", "1"); err == nil {
		t.Fatal("expected page 0 to be rejected")
	}
	if _, err := parseKBListPage("1", "201"); err == nil {
		t.Fatal("expected page_size above 200 to be rejected")
	}
	rows := []*types.KnowledgeBase{{ID: "first"}, {ID: "last"}}
	p, err := parseKBListPage("2147483647", "200")
	if err != nil {
		t.Fatal(err)
	}
	if selected := p.slice(rows); len(selected) != 0 || p.Total != 2 {
		t.Fatalf("out-of-range page returned %d rows, total = %d", len(selected), p.Total)
	}
}
