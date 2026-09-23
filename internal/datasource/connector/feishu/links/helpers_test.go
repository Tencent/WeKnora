package links

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	_ = os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func makeLinksConfig(cfg *core.Config, urls []string, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeFeishuLinks,
		Credentials: map[string]interface{}{"app_id": cfg.AppID, "app_secret": cfg.AppSecret, "base_url": cfg.BaseURL},
		ResourceIDs: resourceIDs,
		Settings:    map[string]interface{}{"urls": urls},
	}
}

type recordingHandler struct {
	emitted []types.FetchedItem
}

func (h *recordingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.emitted = append(h.emitted, item)
	return nil
}

func (h *recordingHandler) Checkpoint(_ context.Context, _ *types.SyncCursor) error { return nil }

func wikiNodeJSON(token, objToken, objType, title, edit string) core.WikiNode {
	return core.WikiNode{
		NodeToken:   token,
		ObjToken:    objToken,
		ObjType:     objType,
		Title:       title,
		ObjEditTime: edit,
		HasChild:    true, // must still not recurse
	}
}
