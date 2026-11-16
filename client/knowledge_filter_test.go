package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestFilterKnowledgeFileTypes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fileTypes []string
		want      string
	}{
		{name: "nil"},
		{name: "empty", fileTypes: []string{}},
		{name: "single", fileTypes: []string{"csv"}, want: "csv"},
		{name: "multiple", fileTypes: []string{"csv", "xlsx"}, want: "csv,xlsx"},
		{name: "reversed", fileTypes: []string{"xlsx", "csv"}, want: "xlsx,csv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/knowledge/search" {
					t.Errorf("request = %s %s, want GET /api/v1/knowledge/search", r.Method, r.URL.Path)
				}
				wantQuery := url.Values{
					"keyword":  {"budget & notes"},
					"offset":   {"3"},
					"limit":    {"20"},
					"agent_id": {"agent-1"},
				}
				if tc.want != "" {
					wantQuery.Set("file_types", tc.want)
				}
				if got := r.URL.Query(); !reflect.DeepEqual(got, wantQuery) {
					t.Errorf("query = %v, want %v", got, wantQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"data":[{"id":"knowledge-1"}],"has_more":true}`))
			}))
			defer srv.Close()

			c := NewClient(srv.URL)
			results, hasMore, err := c.FilterKnowledge(context.Background(), "budget & notes", 3, 20,
				tc.fileTypes, "agent-1")
			if err != nil {
				t.Fatalf("FilterKnowledge() error = %v", err)
			}
			if len(results) != 1 || results[0].ID != "knowledge-1" || !hasMore {
				t.Errorf("FilterKnowledge() = %v, %v; want knowledge-1 and has_more", results, hasMore)
			}
		})
	}
}
