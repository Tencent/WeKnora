package dingtalk

import (
	"context"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestE2E_RealAPI exercises the connector against the real DingTalk Open
// Platform. It is skipped unless credentials are provided via environment
// variables:
//
//	DINGTALK_E2E_APP_KEY=dingxxxx DINGTALK_E2E_APP_SECRET=xxxx \
//	  go test ./internal/datasource/connector/dingtalk/ -run TestE2E -v
//
// The app must have the Wiki.Workspace.Read, Wiki.Node.Read,
// Storage.File.Read and qyapi_get_member scopes enabled.
func TestE2E_RealAPI(t *testing.T) {
	appKey := os.Getenv("DINGTALK_E2E_APP_KEY")
	appSecret := os.Getenv("DINGTALK_E2E_APP_SECRET")
	if appKey == "" || appSecret == "" {
		t.Skip("DINGTALK_E2E_APP_KEY / DINGTALK_E2E_APP_SECRET not set; skipping real-API test")
	}

	config := &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"app_key":    appKey,
			"app_secret": appSecret,
		},
	}
	if operatorID := os.Getenv("DINGTALK_E2E_OPERATOR_ID"); operatorID != "" {
		config.Credentials["operator_id"] = operatorID
	}

	ctx := context.Background()
	connector := NewConnector()

	if err := connector.Validate(ctx, config); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
	t.Log("Validate: OK")

	workspaces, err := connector.ListResources(ctx, config, "")
	if err != nil {
		t.Fatalf("ListResources(root) failed: %v", err)
	}
	if len(workspaces) == 0 {
		t.Fatalf("no workspaces visible to the operator; create a knowledge base first")
	}
	for _, ws := range workspaces {
		t.Logf("workspace: id=%s name=%q", ws.ExternalID, ws.Name)
	}

	wsID := workspaces[0].ExternalID
	children, err := connector.ListResources(ctx, config, wsID)
	if err != nil {
		t.Fatalf("ListResources(%s) failed: %v", wsID, err)
	}
	for _, child := range children {
		t.Logf("node: id=%s type=%s name=%q hasChildren=%v", child.ExternalID, child.Type, child.Name, child.HasChildren)
	}

	items, err := connector.FetchAll(ctx, config, []string{wsID})
	if err != nil {
		t.Fatalf("FetchAll failed: %v", err)
	}
	for _, item := range items {
		preview := truncate(string(item.Content), 60) // rune-safe
		if errMsg, ok := item.Metadata["error"]; ok && errMsg != "" {
			t.Logf("item FAILED: id=%s title=%q err=%s", item.ExternalID, item.Title, errMsg)
			continue
		}
		t.Logf("item: id=%s title=%q file=%s bytes=%d preview=%q",
			item.ExternalID, item.Title, item.FileName, len(item.Content), preview)
	}

	// Incremental: first run fetches everything, second run with the returned
	// cursor should fetch nothing.
	config.ResourceIDs = []string{wsID}
	first, cursor, err := connector.FetchIncremental(ctx, config, nil)
	if err != nil {
		t.Fatalf("FetchIncremental(nil) failed: %v", err)
	}
	t.Logf("incremental first run: %d items", len(first))

	second, _, err := connector.FetchIncremental(ctx, config, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental(cursor) failed: %v", err)
	}
	if len(second) != 0 {
		t.Errorf("incremental second run: expected 0 items, got %d", len(second))
	} else {
		t.Log("incremental second run: 0 items (cursor works)")
	}
}
