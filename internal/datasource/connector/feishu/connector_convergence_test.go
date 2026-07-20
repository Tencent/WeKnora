package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// statefulFeishu is a fake Feishu server whose per-node export outcome can be
// changed between sync passes. A node whose obj_token is in failTokens has its
// export status poll return a failed job, so fetchNodeContent errors for it —
// modelling a transient rate-limit / export failure that later recovers.
type statefulFeishu struct {
	mu         sync.Mutex
	failTokens map[string]bool
}

func (s *statefulFeishu) setFail(tokens ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failTokens = map[string]bool{}
	for _, t := range tokens {
		s.failTokens[t] = true
	}
}

func (s *statefulFeishu) shouldFail(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failTokens[token]
}

func newStatefulFeishu(nodes []wikiNode) (*httptest.Server, *Config, *statefulFeishu) {
	s := &statefulFeishu{failTokens: map[string]bool{}}
	mux := http.NewServeMux()

	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{apiResponse: apiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, wikiSpaceListResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Items     []wikiSpace `json:"items"`
				HasMore   bool        `json:"has_more"`
				PageToken string      `json:"page_token"`
			}{Items: []wikiSpace{{SpaceID: "space1", Name: "Test Space"}}},
		})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, wikiNodeListResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Items     []wikiNode `json:"items"`
				HasMore   bool       `json:"has_more"`
				PageToken string     `json:"page_token"`
			}{Items: nodes},
		})
	})
	// Export create: ticket == obj_token so the poll below can key on it.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		writeJSON(w, exportTaskCreateResponse{
			apiResponse: apiResponse{Code: 0},
			Data:        struct{ Ticket string `json:"ticket"` }{Ticket: body.Token},
		})
	})
	// Export status poll: /open-apis/drive/v1/export_tasks/<ticket>?token=<objToken>
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/file/") && strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("fake-docx-content"))
			return
		}
		token := r.URL.Query().Get("token")
		status := 0
		if s.shouldFail(token) {
			status = 3 // failed job
		}
		resp := exportTaskStatusResponse{apiResponse: apiResponse{Code: 0}}
		resp.Data.Result.FileToken = "file-" + token
		resp.Data.Result.FileName = "exported.docx"
		resp.Data.Result.JobStatus = status
		resp.Data.Result.JobErrorMsg = "rate limited"
		writeJSON(w, resp)
	})

	ts := httptest.NewServer(mux)
	return ts, &Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: ts.URL}, s
}

// convergenceHandler models the service side across a resumable sync:
//   - Emit ingests content items (mirroring the service's ctx check + apply),
//     records failure items separately, and can simulate a mid-tree task timeout
//     by canceling the run's context after cancelAfterCalls Emit calls.
//   - Checkpoint snapshots the connector cursor (the DB round-trip).
type convergenceHandler struct {
	ingested        []string // ExternalIDs of successfully ingested content items
	failed          []string // ExternalIDs surfaced as failures (Metadata["error"])
	exportedTokens  map[string]int
	checkpoints     []feishuCursor
	calls           int
	cancelAfterCall int // >0: cancel ctx and abort on the Nth Emit call
	cancel          context.CancelFunc
}

func (h *convergenceHandler) Emit(ctx context.Context, item types.FetchedItem) error {
	if err := ctx.Err(); err != nil { // mirror service Emit
		return err
	}
	h.calls++
	if h.cancelAfterCall > 0 && h.calls >= h.cancelAfterCall {
		if h.cancel != nil {
			h.cancel()
		}
		return context.Canceled // service returns ctx err → connector aborts
	}
	if item.Metadata["error"] != "" {
		h.failed = append(h.failed, item.ExternalID)
		return nil
	}
	h.ingested = append(h.ingested, item.ExternalID)
	return nil
}

func (h *convergenceHandler) Checkpoint(ctx context.Context, cursor *types.SyncCursor) error {
	var fc feishuCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	h.checkpoints = append(h.checkpoints, fc)
	return nil
}

func lastCheckpointCursor(h *convergenceHandler, t *testing.T) *types.SyncCursor {
	t.Helper()
	if len(h.checkpoints) == 0 {
		return nil
	}
	last := h.checkpoints[len(h.checkpoints)-1]
	b, _ := json.Marshal(last)
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("rebuild persisted cursor: %v", err)
	}
	return &types.SyncCursor{ConnectorCursor: m}
}

// TestFetchStream_ResumeConvergesAfterTimeoutAndTransientFailure is the
// end-to-end proof for Tencent/WeKnora#2136: a large-ish wiki that (a) hits a
// transient per-node export failure and (b) is killed by the 2h task timeout
// mid-traversal must, on the asynq retry, resume from the last checkpoint,
// re-fetch only what is outstanding, retry the transiently-failed node, and end
// with EVERY document synced exactly once — no permanent skip, no full restart,
// no redundant re-export of already-done nodes.
func TestFetchStream_ResumeConvergesAfterTimeoutAndTransientFailure(t *testing.T) {
	// Checkpoint on every processed node so the persisted cursor is precise.
	prevN := feishuStreamCheckpointInterval
	feishuStreamCheckpointInterval = 1
	defer func() { feishuStreamCheckpointInterval = prevN }()

	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc1", ObjEditTime: "100"},
		{NodeToken: "nt2", ObjToken: "obj2", ObjType: "docx", Title: "Doc2", ObjEditTime: "200"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
		{NodeToken: "nt4", ObjToken: "obj4", ObjType: "docx", Title: "Doc4", ObjEditTime: "400"},
		{NodeToken: "nt5", ObjToken: "obj5", ObjType: "docx", Title: "Doc5", ObjEditTime: "500"},
	}
	ts, cfg, srv := newStatefulFeishu(nodes)
	defer ts.Close()
	cfgDS := makeConfig(cfg, []string{"space1"})
	c := NewConnector(RegionFeishu)

	// ---- Pass 1: obj2 export fails transiently; task "times out" on the 3rd
	// Emit call (nt1 success, nt2 failure-item, then cancel as nt3 is emitted).
	srv.setFail("obj2")
	ctx1, cancel1 := context.WithCancel(context.Background())
	h1 := &convergenceHandler{cancelAfterCall: 3, cancel: cancel1}
	_, err1 := c.FetchStream(ctx1, cfgDS, nil, h1)
	cancel1()
	if err1 == nil {
		t.Fatalf("pass 1 expected a timeout/abort error, got nil")
	}

	// Pass 1 ingested only nt1; nt2 surfaced as a failure; nt3+ never reached.
	if got := strings.Join(h1.ingested, ","); got != "nt1" {
		t.Fatalf("pass 1 ingested = %q, want just nt1", got)
	}

	// The persisted cursor (last checkpoint) must contain nt1 but NOT the
	// aborted nt3, nor the transiently-failed nt2 — otherwise resume would skip
	// them forever.
	persisted := lastCheckpointCursor(h1, t)
	if persisted == nil {
		t.Fatalf("pass 1 wrote no checkpoint — resume would restart from scratch")
	}
	var pc feishuCursor
	pb, _ := json.Marshal(persisted.ConnectorCursor)
	_ = json.Unmarshal(pb, &pc)
	p := pc.SpaceNodeTimes["space1"]
	if _, ok := p["nt1"]; !ok {
		t.Errorf("persisted cursor missing nt1 (done work lost on resume)")
	}
	if _, ok := p["nt2"]; ok {
		t.Errorf("persisted cursor recorded transiently-failed nt2 — it would be skipped forever")
	}
	if _, ok := p["nt3"]; ok {
		t.Errorf("persisted cursor recorded aborted nt3 — it would be skipped forever")
	}

	// ---- Pass 2: asynq retry. obj2 has recovered; run to completion resuming
	// from the persisted cursor.
	srv.setFail() // clear all failures
	h2 := &convergenceHandler{}
	next2, err2 := c.FetchStream(context.Background(), cfgDS, persisted, h2)
	if err2 != nil {
		t.Fatalf("pass 2 error: %v", err2)
	}

	// nt1 was already done → must be skipped (not re-exported, not re-ingested).
	for _, id := range h2.ingested {
		if id == "nt1" {
			t.Errorf("pass 2 re-ingested nt1 — redundant re-export of completed work")
		}
	}
	// Everything still outstanding (nt2 transiently-failed, nt3 aborted, nt4, nt5)
	// must be ingested in pass 2.
	want := map[string]bool{"nt2": true, "nt3": true, "nt4": true, "nt5": true}
	got2 := map[string]bool{}
	for _, id := range h2.ingested {
		got2[id] = true
	}
	for id := range want {
		if !got2[id] {
			t.Errorf("pass 2 did not ingest %s (want all outstanding nodes)", id)
		}
	}

	// ---- Convergence: union across both passes covers ALL five nodes exactly.
	union := map[string]bool{}
	for _, id := range h1.ingested {
		union[id] = true
	}
	for _, id := range h2.ingested {
		union[id] = true
	}
	for _, n := range nodes {
		if !union[n.NodeToken] {
			t.Errorf("node %s was NEVER synced across both passes — data loss (#2136)", n.NodeToken)
		}
	}
	if len(union) != len(nodes) {
		t.Errorf("synced %d distinct nodes, want %d", len(union), len(nodes))
	}

	// Final cursor is a complete snapshot of every node (next incremental sync
	// starts clean).
	var fc feishuCursor
	nb, _ := json.Marshal(next2.ConnectorCursor)
	_ = json.Unmarshal(nb, &fc)
	for _, n := range nodes {
		if _, ok := fc.SpaceNodeTimes["space1"][n.NodeToken]; !ok {
			t.Errorf("final cursor missing %s — incremental sync would re-fetch it", n.NodeToken)
		}
	}
}
