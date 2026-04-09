package notion

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func makeNotionConfig(cfg *Config, baseURL string, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeNotion,
		Credentials: map[string]interface{}{
			"api_key": cfg.APIKey,
		},
		ResourceIDs: resourceIDs,
		Settings: map[string]interface{}{
			"base_url": baseURL,
		},
	}
}

func TestConnectorType(t *testing.T) {
	c := NewConnector()
	if c.Type() != types.ConnectorTypeNotion {
		t.Errorf("Type() = %q, want %q", c.Type(), types.ConnectorTypeNotion)
	}
}

func TestConnectorValidate(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	err := c.Validate(context.Background(), makeNotionConfig(cfg, ts.URL, nil))
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestConnectorValidate_BadToken(t *testing.T) {
	ts, _ := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	err := c.Validate(context.Background(), makeNotionConfig(
		&Config{APIKey: "bad-token"}, ts.URL, nil,
	))
	if err == nil {
		t.Fatal("expected error for bad token")
	}
}

func TestConnectorListResources(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	resources, err := c.ListResources(context.Background(), makeNotionConfig(cfg, ts.URL, nil))
	if err != nil {
		t.Fatalf("ListResources() error: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if resources[0].ExternalID != "page-1" || resources[0].Type != "page" || resources[0].Name != "Test Page" {
		t.Errorf("resource[0] = %+v", resources[0])
	}
	if resources[1].ExternalID != "db-1" || resources[1].Type != "database" || resources[1].Name != "Test Database" {
		t.Errorf("resource[1] = %+v, want Name=%q", resources[1], "Test Database")
	}
}

func TestConnectorFetchAll(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeNotionConfig(cfg, ts.URL, []string{"page-1"}), []string{"page-1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	if len(items) == 0 {
		t.Fatal("expected at least 1 item")
	}

	// First item should be the page with markdown content
	found := false
	for _, item := range items {
		if item.ExternalID == "page-1" {
			found = true
			if item.ContentType != "text/markdown" {
				t.Errorf("ContentType = %q", item.ContentType)
			}
			if item.Metadata["channel"] != "notion" {
				t.Errorf("channel = %q", item.Metadata["channel"])
			}
			if len(item.Content) == 0 {
				t.Error("expected non-empty content")
			}
		}
	}
	if !found {
		t.Error("page-1 not found in items")
	}
}

func TestConnectorFetchAll_Database(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeNotionConfig(cfg, ts.URL, []string{"db-1"}), []string{"db-1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	if len(items) == 0 {
		t.Fatal("expected at least 1 item")
	}

	// Each database record should be synced as an individual knowledge item
	found := false
	for _, item := range items {
		if item.ExternalID == "record-1" {
			found = true
			if item.Metadata["object_type"] != "page" {
				t.Errorf("object_type = %q, want %q", item.Metadata["object_type"], "page")
			}
			if item.Metadata["database"] != "Test Database" {
				t.Errorf("database = %q, want %q", item.Metadata["database"], "Test Database")
			}
			if item.ContentType != "text/markdown" {
				t.Errorf("ContentType = %q", item.ContentType)
			}
			if len(item.Content) == 0 {
				t.Error("expected non-empty content")
			}
		}
	}
	if !found {
		t.Error("record-1 not found in items")
	}
}

// TestConnectorFetchAll_SingleRecord verifies that selecting a single database
// row by ID routes through fetchPage's record-detection branch and produces an
// item via buildRecordItem (instead of being silently dropped as an empty page).
func TestConnectorFetchAll_SingleRecord(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeNotionConfig(cfg, ts.URL, []string{"record-1"}), []string{"record-1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ExternalID != "record-1" {
		t.Errorf("ExternalID = %q, want record-1", items[0].ExternalID)
	}
	if items[0].Metadata["object_type"] != "page" {
		t.Errorf("object_type = %q, want page", items[0].Metadata["object_type"])
	}
	if len(items[0].Content) == 0 {
		t.Error("expected non-empty content from buildRecordItem")
	}
}

func TestConnectorFetchIncremental_NoChanges(t *testing.T) {
	ts, cfg := fakeNotion()
	defer ts.Close()

	c := NewConnector()
	config := makeNotionConfig(cfg, ts.URL, []string{"page-1"})

	// First: full fetch to establish baseline
	_, err := c.FetchAll(context.Background(), config, []string{"page-1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	// Build a cursor that matches current state
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"page_edit_times": map[string]interface{}{
				"page-1": "2026-01-15T10:00:00Z",
			},
		},
	}

	items, newCursor, err := c.FetchIncremental(context.Background(), config, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}

	// No changes expected (timestamps match)
	if len(items) != 0 {
		t.Errorf("expected 0 items for no changes, got %d", len(items))
	}
	if newCursor == nil {
		t.Fatal("expected non-nil cursor")
	}
}

func TestPropertyToString(t *testing.T) {
	tests := []struct {
		name     string
		value    map[string]interface{}
		expected string
	}{
		{
			name: "select",
			value: map[string]interface{}{
				"type":   "select",
				"select": map[string]interface{}{"name": "Done"},
			},
			expected: "Done",
		},
		{
			name: "rich_text",
			value: map[string]interface{}{
				"type": "rich_text",
				"rich_text": []interface{}{
					map[string]interface{}{"plain_text": "Hello"},
				},
			},
			expected: "Hello",
		},
		{
			name: "number",
			value: map[string]interface{}{
				"type":   "number",
				"number": 42.0,
			},
			expected: "42",
		},
		{
			name: "checkbox true",
			value: map[string]interface{}{
				"type":     "checkbox",
				"checkbox": true,
			},
			expected: "true",
		},
		{
			name: "date",
			value: map[string]interface{}{
				"type": "date",
				"date": map[string]interface{}{"start": "2026-01-15", "end": "2026-01-20"},
			},
			expected: "2026-01-15 ~ 2026-01-20",
		},
		{
			name: "multi_select",
			value: map[string]interface{}{
				"type": "multi_select",
				"multi_select": []interface{}{
					map[string]interface{}{"name": "Tag1"},
					map[string]interface{}{"name": "Tag2"},
				},
			},
			expected: "Tag1, Tag2",
		},
		{
			name:     "nil value",
			value:    map[string]interface{}{"type": "rich_text", "rich_text": nil},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := propertyToString(tt.value)
			if got != tt.expected {
				t.Errorf("propertyToString() = %q, want %q", got, tt.expected)
			}
		})
	}
}
