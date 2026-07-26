package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// ──────────────────────────────────────────────────────────────────────
// Fake DingTalk API server
// ──────────────────────────────────────────────────────────────────────

// fakeDingTalk builds an httptest.Server that emulates the relevant DingTalk APIs.
func fakeDingTalk(nodes []docNode) (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	// --- auth ---
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{
			AccessToken: "fake-token",
			ExpireIn:    7200,
		})
	})

	// --- spaces ---
	mux.HandleFunc("/v1.0/doc/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, spaceListResponse{
			Result: struct {
				Spaces    []docSpace `json:"spaces"`
				NextToken string     `json:"nextToken"`
			}{
				Spaces: []docSpace{
					{SpaceID: "space1", Name: "Test Space", Desc: "test desc"},
				},
			},
		})
	})

	// --- space nodes ---
	mux.HandleFunc("/v1.0/doc/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nodeListResponse{
			Result: struct {
				Nodes     []docNode `json:"nodes"`
				NextToken string    `json:"nextToken"`
			}{
				Nodes: nodes,
			},
		})
	})

	// --- node detail ---
	mux.HandleFunc("/v1.0/doc/nodes/", func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.URL.Path[len("/v1.0/doc/nodes/"):]
		for _, n := range nodes {
			if n.NodeID == nodeID {
				writeJSON(w, nodeDetailResponse{
					Result: docNodeDetail{
						NodeID:  n.NodeID,
						SpaceID: n.SpaceID,
						Name:    n.Name,
						Type:    n.Type,
						Content: "# " + n.Name + "\n\nHello from DingTalk doc.",
					},
				})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	cfg := &Config{
		AppKey:    "test-key",
		AppSecret: "test-secret",
		BaseURL:   srv.URL,
	}
	return srv, cfg
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func makeDSConfig(cfg *Config, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{"app_key": cfg.AppKey, "app_secret": cfg.AppSecret, "base_url": cfg.BaseURL},
		ResourceIDs: resourceIDs,
	}
}

// ──────────────────────────────────────────────────────────────────────
// Connector interface tests
// ──────────────────────────────────────────────────────────────────────

func TestConnector_Type(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeDingTalk {
		t.Errorf("Type() = %q, want %q", NewConnector().Type(), types.ConnectorTypeDingTalk)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	srv, cfg := fakeDingTalk(nil)
	defer srv.Close()

	if err := NewConnector().Validate(context.Background(), makeDSConfig(cfg, nil)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnector_Validate_BadCredentials(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_key":    "bad",
			"app_secret": "bad",
			"base_url":   "http://127.0.0.1:1", // will fail to connect
		},
	})
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestConnector_Validate_MissingAppKey(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_secret": "test-secret",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing app_key")
	}
}

func TestConnector_Validate_MissingAppSecret(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_key": "test-key",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing app_secret")
	}
}

func TestConnector_ListResources_TopLevel(t *testing.T) {
	srv, cfg := fakeDingTalk(nil)
	defer srv.Close()

	c := NewConnector()
	resources, err := c.ListResources(context.Background(), makeDSConfig(cfg, nil), "")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].ExternalID != "space1" {
		t.Errorf("ExternalID = %q, want %q", resources[0].ExternalID, "space1")
	}
	if resources[0].Name != "Test Space" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "Test Space")
	}
	if resources[0].Type != "kb_space" {
		t.Errorf("Type = %q, want %q", resources[0].Type, "kb_space")
	}
	if !resources[0].HasChildren {
		t.Errorf("HasChildren = false, want true")
	}
}

func TestConnector_ListResources_Nodes(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc 1", Type: "doc", EditTime: 1700000000000},
		{NodeID: "n2", SpaceID: "space1", Name: "Folder 1", Type: "folder", EditTime: 1700000000000},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	resources, err := c.ListResources(context.Background(), makeDSConfig(cfg, nil), "space1")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}

	// Check first node
	if resources[0].ExternalID != "space1:n1" {
		t.Errorf("ExternalID = %q, want %q", resources[0].ExternalID, "space1:n1")
	}
	if resources[0].Name != "Doc 1" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "Doc 1")
	}
	if resources[0].Type != "kb_node" {
		t.Errorf("Type = %q, want %q", resources[0].Type, "kb_node")
	}

	// Folder should have HasChildren=true
	if !resources[1].HasChildren {
		t.Errorf("Folder HasChildren = false, want true")
	}
}

func TestConnector_FetchAll(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc 1", Type: "doc", EditTime: 1700000000000},
		{NodeID: "n2", SpaceID: "space1", Name: "Doc 2", Type: "doc", EditTime: 1700000001000},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeDSConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}

	for _, it := range items {
		if it.ContentType != "text/markdown" {
			t.Errorf("item %s ContentType = %q, want text/markdown", it.ExternalID, it.ContentType)
		}
		if len(it.Content) == 0 {
			t.Errorf("item %s Content is empty", it.ExternalID)
		}
		if it.FileName == "" {
			t.Errorf("item %s FileName is empty", it.ExternalID)
		}
		if it.Metadata["channel"] != types.ChannelDingtalk {
			t.Errorf("item %s channel = %q, want %q", it.ExternalID, it.Metadata["channel"], types.ChannelDingtalk)
		}
	}
}

func TestConnector_FetchAll_SkipsNonDocTypes(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc", Type: "doc", EditTime: 1700000000000},
		{NodeID: "n2", SpaceID: "space1", Name: "Sheet", Type: "sheet", EditTime: 1700000000000},
		{NodeID: "n3", SpaceID: "space1", Name: "Mindmap", Type: "mindmap", EditTime: 1700000000000},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeDSConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}

	// Only "doc" type should be fetched
	if len(items) != 1 {
		t.Fatalf("want 1 item (only doc type), got %d", len(items))
	}
	if items[0].ExternalID != "n1" {
		t.Errorf("ExternalID = %q, want %q", items[0].ExternalID, "n1")
	}
}

func TestConnector_FetchIncremental_NoChanges(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "A", Type: "doc", EditTime: 1700000000000},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	// First sync → establish cursor
	_, cursor1, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(cfg, []string{"space1"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Second sync with same cursor → 0 items
	items, _, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(cfg, []string{"space1"}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 changed items, got %d", len(items))
	}
}

func TestConnector_FetchIncremental_DetectsNewAndChanged(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "A", Type: "doc", EditTime: 1700000000000},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	conn := NewConnector()

	// First sync
	_, cursor1, err := conn.FetchIncremental(context.Background(), makeDSConfig(cfg, []string{"space1"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Simulate change: update edit time and add a new node
	nodes[0].EditTime = 1700000009999
	nodes = append(nodes, docNode{
		NodeID: "n2", SpaceID: "space1", Name: "B", Type: "doc", EditTime: 1700000002000,
	})
	// Re-register node detail handler to include n2
	srv.Config.Handler = newFakeHandlerWithNodes(nodes)

	// Second sync → should detect n1 (changed) + n2 (new)
	items, _, err := conn.FetchIncremental(context.Background(), makeDSConfig(cfg, []string{"space1"}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 changed items, got %d", len(items))
	}
}

func TestConnector_FetchIncremental_DetectsDeleted(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "A", Type: "doc", EditTime: 1700000000000},
		{NodeID: "n2", SpaceID: "space1", Name: "B", Type: "doc", EditTime: 1700000000000},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	conn := NewConnector()

	// First sync: 2 docs
	_, cursor1, err := conn.FetchIncremental(context.Background(), makeDSConfig(cfg, []string{"space1"}), nil)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Remove n2 (simulate deletion)
	nodes = nodes[:1]
	srv.Config.Handler = newFakeHandlerWithNodes(nodes)

	// Second sync → should detect n2 as deleted
	items, _, err := conn.FetchIncremental(context.Background(), makeDSConfig(cfg, []string{"space1"}), cursor1)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	var deletedCount int
	for _, it := range items {
		if it.IsDeleted {
			deletedCount++
			if it.ExternalID != "n2" {
				t.Errorf("deleted ExternalID = %q, want %q", it.ExternalID, "n2")
			}
		}
	}
	if deletedCount != 1 {
		t.Errorf("expected 1 deleted item, got %d", deletedCount)
	}
}

func TestParseDingTalkConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_key":    "key",
				"app_secret": "secret",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AppKey != "key" || cfg.AppSecret != "secret" {
			t.Errorf("cfg = %+v", cfg)
		}
		if cfg.GetBaseURL() != DefaultBaseURL {
			t.Errorf("BaseURL = %q, want %q", cfg.GetBaseURL(), DefaultBaseURL)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := parseDingTalkConfig(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("missing app_key", func(t *testing.T) {
		_, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_secret": "secret",
			},
		})
		if err == nil {
			t.Fatal("expected error for missing app_key")
		}
	})
}

func TestMakeAndParseResourceID(t *testing.T) {
	rid := makeNodeResourceID("space1", "node1")
	spaceID, nodeID := parseDingTalkResourceID(rid)
	if spaceID != "space1" || nodeID != "node1" {
		t.Errorf("parseDingTalkResourceID(%q) = (%q, %q), want (%q, %q)", rid, spaceID, nodeID, "space1", "node1")
	}

	// Space-only resource ID
	spaceID2, nodeID2 := parseDingTalkResourceID("space1")
	if spaceID2 != "space1" || nodeID2 != "" {
		t.Errorf("parseDingTalkResourceID(\"space1\") = (%q, %q), want (%q, %q)", spaceID2, nodeID2, "space1", "")
	}
}

func TestConfig_GetBaseURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", DefaultBaseURL},
		{"https://api.dingtalk.com", "https://api.dingtalk.com"},
		{"api.dingtalk.com", "https://api.dingtalk.com"},
		{"https://api.dingtalk.com/", "https://api.dingtalk.com"},
	}
	for _, tt := range tests {
		cfg := &Config{BaseURL: tt.input}
		if got := cfg.GetBaseURL(); got != tt.expected {
			t.Errorf("GetBaseURL(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// --- Fake handler factory for incremental tests that need to change nodes ---

func newFakeHandlerWithNodes(nodes []docNode) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{AccessToken: "fake-token", ExpireIn: 7200})
	})

	mux.HandleFunc("/v1.0/doc/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, spaceListResponse{
			Result: struct {
				Spaces    []docSpace `json:"spaces"`
				NextToken string     `json:"nextToken"`
			}{
				Spaces: []docSpace{{SpaceID: "space1", Name: "Test Space"}},
			},
		})
	})

	mux.HandleFunc("/v1.0/doc/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nodeListResponse{
			Result: struct {
				Nodes     []docNode `json:"nodes"`
				NextToken string    `json:"nextToken"`
			}{
				Nodes: nodes,
			},
		})
	})

	mux.HandleFunc("/v1.0/doc/nodes/", func(w http.ResponseWriter, r *http.Request) {
		nodeID := r.URL.Path[len("/v1.0/doc/nodes/"):]
		for _, n := range nodes {
			if n.NodeID == nodeID {
				writeJSON(w, nodeDetailResponse{
					Result: docNodeDetail{
						NodeID:  n.NodeID,
						SpaceID: n.SpaceID,
						Name:    n.Name,
						Type:    n.Type,
						Content: "# " + n.Name + "\n\nContent",
					},
				})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})

	return mux
}
