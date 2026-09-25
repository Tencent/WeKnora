package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestParseOffsetPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		query       string
		wantOffset  int
		wantLimit   int
		wantOK      bool
		wantMessage string
	}{
		{name: "defaults", wantOffset: 0, wantLimit: 20, wantOK: true},
		{name: "valid", query: "?offset=40&limit=50", wantOffset: 40, wantLimit: 50, wantOK: true},
		{name: "invalid offset", query: "?offset=not-a-number", wantMessage: "offset must be a non-negative integer"},
		{name: "negative offset", query: "?offset=-1", wantMessage: "offset must be a non-negative integer"},
		{name: "invalid limit", query: "?limit=not-a-number", wantMessage: "limit must be between 1 and 100"},
		{name: "zero limit", query: "?limit=0", wantMessage: "limit must be between 1 and 100"},
		{name: "limit too large", query: "?limit=101", wantMessage: "limit must be between 1 and 100"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/knowledge/search"+tt.query, nil)

			offset, limit, ok := parseOffsetPagination(c)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if offset != tt.wantOffset || limit != tt.wantLimit {
				t.Fatalf("got offset=%d limit=%d, want offset=%d limit=%d", offset, limit, tt.wantOffset, tt.wantLimit)
			}
			if tt.wantOK {
				if len(c.Errors) != 0 {
					t.Fatalf("unexpected errors: %v", c.Errors)
				}
				return
			}
			if len(c.Errors) != 1 {
				t.Fatalf("got %d errors, want 1", len(c.Errors))
			}
			if !strings.Contains(c.Errors[0].Error(), tt.wantMessage) {
				t.Fatalf("error = %q, want message containing %q", c.Errors[0].Error(), tt.wantMessage)
			}
		})
	}
}

func TestSearchKnowledgeRejectsInvalidPagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, query := range []string{
		"?keyword=test&offset=-1",
		"?keyword=test&limit=101",
	} {
		t.Run(query, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("GET", "/knowledge/search"+query, nil)

			(&KnowledgeHandler{}).SearchKnowledge(c)

			if len(c.Errors) != 1 {
				t.Fatalf("got %d errors, want 1", len(c.Errors))
			}
		})
	}
}
