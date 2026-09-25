package drive

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
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func makeConfig(cfg *core.Config, resourceIDs []string) *types.DataSourceConfig {
	creds := map[string]interface{}{
		"app_id":     cfg.AppID,
		"app_secret": cfg.AppSecret,
		"base_url":   cfg.BaseURL,
	}
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeFeishu,
		Credentials: creds,
		ResourceIDs: resourceIDs,
	}
}

type recordingHandler struct {
	emitted     []types.FetchedItem
	checkpoints []core.FeishuDriveCursor
	emitErr     func(item types.FetchedItem) error
}

func (h *recordingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	if h.emitErr != nil {
		if err := h.emitErr(item); err != nil {
			return err
		}
	}
	h.emitted = append(h.emitted, item)
	return nil
}

func (h *recordingHandler) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	var fc core.FeishuDriveCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	h.checkpoints = append(h.checkpoints, fc)
	return nil
}
