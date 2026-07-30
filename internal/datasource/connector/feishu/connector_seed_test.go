//go:build feishu_integration

// Seeder for the real-API integration test. OPT-IN (feishu_integration tag +
// env vars) so it never runs in the normal suite or CI.
//
// It WRITES to your Feishu tenant: it creates a handful of docx documents under
// the given wiki space, each titled "WeKnora sync test doc N" so you can find
// and delete them afterwards. Requires app write scopes:
//   - wiki:wiki           (create wiki nodes)
//   - docx:document       (write docx content)
// plus the app being a member of the space.
//
// Run:
//
//	export CPLUS_INCLUDE_PATH="$(xcrun --show-sdk-path)/usr/include/c++/v1"
//	FEISHU_APP_ID=... FEISHU_APP_SECRET=... FEISHU_TEST_SPACE_ID=... FEISHU_SEED_COUNT=3 \
//	  go test -tags feishu_integration -run TestRealAPI_SeedDocs -v \
//	  ./internal/datasource/connector/feishu/
package feishu

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

// wikiNodeCreateResp is the subset of POST /wiki/v2/spaces/:id/nodes we need.
type wikiNodeCreateResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Node struct {
			NodeToken string `json:"node_token"`
			ObjToken  string `json:"obj_token"`
			ObjType   string `json:"obj_type"`
			Title     string `json:"title"`
		} `json:"node"`
	} `json:"data"`
}

// TestRealAPI_CreateSpace creates a fresh knowledge base owned by the app, so
// the app can seed and read it without a human adding it to an existing space.
// It prints the new space_id for use as FEISHU_TEST_SPACE_ID.
func TestRealAPI_CreateSpace(t *testing.T) {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		t.Skip("set FEISHU_APP_ID, FEISHU_APP_SECRET to run")
	}
	utils.SetSSRFWhitelistFromRaw("*.feishu.cn,*.larksuite.com")
	client := NewClient(&Config{AppID: appID, AppSecret: appSecret, BaseURL: os.Getenv("FEISHU_BASE_URL")})

	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Space struct {
				SpaceID string `json:"space_id"`
				Name    string `json:"name"`
			} `json:"space"`
		} `json:"data"`
	}
	err := client.doRequest(context.Background(), "POST", "/open-apis/wiki/v2/spaces",
		map[string]interface{}{
			"name":        "WeKnora Resilience Test KB",
			"description": "Auto-created for connector resilience testing (#2136). Safe to delete.",
		}, &resp)
	if err != nil {
		t.Fatalf("create space: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("create space: code=%d msg=%s (needs wiki:wiki write scope)", resp.Code, resp.Msg)
	}
	t.Logf("CREATED_SPACE space_id=%s name=%q", resp.Data.Space.SpaceID, resp.Data.Space.Name)
}

func TestRealAPI_SeedDocs(t *testing.T) {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	spaceID := os.Getenv("FEISHU_TEST_SPACE_ID")
	if appID == "" || appSecret == "" || spaceID == "" {
		t.Skip("set FEISHU_APP_ID, FEISHU_APP_SECRET, FEISHU_TEST_SPACE_ID to run")
	}
	count := 3
	if v := os.Getenv("FEISHU_SEED_COUNT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			count = n
		}
	}

	utils.SetSSRFWhitelistFromRaw("*.feishu.cn,*.larksuite.com")

	baseURL := os.Getenv("FEISHU_BASE_URL")
	client := NewClient(&Config{AppID: appID, AppSecret: appSecret, BaseURL: baseURL})
	ctx := context.Background()

	for i := 1; i <= count; i++ {
		title := fmt.Sprintf("WeKnora sync test doc %d", i)

		// 1) Create a docx wiki node.
		var created wikiNodeCreateResp
		err := client.doRequest(ctx, "POST",
			fmt.Sprintf("/open-apis/wiki/v2/spaces/%s/nodes", spaceID),
			map[string]interface{}{
				"obj_type":  "docx",
				"node_type": "origin",
				"title":     title,
			}, &created)
		if err != nil {
			t.Fatalf("create wiki node %d: %v", i, err)
		}
		if created.Code != 0 {
			t.Fatalf("create wiki node %d: code=%d msg=%s (check wiki:wiki write scope + space membership)", i, created.Code, created.Msg)
		}
		docID := created.Data.Node.ObjToken // for docx, obj_token == document_id

		// 2) Add a text block so the export is non-empty.
		var blockResp struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
		}
		berr := client.doRequest(ctx, "POST",
			fmt.Sprintf("/open-apis/docx/v1/documents/%s/blocks/%s/children", docID, docID),
			map[string]interface{}{
				"index": 0,
				"children": []map[string]interface{}{{
					"block_type": 2, // text
					"text": map[string]interface{}{
						"elements": []map[string]interface{}{{
							"text_run": map[string]interface{}{
								"content": fmt.Sprintf("This is WeKnora sync test document number %d. It exists to validate connector resilience (issue #2136).", i),
							},
						}},
					},
				}},
			}, &blockResp)
		if berr != nil {
			t.Logf("warn: add content to doc %d failed (%v) — doc still created, export may be empty", i, berr)
		} else if blockResp.Code != 0 {
			t.Logf("warn: add content to doc %d: code=%d msg=%s (needs docx:document write scope) — doc still created", i, blockResp.Code, blockResp.Msg)
		}

		t.Logf("created doc %d: title=%q node_token=%s obj_token=%s", i, title, created.Data.Node.NodeToken, docID)
	}
	t.Logf("seeded %d docs under space %s. Now run TestRealAPI_FetchStreamResumeConverges.", count, spaceID)
}
