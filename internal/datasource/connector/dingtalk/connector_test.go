package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func TestConnectorType(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeDingTalk {
		t.Fatalf("Type() = %q, want %q", NewConnector().Type(), types.ConnectorTypeDingTalk)
	}
}

func TestValidateGetsAccessTokenAndPingsSpaces(t *testing.T) {
	srv := newDingTalkTestServer(t)
	defer srv.Close()

	cfg := dingtalkTestConfig(srv.URL)
	if err := NewConnector().Validate(context.Background(), cfg); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if srv.tokenCalls != 1 {
		t.Fatalf("token calls = %d, want 1", srv.tokenCalls)
	}
	if srv.spaceCalls != 1 {
		t.Fatalf("space calls = %d, want 1", srv.spaceCalls)
	}
}

func TestListResourcesLoadsSpacesAndDirectChildren(t *testing.T) {
	srv := newDingTalkTestServer(t)
	defer srv.Close()

	connector := NewConnector()
	cfg := dingtalkTestConfig(srv.URL)

	spaces, err := connector.ListResources(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("ListResources(root) error = %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("spaces len = %d, want 1", len(spaces))
	}
	if spaces[0].ExternalID != "space-1" || spaces[0].Type != "doc_space" || !spaces[0].HasChildren {
		t.Fatalf("unexpected space resource: %+v", spaces[0])
	}

	children, err := connector.ListResources(context.Background(), cfg, "space-1")
	if err != nil {
		t.Fatalf("ListResources(children) error = %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children len = %d, want 2", len(children))
	}
	if children[0].ExternalID != "space-1:doc-1" || children[0].Type != "document" {
		t.Fatalf("unexpected first child: %+v", children[0])
	}
	if children[1].ExternalID != "space-1:folder-1" || children[1].Type != "folder" || !children[1].HasChildren {
		t.Fatalf("unexpected second child: %+v", children[1])
	}
}

func TestFetchAllReturnsMarkdownItems(t *testing.T) {
	srv := newDingTalkTestServer(t)
	defer srv.Close()

	items, err := NewConnector().FetchAll(context.Background(), dingtalkTestConfig(srv.URL), []string{"space-1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want 1", len(items))
	}
	item := items[0]
	if item.ExternalID != "doc-1" {
		t.Fatalf("ExternalID = %q, want doc-1", item.ExternalID)
	}
	if item.Title != "Project Plan" {
		t.Fatalf("Title = %q, want Project Plan", item.Title)
	}
	if string(item.Content) != "# Project Plan\n\nHello DingTalk" {
		t.Fatalf("Content = %q", string(item.Content))
	}
	if item.ContentType != "text/markdown" || item.FileName != "Project Plan.md" {
		t.Fatalf("unexpected content metadata: type=%q file=%q", item.ContentType, item.FileName)
	}
	if item.Metadata["channel"] != types.ConnectorTypeDingTalk {
		t.Fatalf("channel metadata = %q, want %q", item.Metadata["channel"], types.ConnectorTypeDingTalk)
	}
}

func TestFetchAllSupportsSelectedDocumentResource(t *testing.T) {
	srv := newDingTalkTestServer(t)
	defer srv.Close()

	items, err := NewConnector().FetchAll(context.Background(), dingtalkTestConfig(srv.URL), []string{"space-1:doc-1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want selected document", len(items))
	}
	if items[0].ExternalID != "doc-1" || string(items[0].Content) == "" {
		t.Fatalf("unexpected selected document item: %+v", items[0])
	}
}

func TestFetchIncrementalSkipsUnchangedAndEmitsDeleted(t *testing.T) {
	srv := newDingTalkTestServer(t)
	defer srv.Close()

	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"space_node_times": map[string]interface{}{
				"space-1": map[string]interface{}{
					"doc-1": "2026-07-01T00:00:00Z",
					"old":   "2026-06-01T00:00:00Z",
				},
			},
		},
	}

	items, next, err := NewConnector().FetchIncremental(context.Background(), dingtalkTestConfig(srv.URL), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if next == nil || next.ConnectorCursor == nil {
		t.Fatalf("next cursor missing: %+v", next)
	}
	if len(items) != 1 {
		t.Fatalf("items len = %d, want only deleted placeholder", len(items))
	}
	if !items[0].IsDeleted || items[0].ExternalID != "old" {
		t.Fatalf("unexpected deleted item: %+v", items[0])
	}
	if srv.contentCalls != 0 {
		t.Fatalf("content calls = %d, want 0 for unchanged doc", srv.contentCalls)
	}
}

func TestResolveResourceAncestorsRevealsSelectedDocument(t *testing.T) {
	srv := newDingTalkTestServer(t)
	defer srv.Close()

	ancestors, err := NewConnector().ResolveResourceAncestors(
		context.Background(),
		dingtalkTestConfig(srv.URL),
		[]string{"space-1:doc-1", "space-1"},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors() error = %v", err)
	}
	if len(ancestors) != 1 || ancestors[0] != "space-1" {
		t.Fatalf("ancestors = %+v, want [space-1]", ancestors)
	}
}

type dingtalkTestServer struct {
	*httptest.Server
	tokenCalls   int
	spaceCalls   int
	contentCalls int
}

func newDingTalkTestServer(t *testing.T) *dingtalkTestServer {
	t.Helper()
	s := &dingtalkTestServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		s.tokenCalls++
		if r.Method != http.MethodPost {
			t.Fatalf("token method = %s", r.Method)
		}
		writeJSON(t, w, map[string]any{"accessToken": "token-1", "expireIn": 7200})
	})
	mux.HandleFunc("/v1.0/doc/spaces", func(w http.ResponseWriter, r *http.Request) {
		s.spaceCalls++
		requireToken(t, r)
		writeJSON(t, w, map[string]any{
			"spaces": []map[string]any{{
				"spaceId":     "space-1",
				"name":        "Engineering",
				"description": "Team documents",
				"modifiedAt":  "2026-07-01T00:00:00Z",
			}},
		})
	})
	mux.HandleFunc("/v1.0/doc/spaces/space-1/nodes", func(w http.ResponseWriter, r *http.Request) {
		requireToken(t, r)
		parentID := r.URL.Query().Get("parentId")
		if parentID == "" {
			writeJSON(t, w, map[string]any{
				"nodes": []map[string]any{
					{
						"nodeId":    "doc-1",
						"title":     "Project Plan",
						"type":      "document",
						"url":       "https://example.test/doc-1",
						"updatedAt": "2026-07-01T00:00:00Z",
					},
					{
						"nodeId":      "folder-1",
						"title":       "Archive",
						"type":        "folder",
						"hasChildren": true,
						"updatedAt":   "2026-07-01T00:00:00Z",
					},
				},
			})
			return
		}
		writeJSON(t, w, map[string]any{"nodes": []map[string]any{}})
	})
	mux.HandleFunc("/v1.0/doc/documents/doc-1/content", func(w http.ResponseWriter, r *http.Request) {
		s.contentCalls++
		requireToken(t, r)
		writeJSON(t, w, map[string]any{
			"documentId": "doc-1",
			"title":      "Project Plan",
			"content":    "# Project Plan\n\nHello DingTalk",
			"format":     "markdown",
			"updatedAt":  "2026-07-01T00:00:00Z",
		})
	})
	s.Server = httptest.NewServer(mux)
	return s
}

func dingtalkTestConfig(baseURL string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]any{
			"app_key":    "ding-app",
			"app_secret": "ding-secret",
			"base_url":   baseURL,
		},
		ResourceIDs: []string{"space-1"},
	}
}

func requireToken(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "token-1" {
		t.Fatalf("access token = %q, want token-1", got)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Fatal(err)
	}
}

func TestParseDingTalkTime(t *testing.T) {
	got := parseDingTalkTime("2026-07-01T00:00:00Z")
	want := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("time = %s, want %s", got, want)
	}
}
