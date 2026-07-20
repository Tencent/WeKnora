//go:build feishu_integration

// Real-API integration test for FetchStream against a live Feishu tenant.
//
// It is OPT-IN and never runs in the normal suite or CI: it is guarded by the
// `feishu_integration` build tag AND skips unless the required env vars are set.
//
// Run it with:
//
//	export CPLUS_INCLUDE_PATH="$(xcrun --show-sdk-path)/usr/include/c++/v1"
//	FEISHU_APP_ID=...  FEISHU_APP_SECRET=...  FEISHU_TEST_SPACE_ID=... \
//	  go test -tags feishu_integration -run TestRealAPI -v \
//	  ./internal/datasource/connector/feishu/
//
// The test is strictly READ-ONLY: it lists and exports documents from the given
// wiki space but never creates, edits or deletes anything. It whitelists
// *.feishu.cn / *.larksuite.com for SSRF in-process so it works from a dev
// machine behind a fake-ip proxy without any production code change.
package feishu

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// collectHandler records everything a real FetchStream emits so the test can
// assert coverage and resume behaviour. cancelAfter>0 simulates a task timeout
// by cancelling the run after that many successful content emits.
type collectHandler struct {
	ingested    []string
	failed      []string
	checkpoints []*types.SyncCursor
	calls       int
	cancelAfter int
	cancel      context.CancelFunc
}

func (h *collectHandler) Emit(ctx context.Context, item types.FetchedItem) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if item.Metadata["error"] != "" {
		h.failed = append(h.failed, item.ExternalID)
		return nil
	}
	h.calls++
	if h.cancelAfter > 0 && h.calls > h.cancelAfter {
		if h.cancel != nil {
			h.cancel()
		}
		return context.Canceled
	}
	h.ingested = append(h.ingested, item.ExternalID)
	return nil
}

func (h *collectHandler) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	// Snapshot the cursor JSON (mirrors the service serialising synchronously).
	b, _ := json.Marshal(cursor.ConnectorCursor)
	var m map[string]interface{}
	_ = json.Unmarshal(b, &m)
	h.checkpoints = append(h.checkpoints, &types.SyncCursor{ConnectorCursor: m})
	return nil
}

func nodeTimes(t *testing.T, cur *types.SyncCursor) map[string]map[string]string {
	t.Helper()
	if cur == nil {
		return nil
	}
	var fc feishuCursor
	b, _ := json.Marshal(cur.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	return fc.SpaceNodeTimes
}

func countNodes(m map[string]map[string]string) int {
	n := 0
	for _, v := range m {
		n += len(v)
	}
	return n
}

// TestRealAPI_ListSpaces discovers which wiki spaces the app can access, so the
// space ID for the other tests can be found without guessing. Read-only.
func TestRealAPI_ListSpaces(t *testing.T) {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	if appID == "" || appSecret == "" {
		t.Skip("set FEISHU_APP_ID, FEISHU_APP_SECRET to run")
	}
	utils.SetSSRFWhitelistFromRaw("*.feishu.cn,*.larksuite.com")

	client := NewClient(&Config{AppID: appID, AppSecret: appSecret, BaseURL: os.Getenv("FEISHU_BASE_URL")})
	spaces, err := client.ListWikiSpaces(context.Background())
	if err != nil {
		t.Fatalf("ListWikiSpaces: %v", err)
	}
	if len(spaces) == 0 {
		t.Fatalf("app can access 0 wiki spaces — add the app as a member of a knowledge base")
	}
	for _, s := range spaces {
		t.Logf("SPACE space_id=%s name=%q", s.SpaceID, s.Name)
	}
}

func TestRealAPI_FetchStreamResumeConverges(t *testing.T) {
	appID := os.Getenv("FEISHU_APP_ID")
	appSecret := os.Getenv("FEISHU_APP_SECRET")
	spaceID := os.Getenv("FEISHU_TEST_SPACE_ID")
	if appID == "" || appSecret == "" || spaceID == "" {
		t.Skip("set FEISHU_APP_ID, FEISHU_APP_SECRET, FEISHU_TEST_SPACE_ID to run")
	}

	// Whitelist the Feishu/Lark hosts for SSRF so the dev-machine fake-ip proxy
	// (198.18.x) is allowed. No production code change; scoped to these hosts.
	utils.SetSSRFWhitelistFromRaw("*.feishu.cn,*.larksuite.com")

	baseURL := os.Getenv("FEISHU_BASE_URL") // optional; defaults to region base
	cfg := &types.DataSourceConfig{
		Type: types.ConnectorTypeFeishu,
		Credentials: map[string]interface{}{
			"app_id":     appID,
			"app_secret": appSecret,
			"base_url":   baseURL,
		},
		ResourceIDs: []string{spaceID},
	}
	c := NewConnector(RegionFeishu)

	// ---- Pass 1: full sync against the real space.
	h1 := &collectHandler{}
	cur1, err := c.FetchStream(context.Background(), cfg, nil, h1)
	if err != nil {
		t.Fatalf("pass 1 (full) FetchStream error: %v", err)
	}
	n := len(h1.ingested)
	t.Logf("pass 1: ingested=%d failed=%d cursor_nodes=%d", n, len(h1.failed), countNodes(nodeTimes(t, cur1)))
	if n == 0 {
		t.Fatalf("pass 1 ingested 0 documents — create a few docs in the space, or check app scopes (wiki/docx/drive read)")
	}
	// Every ingested node must be recorded in the cursor.
	nt1 := nodeTimes(t, cur1)[spaceID]
	for _, id := range h1.ingested {
		if _, ok := nt1[id]; !ok {
			t.Errorf("pass 1 cursor missing edit time for ingested node %s", id)
		}
	}

	// ---- Pass 2: incremental with pass-1 cursor. Against the REAL API this is
	// the load-bearing check a fake can't make: real obj_edit_time must be
	// stable for unchanged docs, so NOTHING is re-ingested.
	h2 := &collectHandler{}
	cur2, err := c.FetchStream(context.Background(), cfg, cur1, h2)
	if err != nil {
		t.Fatalf("pass 2 (incremental) FetchStream error: %v", err)
	}
	t.Logf("pass 2: re-ingested=%d (want 0)", len(h2.ingested))
	if len(h2.ingested) != 0 {
		t.Errorf("pass 2 re-ingested %v — real edit-time is unstable or skip logic is broken", h2.ingested)
	}
	if got := countNodes(nodeTimes(t, cur2)); got != countNodes(nt1AsMap(nt1)) {
		t.Logf("note: cursor node count changed between passes (%d→%d) — space may have been edited concurrently", countNodes(nt1AsMap(nt1)), got)
	}

	// ---- Pass 3: interrupt mid-sync then resume, against the real API.
	// Only meaningful with at least 2 documents.
	if n < 2 {
		t.Logf("only %d doc(s); skipping interrupt/resume convergence sub-test (needs >=2)", n)
		return
	}
	prevN := feishuStreamCheckpointInterval
	feishuStreamCheckpointInterval = 1 // checkpoint every node so resume is precise
	defer func() { feishuStreamCheckpointInterval = prevN }()

	ctx3, cancel3 := context.WithCancel(context.Background())
	h3 := &collectHandler{cancelAfter: 1, cancel: cancel3} // abort after 1 success
	_, err = c.FetchStream(ctx3, cfg, nil, h3)
	cancel3()
	if err == nil {
		t.Fatalf("pass 3 expected an abort error from the simulated timeout")
	}
	if len(h3.checkpoints) == 0 {
		t.Fatalf("pass 3 wrote no checkpoint — resume would restart from scratch")
	}
	persisted := h3.checkpoints[len(h3.checkpoints)-1]
	t.Logf("pass 3: ingested=%d before abort, persisted cursor nodes=%d", len(h3.ingested), countNodes(nodeTimes(t, persisted)))

	// Resume from the persisted checkpoint; must converge to full coverage.
	h4 := &collectHandler{}
	_, err = c.FetchStream(context.Background(), cfg, persisted, h4)
	if err != nil {
		t.Fatalf("pass 4 (resume) FetchStream error: %v", err)
	}
	union := map[string]bool{}
	for _, id := range h3.ingested {
		union[id] = true
	}
	for _, id := range h4.ingested {
		union[id] = true
	}
	t.Logf("pass 3+4 union ingested=%d, pass-1 full=%d", len(union), n)
	if len(union) < n {
		t.Errorf("resume converged to %d docs but full sync had %d — a document was lost across interrupt+resume", len(union), n)
	}
	// Resume must not redo already-checkpointed work.
	done := nodeTimes(t, persisted)[spaceID]
	for _, id := range h4.ingested {
		if _, ok := done[id]; ok {
			t.Errorf("pass 4 re-ingested already-checkpointed node %s (redundant re-export)", id)
		}
	}
}

func nt1AsMap(m map[string]string) map[string]map[string]string {
	return map[string]map[string]string{"_": m}
}
