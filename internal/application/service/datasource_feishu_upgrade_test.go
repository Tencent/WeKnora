package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/drive"
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/wiki"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestStreamingFetch_FeishuUpgradeKeepsDeletionBaseline(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	defer secutils.ResetSSRFWhitelistForTest()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code": 0, "tenant_access_token": "test", "expire": 7200,
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"code": 0, "data": map[string]interface{}{"items": []interface{}{}, "has_more": false},
		})
	}))
	defer server.Close()
	for _, tc := range []struct {
		name      string
		conn      datasource.StreamingConnector
		cursorKey string
	}{
		{"feishu", wiki.NewConnector(core.RegionFeishu), "space_node_times"},
		{"lark", wiki.NewConnector(core.RegionLark), "space_node_times"},
		{"feishu_drive", drive.NewDriveConnector(core.RegionFeishu), "file_times"},
		{"lark_drive", drive.NewDriveConnector(core.RegionLark), "file_times"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &types.DataSourceConfig{
				Type:        tc.name,
				Credentials: map[string]interface{}{"app_id": "a", "app_secret": "b", "base_url": server.URL},
				ResourceIDs: []string{"resource1"}, Settings: map[string]interface{}{"resync_required": true},
			}
			baseline := &types.SyncCursor{ConnectorCursor: map[string]interface{}{
				tc.cursorKey: map[string]interface{}{"resource1": map[string]interface{}{"gone": "100"}},
			}}
			conn := tc.conn

			control := &feishuUpgradeHandler{}
			if _, err := conn.FetchStream(context.Background(), cfg, baseline, control); err != nil {
				t.Fatal(err)
			}
			if len(control.Items) != 1 || !control.Items[0].IsDeleted {
				t.Fatalf("incremental control did not detect deletion: %+v", control.Items)
			}
			raw, err := baseline.ToJSON()
			if err != nil {
				t.Fatal(err)
			}
			ds := &types.DataSource{LastSyncCursor: raw, SyncMode: types.SyncModeIncremental}
			start, err := streamStartCursor(ds, resyncRequired(cfg), 0)
			if err != nil {
				t.Fatal(err)
			}
			h := &feishuUpgradeHandler{}
			next, err := streamingFetch(context.Background(), conn, cfg, true, start, baseline, h)
			if err != nil {
				t.Fatal(err)
			}
			subsequent := &feishuUpgradeHandler{}
			if _, err = conn.FetchStream(context.Background(), cfg, next, subsequent); err != nil {
				t.Fatal(err)
			}
			if len(h.Items) != 1 || !h.Items[0].IsDeleted || h.Items[0].ExternalID != "gone" {
				t.Fatalf("full upgrade must emit the deletion: %+v", h.Items)
			}
			if len(subsequent.Items) != 0 {
				t.Fatalf("next sync repeated a reconciled deletion: %+v", subsequent.Items)
			}
		})
	}
}

type feishuUpgradeHandler struct{ Items []types.FetchedItem }

func (h *feishuUpgradeHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.Items = append(h.Items, item)
	return nil
}
func (h *feishuUpgradeHandler) Checkpoint(context.Context, *types.SyncCursor) error { return nil }
