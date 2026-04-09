package notion

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeNotion builds an httptest.Server that emulates relevant Notion API endpoints.
func fakeNotion() (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	// GET /v1/users/me — auth check
	mux.HandleFunc("/v1/users/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(401)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":  401,
				"code":    "unauthorized",
				"message": "API token is invalid.",
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "user",
			"id":     "bot-user-id",
			"type":   "bot",
			"name":   "Test Integration",
		})
	})

	// POST /v1/search — return pages and databases
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"results": []interface{}{
				map[string]interface{}{
					"id":               "page-1",
					"object":           "page",
					"url":              "https://notion.so/Page-1",
					"last_edited_time": "2026-01-15T10:00:00.000Z",
					"in_trash":         false,
					"parent":           map[string]interface{}{"type": "workspace", "workspace": true},
					"properties": map[string]interface{}{
						"title": map[string]interface{}{
							"type": "title",
							"title": []interface{}{
								map[string]interface{}{"plain_text": "Test Page"},
							},
						},
					},
				},
				map[string]interface{}{
					"id":               "db-1",
					"object":           "data_source",
					"url":              "https://notion.so/DB-1",
					"last_edited_time": "2026-01-16T10:00:00.000Z",
					"in_trash":         false,
					"parent":           map[string]interface{}{"type": "workspace", "workspace": true},
					"title": []interface{}{
						map[string]interface{}{"plain_text": "Test Database"},
					},
				},
			},
			"has_more":    false,
			"next_cursor": nil,
		})
	})

	// GET /v1/blocks/{id}/children — return blocks
	mux.HandleFunc("/v1/blocks/page-1/children", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"results": []interface{}{
				map[string]interface{}{
					"id":           "blk-1",
					"type":         "paragraph",
					"has_children": false,
					"paragraph": map[string]interface{}{
						"rich_text": []interface{}{
							map[string]interface{}{
								"type":       "text",
								"plain_text": "Hello world",
								"text":       map[string]interface{}{"content": "Hello world"},
								"annotations": map[string]interface{}{
									"bold": false, "italic": false, "strikethrough": false,
									"underline": false, "code": false, "color": "default",
								},
							},
						},
					},
				},
				map[string]interface{}{
					"id":           "blk-2",
					"type":         "child_page",
					"has_children": true,
					"child_page":   map[string]interface{}{"title": "Sub Page"},
				},
			},
			"has_more":    false,
			"next_cursor": nil,
		})
	})

	// GET /v1/pages/{id}
	mux.HandleFunc("/v1/pages/page-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":               "page-1",
			"object":           "page",
			"url":              "https://notion.so/Page-1",
			"last_edited_time": "2026-01-15T10:00:00.000Z",
			"in_trash":         false,
			"parent":           map[string]interface{}{"type": "workspace", "workspace": true},
			"properties": map[string]interface{}{
				"title": map[string]interface{}{
					"type": "title",
					"title": []interface{}{
						map[string]interface{}{"plain_text": "Test Page"},
					},
				},
			},
		})
	})

	// GET /v1/databases/{id} — returns container with data_sources array (2025-09-03+)
	mux.HandleFunc("/v1/databases/db-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":               "db-1",
			"object":           "database",
			"url":              "https://notion.so/DB-1",
			"last_edited_time": "2026-01-16T10:00:00.000Z",
			"in_trash":         false,
			"parent":           map[string]interface{}{"type": "workspace", "workspace": true},
			"title":            []interface{}{map[string]interface{}{"plain_text": "Test Database"}},
			"data_sources": []interface{}{
				map[string]interface{}{"id": "ds-1", "name": "Default"},
			},
		})
	})

	// GET /v1/data_sources/{id} — returns schema/properties
	mux.HandleFunc("/v1/data_sources/ds-1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "ds-1",
				"object": "data_source",
				"properties": map[string]interface{}{
					"Name":   map[string]interface{}{"type": "title", "title": map[string]interface{}{}},
					"Status": map[string]interface{}{"type": "select", "select": map[string]interface{}{}},
				},
			})
			return
		}
		http.NotFound(w, r)
	})

	// POST /v1/data_sources/{id}/query — return database records (2025-09-03+)
	mux.HandleFunc("/v1/data_sources/ds-1/query", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object": "list",
			"results": []interface{}{
				map[string]interface{}{
					"id":               "record-1",
					"object":           "page",
					"url":              "https://notion.so/Record-1",
					"last_edited_time": "2026-01-17T10:00:00.000Z",
					"in_trash":         false,
					"parent":           map[string]interface{}{"type": "data_source_id", "data_source_id": "ds-1"},
					"properties": map[string]interface{}{
						"Name": map[string]interface{}{
							"type": "title",
							"title": []interface{}{
								map[string]interface{}{"plain_text": "Record One"},
							},
						},
						"Status": map[string]interface{}{
							"type":   "select",
							"select": map[string]interface{}{"name": "Done"},
						},
					},
				},
			},
			"has_more":    false,
			"next_cursor": nil,
		})
	})

	// GET /v1/pages/record-1 — direct fetch of a database row (single-select case).
	// fetchPage must recognize both database_id (older responses) and
	// data_source_id (2025-09-03+) as a record and route to buildRecordItem.
	mux.HandleFunc("/v1/pages/record-1", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":               "record-1",
			"object":           "page",
			"url":              "https://notion.so/Record-1",
			"last_edited_time": "2026-01-17T10:00:00.000Z",
			"in_trash":         false,
			"parent":           map[string]interface{}{"type": "data_source_id", "data_source_id": "ds-1"},
			"properties": map[string]interface{}{
				"Name": map[string]interface{}{
					"type": "title",
					"title": []interface{}{
						map[string]interface{}{"plain_text": "Record One"},
					},
				},
				"Status": map[string]interface{}{
					"type":   "select",
					"select": map[string]interface{}{"name": "Done"},
				},
			},
		})
	})

	// GET /v1/blocks/record-1/children — empty blocks for database record
	mux.HandleFunc("/v1/blocks/record-1/children", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"object":      "list",
			"results":     []interface{}{},
			"has_more":    false,
			"next_cursor": nil,
		})
	})

	ts := httptest.NewServer(mux)
	cfg := &Config{APIKey: "test-token"}
	return ts, cfg
}

func TestClientPing(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	client := newClient(cfg.APIKey, ts.URL)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestClientPing_InvalidToken(t *testing.T) {
	ts, _ := fakeNotion()
	defer ts.Close()

	client := newClient("wrong-token", ts.URL)
	err := client.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestClientSearchPages(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	client := newClient(cfg.APIKey, ts.URL)
	pages, err := client.SearchPages(context.Background())
	if err != nil {
		t.Fatalf("SearchPages() error: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	// First result is a page
	if pages[0].Object != "page" || pages[0].ID != "page-1" {
		t.Errorf("page[0] = %+v", pages[0])
	}
	// Second result is a data_source (databases are returned as object="data_source"
	// in API 2025-09-03+ which is what the mock simulates)
	if pages[1].Object != "data_source" || pages[1].ID != "db-1" {
		t.Errorf("page[1] = %+v", pages[1])
	}
}

func TestClientGetBlockChildrenAll(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	client := newClient(cfg.APIKey, ts.URL)
	blocks, err := client.GetBlockChildrenAll(context.Background(), "page-1")
	if err != nil {
		t.Fatalf("GetBlockChildrenAll() error: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "paragraph" {
		t.Errorf("block[0].Type = %q", blocks[0].Type)
	}
	if blocks[1].Type != "child_page" {
		t.Errorf("block[1].Type = %q", blocks[1].Type)
	}
	// child_page should NOT have its children recursed
	if blocks[1].Children != nil {
		t.Error("child_page children should not be recursed by client")
	}
}

func TestClientGetPage(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	client := newClient(cfg.APIKey, ts.URL)
	page, err := client.GetPage(context.Background(), "page-1")
	if err != nil {
		t.Fatalf("GetPage() error: %v", err)
	}
	if page.ID != "page-1" {
		t.Errorf("ID = %q", page.ID)
	}
	if page.Title != "Test Page" {
		t.Errorf("Title = %q, want %q", page.Title, "Test Page")
	}
}

func TestClientQueryDatabaseAll(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	client := newClient(cfg.APIKey, ts.URL)
	records, err := client.QueryDatabaseAll(context.Background(), "db-1")
	if err != nil {
		t.Fatalf("QueryDatabaseAll() error: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].ID != "record-1" {
		t.Errorf("record ID = %q", records[0].ID)
	}
}

// Ensure time import is used (referenced by fakeNotion indirectly via time.Time in types).
var _ = time.Now
