package notion

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func TestFetchIncrementalSearchFailure(t *testing.T) {
	for _, failPage := range []int{1, 2} {
		t.Run("page_"+strconv.Itoa(failPage), func(t *testing.T) {
			allowNotionTestServer(t)
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/v1/search" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if requests == failPage {
					http.Error(w, "access revoked", http.StatusUnauthorized)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"results": []interface{}{}, "has_more": true, "next_cursor": "second-page",
				})
			}))
			defer server.Close()
			config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page-1"})
			cursor := buildCursor(map[string]time.Time{"page-1": time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)})
			items, next, err := NewConnector().FetchIncremental(context.Background(), config, cursor)
			if !errors.Is(err, datasource.ErrInvalidCredentials) {
				t.Errorf("expected credential error, got %v", err)
			}
			if len(items) != 0 || next != nil {
				t.Errorf("failed discovery must return no items or cursor, got items=%+v cursor=%+v", items, next)
			}
			if requests != failPage {
				t.Errorf("expected %d requests, got %d", failPage, requests)
			}
		})
	}
}

func TestFetchIncrementalSuccessfulEmptySearch(t *testing.T) {
	allowNotionTestServer(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"results": []interface{}{}, "has_more": false,
		})
	}))
	defer server.Close()
	config := makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page-1"})
	cursor := buildCursor(map[string]time.Time{"page-1": time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)})
	items, next, err := NewConnector().FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatalf("expected successful discovery, got %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "page-1" || !items[0].IsDeleted {
		t.Fatalf("expected deletion of absent page-1, got %+v", items)
	}
	if next == nil {
		t.Fatal("expected updated cursor after successful discovery")
	}
}
