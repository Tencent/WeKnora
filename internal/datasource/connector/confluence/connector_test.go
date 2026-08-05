package confluence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// ──────────────────────────────────────────────────────────────────────
// Fake Confluence API server
// ──────────────────────────────────────────────────────────────────────

// fakeConfluencePage holds the data needed to simulate a single page.
type fakeConfluencePage struct {
	ID          string
	Title       string
	SpaceKey    string
	VersionWhen string
	Creator     string
}

// fakeConfluence builds an httptest.Server that emulates Confluence Server 7.x APIs.
type fakeConfluence struct {
	server *httptest.Server
	mux    *http.ServeMux
	cfg    *Config
	pages  []fakeConfluencePage
}

// fakePDF generates a fake PDF byte slice that passes the validation in ExportPageAsPDF
// (must contain "pdf" in content-type and be >= 100 bytes).
var fakePDF = bytes.Repeat([]byte("%PDF-1.4 fake confluence page content for testing "), 5)

func newFakeConfluence(spaces []confluenceSpaceV1, pages []fakeConfluencePage) *fakeConfluence {
	f := &fakeConfluence{
		mux:   http.NewServeMux(),
		pages: pages,
		cfg: &Config{
			Edition:  "server",
			BaseURL:  "", // set after server creation
			Username: "testuser",
			Password: "testpass",
		},
	}

	// --- space listing (v1) ---
	f.mux.HandleFunc("/rest/api/space", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, confluenceSpaceListResponse{
			Results: spaces,
			Start:   0,
			Limit:   50,
			Size:    len(spaces),
		})
	})

	// --- CQL search (pages in space) ---
	f.mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, _ *http.Request) {
		results := make([]confluencePage, 0, len(pages))
		for _, p := range pages {
			results = append(results, confluencePage{
				ID:     p.ID,
				Title:  p.Title,
				Type:   "page",
				Status: "current",
				Space: struct {
					ID   int    `json:"id"`
					Key  string `json:"key"`
					Name string `json:"name"`
				}{
					ID:   1,
					Key:  p.SpaceKey,
					Name: p.SpaceKey,
				},
				Version: struct {
					By struct {
						DisplayName string `json:"displayName"`
					} `json:"by"`
					When string `json:"when"`
				}{
					By: struct {
						DisplayName string `json:"displayName"`
					}{DisplayName: p.Creator},
					When: p.VersionWhen,
				},
				Links: struct {
					WebUI string `json:"webui"`
					Self  string `json:"self"`
				}{
					WebUI: fmt.Sprintf("/display/%s/%s", p.SpaceKey, strings.ReplaceAll(p.Title, " ", "+")),
				},
			})
		}
		writeJSON(w, confluenceSearchResponse{
			Results: results,
			Start:   0,
			Limit:   100,
			Size:    len(results),
		})
	})

	// --- PDF export ---
	f.mux.HandleFunc("/spaces/flyingpdf/pdfpageexport.action", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(fakePDF)
	})

	// --- single content fetch ---
	f.mux.HandleFunc("/rest/api/content/", func(w http.ResponseWriter, r *http.Request) {
		// Handle content fetch by ID
		for _, p := range pages {
			if strings.Contains(r.URL.Path, p.ID) {
				writeJSON(w, confluencePage{
					ID:     p.ID,
					Title:  p.Title,
					Type:   "page",
					Status: "current",
					Space: struct {
						ID   int    `json:"id"`
						Key  string `json:"key"`
						Name string `json:"name"`
					}{ID: 1, Key: p.SpaceKey, Name: p.SpaceKey},
					Version: struct {
						By struct {
							DisplayName string `json:"displayName"`
						} `json:"by"`
						When string `json:"when"`
					}{
						By: struct {
							DisplayName string `json:"displayName"`
						}{DisplayName: p.Creator},
						When: p.VersionWhen,
					},
				})
				return
			}
		}
		http.NotFound(w, r)
	})

	ts := httptest.NewServer(f.mux)
	f.server = ts
	f.cfg.BaseURL = ts.URL
	return f
}

func (f *fakeConfluence) Close() {
	f.server.Close()
}

func (f *fakeConfluence) makeDSConfig(resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeConfluence,
		Credentials: map[string]interface{}{
			"edition":  f.cfg.Edition,
			"base_url": f.cfg.BaseURL,
			"username": f.cfg.Username,
			"password": f.cfg.Password,
		},
		ResourceIDs: resourceIDs,
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func defaultSpaces() []confluenceSpaceV1 {
	return []confluenceSpaceV1{
		{
			ID:   1001,
			Key:  "DEV",
			Name: "Development",
			Type: "global",
			Homepage: struct {
				ID int `json:"id"`
			}{ID: 100},
			Description: struct {
				View struct {
					Value string `json:"value"`
				} `json:"view"`
			}{View: struct {
				Value string `json:"value"`
			}{Value: "Dev space"}},
		},
	}
}

func defaultPages() []fakeConfluencePage {
	return []fakeConfluencePage{
		{
			ID: "101", Title: "Getting Started", SpaceKey: "DEV",
			VersionWhen: "2026-07-01T10:00:00.000+08:00", Creator: "alice",
		},
		{
			ID: "102", Title: "API Reference", SpaceKey: "DEV",
			VersionWhen: "2026-07-02T11:00:00.000+08:00", Creator: "bob",
		},
	}
}

// ──────────────────────────────────────────────────────────────────────
// Helper function tests
// ──────────────────────────────────────────────────────────────────────

func TestParseConfluenceConfig(t *testing.T) {
	t.Run("valid server config", func(t *testing.T) {
		cfg, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"edition":  "server",
				"base_url": "https://confluence.example.com",
				"username": "admin",
				"password": "secret",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Edition != "server" {
			t.Errorf("Edition = %q, want server", cfg.Edition)
		}
		if cfg.BaseURL != "https://confluence.example.com" {
			t.Errorf("BaseURL = %q", cfg.BaseURL)
		}
		if cfg.Username != "admin" {
			t.Errorf("Username = %q", cfg.Username)
		}
		if cfg.Password != "secret" {
			t.Errorf("Password = %q", cfg.Password)
		}
	})

	t.Run("valid cloud config", func(t *testing.T) {
		cfg, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"edition":   "cloud",
				"base_url":  "https://my.atlassian.net/wiki",
				"username":  "user@example.com",
				"api_token": "tok123",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !cfg.IsCloud() {
			t.Error("expected IsCloud() = true")
		}
		if cfg.APIToken != "tok123" {
			t.Errorf("APIToken = %q", cfg.APIToken)
		}
	})

	t.Run("default edition is server", func(t *testing.T) {
		cfg, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"base_url": "https://confluence.example.com",
				"username": "admin",
				"password": "secret",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Edition != "server" {
			t.Errorf("Edition = %q, want server", cfg.Edition)
		}
		if cfg.IsCloud() {
			t.Error("expected IsCloud() = false for default edition")
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := parseConfluenceConfig(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("missing base_url", func(t *testing.T) {
		_, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"username": "admin",
				"password": "secret",
			},
		})
		if err == nil {
			t.Fatal("expected error for missing base_url")
		}
	})

	t.Run("missing username", func(t *testing.T) {
		_, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"base_url": "https://confluence.example.com",
				"password": "secret",
			},
		})
		if err == nil {
			t.Fatal("expected error for missing username")
		}
	})

	t.Run("missing password for server", func(t *testing.T) {
		_, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"base_url": "https://confluence.example.com",
				"username": "admin",
			},
		})
		if err == nil {
			t.Fatal("expected error for missing password")
		}
	})

	t.Run("missing api_token for cloud", func(t *testing.T) {
		_, err := parseConfluenceConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"edition":  "cloud",
				"base_url": "https://my.atlassian.net/wiki",
				"username": "user@example.com",
			},
		})
		if err == nil {
			t.Fatal("expected error for missing api_token")
		}
	})
}

func TestParseConfluenceTimestamp(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		ts := parseConfluenceTimestamp("2026-07-01T10:00:00+08:00")
		if ts.IsZero() {
			t.Fatal("expected non-zero time")
		}
	})

	t.Run("normalized format", func(t *testing.T) {
		ts := parseConfluenceTimestamp("2026-07-01T10:00:00.000+08:00")
		// Should fall through to normalized parsing
		if ts.IsZero() {
			t.Fatal("expected non-zero time for ISO with millis")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		ts := parseConfluenceTimestamp("")
		if !ts.IsZero() {
			t.Error("expected zero time for empty string")
		}
	})

	t.Run("invalid string", func(t *testing.T) {
		ts := parseConfluenceTimestamp("not-a-date")
		if !ts.IsZero() {
			t.Error("expected zero time for invalid string")
		}
	})
}

func TestNormalizeLastModified(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"2026-07-16T11:25:00.000+08:00", "2026-07-16 11:25"},
		{"2026-07-16T11:25:00+08:00", "2026-07-16 11:25"},
		{"short", "short"},
	}
	for _, tt := range tests {
		got := normalizeLastModified(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeLastModified(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestSafeFilename(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"", "untitled"},
		{"a/b\\c:d*e?f", "a_b_c_d_e_f"},
		{"normal file", "normal file"},
		{strings.Repeat("a", 300), strings.Repeat("a", 200)},
	}
	for _, tt := range tests {
		got := safeFilename(tt.input)
		if got != tt.expected {
			t.Errorf("safeFilename(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestParseResourceID(t *testing.T) {
	tests := []struct {
		input      string
		wantPrefix string
		wantID     string
	}{
		{"s:12345", "s", "12345"},
		{"12345", "s", "12345"},
		{"s:abc", "s", "abc"},
	}
	for _, tt := range tests {
		prefix, id := parseResourceID(tt.input)
		if prefix != tt.wantPrefix || id != tt.wantID {
			t.Errorf("parseResourceID(%q) = (%q, %q), want (%q, %q)",
				tt.input, prefix, id, tt.wantPrefix, tt.wantID)
		}
	}
}

func TestConfluenceCursorRoundTrip(t *testing.T) {
	original := confluenceCursor{
		LastSyncTime: time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
		SpacePageTimes: map[string]map[string]string{
			"s:1001": {
				"101": "2026-07-01T10:00:00.000+08:00",
				"102": "2026-07-02T11:00:00.000+08:00",
			},
		},
	}

	// Serialize
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	// Deserialize to generic map (as stored in SyncCursor.ConnectorCursor)
	var cursorMap map[string]interface{}
	if err := json.Unmarshal(data, &cursorMap); err != nil {
		t.Fatalf("unmarshal to map error: %v", err)
	}

	// Restore via unmarshalCursor
	restored := unmarshalCursor(cursorMap)
	if restored == nil {
		t.Fatal("unmarshalCursor returned nil")
	}

	if restored.SpacePageTimes["s:1001"]["101"] != "2026-07-01T10:00:00.000+08:00" {
		t.Errorf("restored page 101 = %q", restored.SpacePageTimes["s:1001"]["101"])
	}
	if restored.SpacePageTimes["s:1001"]["102"] != "2026-07-02T11:00:00.000+08:00" {
		t.Errorf("restored page 102 = %q", restored.SpacePageTimes["s:1001"]["102"])
	}
}

func TestUnmarshalCursor_Nil(t *testing.T) {
	if unmarshalCursor(nil) != nil {
		t.Error("expected nil for nil input")
	}
}

// ──────────────────────────────────────────────────────────────────────
// Connector interface tests
// ──────────────────────────────────────────────────────────────────────

func TestConnectorType(t *testing.T) {
	c := NewConnector()
	if c.Type() != types.ConnectorTypeConfluence {
		t.Errorf("Type() = %q, want %q", c.Type(), types.ConnectorTypeConfluence)
	}
}

func TestConnectorValidate_Success(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	err := c.Validate(context.Background(), f.makeDSConfig(nil))
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestConnectorValidate_BadCredentials(t *testing.T) {
	c := NewConnector()
	err := c.Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"edition":  "server",
			"base_url": "http://127.0.0.1:1", // will fail to connect
			"username": "bad",
			"password": "bad",
		},
	})
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestConnectorListResources(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	resources, err := c.ListResources(context.Background(), f.makeDSConfig(nil), "")
	if err != nil {
		t.Fatalf("ListResources() error: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Name != "Development" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "Development")
	}
	if resources[0].Type != "space" {
		t.Errorf("Type = %q, want %q", resources[0].Type, "space")
	}
	if resources[0].HasChildren {
		t.Error("HasChildren should be false for spaces")
	}
	if resources[0].Description != "Dev space" {
		t.Errorf("Description = %q, want %q", resources[0].Description, "Dev space")
	}
}

func TestConnectorListResources_WithParentID(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	// Non-empty parentID should return nil (spaces are top-level only)
	resources, err := c.ListResources(context.Background(), f.makeDSConfig(nil), "some-parent")
	if err != nil {
		t.Fatalf("ListResources() error: %v", err)
	}
	if resources != nil {
		t.Errorf("expected nil for non-empty parentID, got %+v", resources)
	}
}

func TestConnectorResolveResourceAncestors(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	ancestors, err := c.ResolveResourceAncestors(
		context.Background(), f.makeDSConfig(nil), []string{"s:1001"},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors() error: %v", err)
	}
	if len(ancestors) != 0 {
		t.Errorf("expected no ancestors for space-level resources, got %+v", ancestors)
	}
}

// ──────────────────────────────────────────────────────────────────────
// FetchAll tests
// ──────────────────────────────────────────────────────────────────────

func TestFetchAll_BasicSync(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), f.makeDSConfig([]string{"1001"}), []string{"1001"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// Verify first item
	item := items[0]
	if item.ExternalID != "101" {
		t.Errorf("ExternalID = %q, want %q", item.ExternalID, "101")
	}
	if item.Title != "Getting Started" {
		t.Errorf("Title = %q", item.Title)
	}
	if item.ContentType != "application/pdf" {
		t.Errorf("ContentType = %q, want application/pdf", item.ContentType)
	}
	if item.FileName != "Getting Started.pdf" {
		t.Errorf("FileName = %q", item.FileName)
	}
	if item.Metadata["channel"] != types.ChannelConfluence {
		t.Errorf("channel = %q", item.Metadata["channel"])
	}
	if item.Metadata["space_key"] != "DEV" {
		t.Errorf("space_key = %q", item.Metadata["space_key"])
	}
	if item.Metadata["creator"] != "alice" {
		t.Errorf("creator = %q", item.Metadata["creator"])
	}
	if !bytes.Equal(item.Content, fakePDF) {
		t.Errorf("Content length = %d, want %d", len(item.Content), len(fakePDF))
	}
}

func TestFetchAll_EmptySpace(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), nil) // no pages
	defer f.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), f.makeDSConfig([]string{"1001"}), []string{"1001"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty space, got %d", len(items))
	}
}

func TestFetchAll_PageExportError_EmitsPlaceholder(t *testing.T) {
	// Create a fake server where PDF export fails for one page
	f := &fakeConfluence{
		mux: http.NewServeMux(),
		cfg: &Config{
			Edition:  "server",
			Username: "testuser",
			Password: "testpass",
		},
		pages: []fakeConfluencePage{
			{ID: "201", Title: "Good Page", SpaceKey: "DEV", VersionWhen: "2026-07-01T10:00:00.000+08:00"},
			{ID: "202", Title: "Bad Page", SpaceKey: "DEV", VersionWhen: "2026-07-01T11:00:00.000+08:00"},
		},
	}

	f.mux.HandleFunc("/rest/api/space", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, confluenceSpaceListResponse{
			Results: defaultSpaces(),
			Start:   0, Limit: 50, Size: 1,
		})
	})
	f.mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, _ *http.Request) {
		results := make([]confluencePage, 0, len(f.pages))
		for _, p := range f.pages {
			results = append(results, confluencePage{
				ID: p.ID, Title: p.Title, Type: "page", Status: "current",
				Space: struct {
					ID   int    `json:"id"`
					Key  string `json:"key"`
					Name string `json:"name"`
				}{ID: 1, Key: p.SpaceKey, Name: p.SpaceKey},
				Version: struct {
					By struct {
						DisplayName string `json:"displayName"`
					} `json:"by"`
					When string `json:"when"`
				}{When: p.VersionWhen},
				Links: struct {
					WebUI string `json:"webui"`
					Self  string `json:"self"`
				}{WebUI: "/display/DEV/" + p.Title},
			})
		}
		writeJSON(w, confluenceSearchResponse{Results: results, Start: 0, Limit: 100, Size: len(results)})
	})
	// PDF export: fail for page 202, succeed for others
	f.mux.HandleFunc("/spaces/flyingpdf/pdfpageexport.action", func(w http.ResponseWriter, r *http.Request) {
		pageID := r.URL.Query().Get("pageId")
		if pageID == "202" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("internal error"))
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(fakePDF)
	})

	ts := httptest.NewServer(f.mux)
	defer ts.Close()
	f.cfg.BaseURL = ts.URL

	dsConfig := &types.DataSourceConfig{
		Type: types.ConnectorTypeConfluence,
		Credentials: map[string]interface{}{
			"edition":  "server",
			"base_url": ts.URL,
			"username": "testuser",
			"password": "testpass",
		},
		ResourceIDs: []string{"1001"},
	}

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), dsConfig, []string{"1001"})
	if err != nil {
		t.Fatalf("FetchAll must not abort on single page error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items (1 success + 1 error placeholder), got %d", len(items))
	}

	// Find the error placeholder
	var placeholder *types.FetchedItem
	for i := range items {
		if items[i].Metadata["error"] != "" {
			placeholder = &items[i]
		}
	}
	if placeholder == nil {
		t.Fatal("expected a placeholder item with Metadata[error] set")
	}
	if placeholder.ExternalID != "202" {
		t.Errorf("placeholder ExternalID = %q, want 202", placeholder.ExternalID)
	}
	if placeholder.Title != "Bad Page" {
		t.Errorf("placeholder Title = %q", placeholder.Title)
	}
	if len(placeholder.Content) != 0 {
		t.Errorf("placeholder should have empty Content")
	}
	if placeholder.Metadata["channel"] != types.ChannelConfluence {
		t.Errorf("placeholder channel = %q", placeholder.Metadata["channel"])
	}
}

func TestFetchAll_MultipleSpaces(t *testing.T) {
	spaces := []confluenceSpaceV1{
		{
			ID: 1001, Key: "DEV", Name: "Dev", Type: "global",
			Homepage: struct {
				ID int `json:"id"`
			}{ID: 100},
		},
		{
			ID: 1002, Key: "OPS", Name: "Ops", Type: "global",
			Homepage: struct {
				ID int `json:"id"`
			}{ID: 200},
		},
	}

	f := &fakeConfluence{
		mux: http.NewServeMux(),
		cfg: &Config{Edition: "server", Username: "u", Password: "p"},
		pages: []fakeConfluencePage{
			{ID: "301", Title: "Dev Page", SpaceKey: "DEV", VersionWhen: "2026-07-01T10:00:00.000+08:00"},
		},
	}

	f.mux.HandleFunc("/rest/api/space", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, confluenceSpaceListResponse{Results: spaces, Start: 0, Limit: 50, Size: len(spaces)})
	})
	f.mux.HandleFunc("/rest/api/content/search", func(w http.ResponseWriter, _ *http.Request) {
		// Return same page for any space
		writeJSON(w, confluenceSearchResponse{
			Results: []confluencePage{
				{
					ID: "301", Title: "Dev Page", Type: "page", Status: "current",
					Space: struct {
						ID   int    `json:"id"`
						Key  string `json:"key"`
						Name string `json:"name"`
					}{ID: 1, Key: "DEV", Name: "Dev"},
					Version: struct {
						By struct {
							DisplayName string `json:"displayName"`
						} `json:"by"`
						When string `json:"when"`
					}{When: "2026-07-01T10:00:00.000+08:00"},
					Links: struct {
						WebUI string `json:"webui"`
						Self  string `json:"self"`
					}{WebUI: "/display/DEV/Dev+Page"},
				},
			},
			Start: 0, Limit: 100, Size: 1,
		})
	})
	f.mux.HandleFunc("/spaces/flyingpdf/pdfpageexport.action", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(fakePDF)
	})

	ts := httptest.NewServer(f.mux)
	defer ts.Close()
	f.cfg.BaseURL = ts.URL

	dsConfig := &types.DataSourceConfig{
		Type: types.ConnectorTypeConfluence,
		Credentials: map[string]interface{}{
			"edition":  "server",
			"base_url": ts.URL,
			"username": "u",
			"password": "p",
		},
		ResourceIDs: []string{"1001", "1002"},
	}

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), dsConfig, []string{"1001", "1002"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}
	// Both spaces return the same page (1 page each), total 2 items
	if len(items) != 2 {
		t.Errorf("expected 2 items (1 per space), got %d", len(items))
	}
}

// ──────────────────────────────────────────────────────────────────────
// FetchIncremental tests
// ──────────────────────────────────────────────────────────────────────

func TestFetchIncremental_FirstSync(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	dsConfig := f.makeDSConfig([]string{"1001"})

	items, cursor, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items on first sync, got %d", len(items))
	}
	if cursor == nil {
		t.Fatal("expected non-nil cursor")
	}
	if cursor.LastSyncTime.IsZero() {
		t.Error("cursor.LastSyncTime should not be zero")
	}
}

func TestFetchIncremental_NoChanges(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	dsConfig := f.makeDSConfig([]string{"1001"})

	// First sync
	_, cursor1, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}

	// Second sync with same data → no changes
	items, _, err := c.FetchIncremental(context.Background(), dsConfig, cursor1)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items (no changes), got %d", len(items))
	}
}

func TestFetchIncremental_DetectsDeletion(t *testing.T) {
	// First sync: 2 pages
	f1 := newFakeConfluence(defaultSpaces(), defaultPages())
	c := NewConnector()
	dsConfig := f1.makeDSConfig([]string{"1001"})

	_, cursor1, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	f1.Close()

	// Second sync: only 1 page remains (page 102 was deleted)
	pagesAfterDelete := []fakeConfluencePage{
		{
			ID: "101", Title: "Getting Started", SpaceKey: "DEV",
			VersionWhen: "2026-07-01T10:00:00.000+08:00", Creator: "alice",
		},
	}
	f2 := newFakeConfluence(defaultSpaces(), pagesAfterDelete)
	defer f2.Close()
	dsConfig2 := f2.makeDSConfig([]string{"1001"})

	items, _, err := c.FetchIncremental(context.Background(), dsConfig2, cursor1)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}

	// Should have 1 deleted item for page 102
	deletedCount := 0
	for _, item := range items {
		if item.IsDeleted {
			deletedCount++
			if item.ExternalID != "102" {
				t.Errorf("expected deleted ExternalID=102, got %q", item.ExternalID)
			}
		}
	}
	if deletedCount != 1 {
		t.Errorf("expected 1 deleted item, got %d; items=%+v", deletedCount, items)
	}
}

func TestFetchIncremental_DetectsChanges(t *testing.T) {
	// First sync
	f1 := newFakeConfluence(defaultSpaces(), defaultPages())
	c := NewConnector()
	dsConfig := f1.makeDSConfig([]string{"1001"})

	_, cursor1, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	f1.Close()

	// Second sync: page 101 has a newer version timestamp
	changedPages := []fakeConfluencePage{
		{
			ID: "101", Title: "Getting Started", SpaceKey: "DEV",
			VersionWhen: "2026-07-10T15:00:00.000+08:00", Creator: "alice",
		},
		{
			ID: "102", Title: "API Reference", SpaceKey: "DEV",
			VersionWhen: "2026-07-02T11:00:00.000+08:00", Creator: "bob",
		},
	}
	f2 := newFakeConfluence(defaultSpaces(), changedPages)
	defer f2.Close()
	dsConfig2 := f2.makeDSConfig([]string{"1001"})

	items, _, err := c.FetchIncremental(context.Background(), dsConfig2, cursor1)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}

	// Only page 101 changed
	if len(items) != 1 {
		t.Fatalf("expected 1 changed item, got %d", len(items))
	}
	if items[0].ExternalID != "101" {
		t.Errorf("expected changed item ExternalID=101, got %q", items[0].ExternalID)
	}
}

func TestFetchIncremental_NoResourceIDs(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	c := NewConnector()
	dsConfig := f.makeDSConfig(nil)
	dsConfig.ResourceIDs = nil

	_, _, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err == nil {
		t.Fatal("expected error for empty resource IDs")
	}
	if !strings.Contains(err.Error(), "no resource IDs") {
		t.Errorf("unexpected error: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Client tests
// ──────────────────────────────────────────────────────────────────────

func TestClientPing(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	client, err := NewClient(f.cfg)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestClientListSpaces(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	client, err := NewClient(f.cfg)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	spaces, err := client.ListSpaces(context.Background())
	if err != nil {
		t.Fatalf("ListSpaces() error: %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("expected 1 space, got %d", len(spaces))
	}
	if spaces[0].Key != "DEV" {
		t.Errorf("Key = %q, want DEV", spaces[0].Key)
	}
	if spaces[0].Name != "Development" {
		t.Errorf("Name = %q, want Development", spaces[0].Name)
	}
	// v1 int ID should be converted to string
	if spaces[0].ID != "1001" {
		t.Errorf("ID = %q, want 1001", spaces[0].ID)
	}
}

func TestClientGetAllPagesInSpace(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	client, err := NewClient(f.cfg)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	pages, err := client.GetAllPagesInSpace(context.Background(), "DEV")
	if err != nil {
		t.Fatalf("GetAllPagesInSpace() error: %v", err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages, got %d", len(pages))
	}
	if pages[0].ID != "101" {
		t.Errorf("first page ID = %q, want 101", pages[0].ID)
	}
}

func TestClientExportPageAsPDF(t *testing.T) {
	f := newFakeConfluence(defaultSpaces(), defaultPages())
	defer f.Close()

	client, err := NewClient(f.cfg)
	if err != nil {
		t.Fatalf("NewClient error: %v", err)
	}

	data, filename, err := client.ExportPageAsPDF(context.Background(), "101", "Getting Started")
	if err != nil {
		t.Fatalf("ExportPageAsPDF() error: %v", err)
	}
	if !bytes.Equal(data, fakePDF) {
		t.Errorf("data length = %d, want %d", len(data), len(fakePDF))
	}
	if filename != "Getting Started.pdf" {
		t.Errorf("filename = %q", filename)
	}
}

func TestClientDoRequest_BasicAuth(t *testing.T) {
	var gotUser, gotPass string
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/space", func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		writeJSON(w, confluenceSpaceListResponse{Results: nil, Start: 0, Limit: 50, Size: 0})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client, _ := NewClient(&Config{
		Edition:  "server",
		BaseURL:  ts.URL,
		Username: "myuser",
		Password: "mypass",
	})
	_, _ = client.ListSpaces(context.Background())

	if gotUser != "myuser" {
		t.Errorf("BasicAuth username = %q, want myuser", gotUser)
	}
	if gotPass != "mypass" {
		t.Errorf("BasicAuth password = %q, want mypass", gotPass)
	}
}

func TestClientDoRequest_ErrorResponse(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/space", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Unauthorized"}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client, _ := NewClient(&Config{
		Edition:  "server",
		BaseURL:  ts.URL,
		Username: "bad",
		Password: "bad",
	})
	_, err := client.ListSpaces(context.Background())
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code: %v", err)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Space to resource conversion
// ──────────────────────────────────────────────────────────────────────

func TestSpaceToResource(t *testing.T) {
	space := confluenceSpace{
		ID:   "123",
		Key:  "TEST",
		Name: "Test Space",
		Type: "global",
		Description: struct {
			View struct {
				Value string `json:"value"`
			} `json:"view"`
		}{View: struct {
			Value string `json:"value"`
		}{Value: "A test space"}},
		Links: struct {
			WebUI string `json:"webui"`
			Self  string `json:"self"`
		}{WebUI: "/display/TEST"},
	}

	r := spaceToResource(space, "https://confluence.example.com")
	if r.ExternalID != "123" {
		t.Errorf("ExternalID = %q", r.ExternalID)
	}
	if r.Name != "Test Space" {
		t.Errorf("Name = %q", r.Name)
	}
	if r.Type != "space" {
		t.Errorf("Type = %q", r.Type)
	}
	if r.URL != "https://confluence.example.com/display/TEST" {
		t.Errorf("URL = %q", r.URL)
	}
	if r.Description != "A test space" {
		t.Errorf("Description = %q", r.Description)
	}
	if r.HasChildren {
		t.Error("HasChildren should be false")
	}
	if r.Metadata["space_key"] != "TEST" {
		t.Errorf("metadata space_key = %q", r.Metadata["space_key"])
	}
}

// ──────────────────────────────────────────────────────────────────────
// PDF helper tests (pdf.go)
// ──────────────────────────────────────────────────────────────────────

func TestCleanConfluenceHTML_LayoutToTable(t *testing.T) {
	input := `<ac:layout><ac:layout-section>` +
		`<ac:layout-cell ac:width="50"><ac:layout-cell-content>` +
		`<p>Hello</p></ac:layout-cell-content></ac:layout-cell>` +
		`</ac:layout-section></ac:layout>`
	got := cleanConfluenceHTML(input)

	if !strings.Contains(got, "<table") {
		t.Error("ac:layout should be converted to <table>")
	}
	if !strings.Contains(got, "<tr>") {
		t.Error("ac:layout-section should be converted to <tr>")
	}
	if !strings.Contains(got, "<td") {
		t.Error("ac:layout-cell should be converted to <td>")
	}
	if !strings.Contains(got, "<div>") {
		t.Error("ac:layout-cell-content should be converted to <div>")
	}
	if !strings.Contains(got, "<p>Hello</p>") {
		t.Error("inner content should be preserved")
	}
	if strings.Contains(got, "ac:") {
		t.Errorf("no ac: tags should remain, got: %s", got)
	}
}

func TestCleanConfluenceHTML_UnwrapAcImage(t *testing.T) {
	input := `<ac:image ac:align="center">` +
		`<ri:attachment ri:filename="photo.png" />` +
		`<img src="/download/photo.png" /></ac:image>`
	got := cleanConfluenceHTML(input)

	if strings.Contains(got, "ac:image") {
		t.Error("ac:image should be unwrapped")
	}
	if strings.Contains(got, "ri:attachment") {
		t.Error("ri:attachment should be removed")
	}
	if !strings.Contains(got, "<img") {
		t.Error("inner <img> should be preserved")
	}
}

func TestCleanConfluenceHTML_RemoveRiPage(t *testing.T) {
	input := `<p>Link: <ri:page ri:content-title="Other Page" ri:space-key="DEV" /></p>`
	got := cleanConfluenceHTML(input)

	if strings.Contains(got, "ri:page") {
		t.Error("ri:page should be removed")
	}
	if !strings.Contains(got, "<p>Link: </p>") {
		t.Errorf("surrounding content should be preserved, got: %s", got)
	}
}

func TestCleanConfluenceHTML_ImgAlignToCSS(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantCSS string
	}{
		{"center", `<img src="a.png" ac:align="center">`, "display:block;margin-left:auto;margin-right:auto;"},
		{"right", `<img src="a.png" ac:align="right">`, "float:right;"},
		{"left", `<img src="a.png" ac:align="left">`, "float:left;"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanConfluenceHTML(tt.input)
			if !strings.Contains(got, tt.wantCSS) {
				t.Errorf("expected CSS %q in output: %s", tt.wantCSS, got)
			}
			if strings.Contains(got, "ac:align") {
				t.Error("ac:align attribute should be removed")
			}
		})
	}
}

func TestCleanConfluenceHTML_RemoveDataMceSrc(t *testing.T) {
	input := `<img src="real.png" data-mce-src="stale.png" alt="pic">`
	got := cleanConfluenceHTML(input)

	if strings.Contains(got, "data-mce-src") {
		t.Error("data-mce-src should be removed")
	}
	if !strings.Contains(got, `src="real.png"`) {
		t.Error("real src should be preserved")
	}
}

func TestWrapHTMLDocument_EscapesTitle(t *testing.T) {
	malicious := `<script>alert("xss")</script>`
	got := wrapHTMLDocument(malicious, "<p>body</p>")

	if strings.Contains(got, "<script>") {
		t.Error("title should be HTML-escaped, raw <script> must not appear")
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Error("title should contain escaped entities")
	}
	if !strings.Contains(got, "<p>body</p>") {
		t.Error("body HTML should be included as-is")
	}
	if !strings.Contains(got, "<!DOCTYPE html>") {
		t.Error("should be a full HTML document")
	}
}

func TestWrapHTMLDocument_Structure(t *testing.T) {
	got := wrapHTMLDocument("My Page", "<h2>Section</h2>")

	if !strings.Contains(got, "<title>My Page</title>") {
		t.Error("should contain <title>")
	}
	if !strings.Contains(got, "<h1>My Page</h1>") {
		t.Error("should contain <h1> with page title")
	}
	if !strings.Contains(got, "@page") {
		t.Error("should contain @page CSS for print")
	}
}

func TestExtractFilenameFromURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://xxx/wiki/download/attachments/123/image.png?api=v2", "image.png"},
		{"https://xxx/wiki/download/attachments/123/image.png", "image.png"},
		{"/download/attachments/456/diagram.svg", "diagram.svg"},
		{"just-a-filename.jpg", "just-a-filename.jpg"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractFilenameFromURL(tt.input)
		if got != tt.want {
			t.Errorf("extractFilenameFromURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractAltFromTag(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`<img src="a.png" alt="screenshot">`, "screenshot"},
		{`<img alt="logo" src="b.png">`, "logo"},
		{`<img src="c.png">`, ""},
	}
	for _, tt := range tests {
		got := extractAltFromTag(tt.input)
		if got != tt.want {
			t.Errorf("extractAltFromTag(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is a long string", 10, "this is a ..."},
		{"", 5, ""},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
	}
}

// ──────────────────────────────────────────────────────────────────────
// Image inlining tests
// ──────────────────────────────────────────────────────────────────────

func TestInlineImages_ByResourceID(t *testing.T) {
	// Fake server that serves an attachment download
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/101/child/attachment/att999/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG fake image data"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client, _ := NewClient(&Config{
		Edition:  "server",
		BaseURL:  ts.URL,
		Username: "u",
		Password: "p",
	})

	input := `<img src="/download/attachments/101/photo.png" data-linked-resource-id="att999" alt="photo">`
	got := inlineImages(context.Background(), client, "101", nil, input)

	if !strings.Contains(got, "data:image/png;base64,") {
		t.Errorf("expected base64 data URI, got: %s", got)
	}
	if strings.Contains(got, "/download/attachments") {
		t.Error("original src should be replaced")
	}
}

func TestInlineImages_ByFilenameLookup(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/rest/api/content/101/child/attachment/att555/download", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("\xff\xd8\xff fake jpeg"))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client, _ := NewClient(&Config{
		Edition:  "server",
		BaseURL:  ts.URL,
		Username: "u",
		Password: "p",
	})

	attMap := map[string]string{"diagram.jpg": "att555"}
	input := `<img src="/download/attachments/101/diagram.jpg?api=v2">`
	got := inlineImages(context.Background(), client, "101", attMap, input)

	if !strings.Contains(got, "data:image/jpeg;base64,") {
		t.Errorf("expected base64 data URI for jpeg, got: %s", got)
	}
}

func TestInlineImages_SkipsDataURI(t *testing.T) {
	client, _ := NewClient(&Config{
		Edition:  "server",
		BaseURL:  "http://127.0.0.1:1",
		Username: "u",
		Password: "p",
	})

	input := `<img src="data:image/png;base64,abc123">`
	got := inlineImages(context.Background(), client, "101", nil, input)

	if got != input {
		t.Errorf("data URI should be left unchanged, got: %s", got)
	}
}

func TestInlineImages_UnresolvableSrc(t *testing.T) {
	client, _ := NewClient(&Config{
		Edition:  "server",
		BaseURL:  "http://127.0.0.1:1",
		Username: "u",
		Password: "p",
	})

	input := `<img src="/some/unknown/path.png">`
	got := inlineImages(context.Background(), client, "101", nil, input)

	// Should return the tag unchanged when attachment cannot be resolved
	if got != input {
		t.Errorf("unresolvable img should be unchanged, got: %s", got)
	}
}
