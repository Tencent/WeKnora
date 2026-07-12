package dingtalk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func testConfig(base string) *types.DataSourceConfig {
	return &types.DataSourceConfig{Credentials: map[string]interface{}{
		"app_key": "ding-key", "app_secret": "secret", "operator_id": "union-1", "base_url": base,
	}}
}

func TestConnectorListsAndFetchesDingTalkDocs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"accessToken": "token", "expireIn": 7200})
	})
	mux.HandleFunc("/v2.0/wiki/org/workspaces", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "token", r.Header.Get("x-acs-dingtalk-access-token"))
		require.Equal(t, "union-1", r.URL.Query().Get("operatorId"))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"workspaces": []map[string]interface{}{{"workspaceId": "ws-1", "workspaceName": "Engineering", "rootDentryUuid": "root-1", "url": "https://alidocs.dingtalk.com/i/nodes/root-1"}}})
	})
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parent := r.URL.Query().Get("parentNodeId")
		if parent == "root-1" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"nodes": []map[string]interface{}{{"nodeId": "folder-1", "name": "Guides", "type": "FOLDER", "hasChildren": true}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"nodes": []map[string]interface{}{{"nodeId": "doc-1", "name": "Setup", "type": "FILE", "category": "ALIDOC", "extension": "adoc", "url": "https://alidocs.dingtalk.com/i/nodes/doc-1", "modifiedTime": "2026-07-11T10:00:00Z", "workspaceId": "ws-1"}}})
	})
	mux.HandleFunc("/v2.0/doc/query/doc-1/contents", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "markdown", r.URL.Query().Get("targetFormat"))
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"taskId": 42})
	})
	mux.HandleFunc("/v2.0/doc/contents/doc-1/jobStatuses", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": 2, "contentKey": "# Setup\n\nHello DingTalk"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := NewConnector()
	cfg := testConfig(srv.URL)
	resources, err := c.ListResources(t.Context(), cfg, "")
	require.NoError(t, err)
	require.Len(t, resources, 1)
	require.Equal(t, "root-1", resources[0].ExternalID)
	items, err := c.FetchAll(t.Context(), cfg, []string{"root-1"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "doc-1", items[0].ExternalID)
	require.Equal(t, "# Setup\n\nHello DingTalk", string(items[0].Content))
	require.Equal(t, types.ChannelDingtalk, items[0].Metadata["channel"])
}

func TestParseConfigRequiresAllCredentials(t *testing.T) {
	_, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]interface{}{"app_key": "x"}})
	require.Error(t, err)
}
