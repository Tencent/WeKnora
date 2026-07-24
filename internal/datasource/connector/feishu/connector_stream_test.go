package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// fakeFeishuFailingExport serves the auth/spaces/nodes endpoints normally but
// fails every document export (code != 0), so fetchNodeContent returns an error
// for each supported node — modelling a rate-limited / broken export.
func fakeFeishuFailingExport(nodes []wikiNode) (*httptest.Server, *Config) {
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
	// Export creation fails for every document.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, apiResponse{Code: 1, Msg: "export unavailable"})
	})
	ts := httptest.NewServer(mux)
	return ts, &Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: ts.URL}
}

// A node whose fetch fails must NOT have its new edit time recorded in the
// returned cursor: recording it would make the next sync's unchanged fast-path
// skip it forever, silently dropping a document on a transient export failure
// (Tencent/WeKnora#2136). With a prior edit time known, the prior value is
// retained so prev != current next run and the node is retried.
func TestFetchStream_FailedFetchRetainsPriorCursor(t *testing.T) {
	nodes := []wikiNode{{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"}}
	ts, cfg := fakeFeishuFailingExport(nodes)
	defer ts.Close()

	cursor := makeStreamCursor(t, map[string]map[string]string{"space1": {"nt1": "50"}}) // prior, older

	c := NewConnector(RegionFeishu)
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), cursor, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// A failure item must be surfaced, not silently dropped.
	if len(h.emitted) != 1 || h.emitted[0].Metadata["error"] == "" {
		t.Fatalf("expected 1 emitted failure item with error metadata, got %+v", h.emitted)
	}

	var fc feishuCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	got := fc.SpaceNodeTimes["space1"]["nt1"]
	if got == "100" {
		t.Fatalf("failed node advanced to current edit time %q — it will be skipped forever", got)
	}
	if got != "50" {
		t.Errorf("failed node cursor = %q, want prior value \"50\" (retry next run)", got)
	}
}

// With no prior cursor entry, a failed fetch must leave the node out of the
// returned cursor entirely, so the next run treats it as new and retries it.
func TestFetchStream_FailedFetchNoPriorOmitsFromCursor(t *testing.T) {
	nodes := []wikiNode{{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"}}
	ts, cfg := fakeFeishuFailingExport(nodes)
	defer ts.Close()

	c := NewConnector(RegionFeishu)
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	var fc feishuCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	if v, ok := fc.SpaceNodeTimes["space1"]["nt1"]; ok {
		t.Errorf("failed node recorded in cursor as %q; want absent so it is retried next run", v)
	}
}

// recordingHandler captures the items and checkpoints a streaming fetch emits.
// Checkpoints are snapshotted (JSON-encoded) at call time — mirroring the
// service, which serializes the cursor synchronously inside Checkpoint — so the
// test observes the cursor state as it was when Checkpoint was called, not the
// connector's later-mutated map.
type recordingHandler struct {
	emitted     []types.FetchedItem
	checkpoints []feishuCursor
	emitErr     func(item types.FetchedItem) error
}

func (h *recordingHandler) Emit(ctx context.Context, item types.FetchedItem) error {
	if h.emitErr != nil {
		if err := h.emitErr(item); err != nil {
			return err
		}
	}
	h.emitted = append(h.emitted, item)
	return nil
}

func (h *recordingHandler) Checkpoint(ctx context.Context, cursor *types.SyncCursor) error {
	var fc feishuCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	h.checkpoints = append(h.checkpoints, fc)
	return nil
}

func makeStreamCursor(t *testing.T, spaceNodeTimes map[string]map[string]string) *types.SyncCursor {
	t.Helper()
	prev := feishuCursor{SpaceNodeTimes: spaceNodeTimes}
	b, _ := json.Marshal(prev)
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("build cursor: %v", err)
	}
	return &types.SyncCursor{ConnectorCursor: m}
}

// A nil-cursor stream emits every supported node once and skips unsupported
// types, and the returned cursor records the edit time of every discovered node
// (so the next incremental run can detect changes).
func TestFetchStream_EmitsSupportedSkipsUnsupported(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt2", ObjToken: "obj2", ObjType: "mindnote", Title: "Brain", ObjEditTime: "200"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector(RegionFeishu)
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 2 {
		t.Fatalf("emitted %d items, want 2 (nt1, nt3)", len(h.emitted))
	}
	if h.emitted[0].ExternalID != "nt1" || h.emitted[1].ExternalID != "nt3" {
		t.Errorf("emitted ids = %q,%q; want nt1,nt3", h.emitted[0].ExternalID, h.emitted[1].ExternalID)
	}

	var fc feishuCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	times := fc.SpaceNodeTimes["space1"]
	for _, tok := range []string{"nt1", "nt2", "nt3"} {
		if _, ok := times[tok]; !ok {
			t.Errorf("returned cursor missing edit time for %s", tok)
		}
	}
}

// When a cursor already records a node at its current edit time, that node is
// unchanged and must not be re-emitted; only new/changed nodes stream through.
// This is the resume/incremental-skip behavior that lets a timed-out sync
// converge across retries instead of re-exporting everything.
func TestFetchStream_SkipsUnchangedNodesFromCursor(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	cursor := makeStreamCursor(t, map[string]map[string]string{
		"space1": {"nt1": "100"}, // nt1 unchanged; nt3 unknown → new
	})

	c := NewConnector(RegionFeishu)
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), cursor, h); err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 1 {
		t.Fatalf("emitted %d items, want 1 (only changed nt3)", len(h.emitted))
	}
	if h.emitted[0].ExternalID != "nt3" {
		t.Errorf("emitted id = %q, want nt3", h.emitted[0].ExternalID)
	}
}

// Checkpoints must persist progress at page boundaries so a crash mid-sync
// resumes from the last checkpoint. With the interval set to 1, each emitted
// item triggers a checkpoint, and the first checkpoint must already contain the
// first node's edit time.
func TestFetchStream_CheckpointsProgress(t *testing.T) {
	prev := feishuStreamCheckpointInterval
	feishuStreamCheckpointInterval = 1
	defer func() { feishuStreamCheckpointInterval = prev }()

	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector(RegionFeishu)
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h); err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.checkpoints) == 0 {
		t.Fatalf("expected at least one checkpoint")
	}
	first := h.checkpoints[0]
	if _, ok := first.SpaceNodeTimes["space1"]["nt1"]; !ok {
		t.Errorf("first checkpoint missing nt1 progress: %+v", first.SpaceNodeTimes)
	}
}

// Checkpoints must ALSO fire on elapsed time, not only every N nodes.
// Otherwise a sync with fewer than the node interval of slow (rate-limited)
// exports reaches the 2h task timeout having never checkpointed, and resumes
// from scratch forever — exactly the #2136 "never fully syncs" case. With the
// node interval effectively disabled and the time interval at 0, every
// processed node must still produce a checkpoint.
func TestFetchStream_CheckpointsOnElapsedTime(t *testing.T) {
	prevN := feishuStreamCheckpointInterval
	prevT := feishuStreamCheckpointMaxInterval
	feishuStreamCheckpointInterval = 1 << 30 // never fires by count
	feishuStreamCheckpointMaxInterval = 0     // fires by elapsed time every node
	defer func() {
		feishuStreamCheckpointInterval = prevN
		feishuStreamCheckpointMaxInterval = prevT
	}()

	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector(RegionFeishu)
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h); err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}
	if len(h.checkpoints) == 0 {
		t.Fatalf("expected time-based checkpoints even though node interval never fires")
	}
}

// An Emit error aborts the stream immediately — the connector must return the
// error and stop fetching further nodes (the sync is failing; do not burn API
// budget on the rest of the tree).
func TestFetchStream_EmitErrorAborts(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	boom := errors.New("ingest failed")
	c := NewConnector(RegionFeishu)
	h := &recordingHandler{emitErr: func(item types.FetchedItem) error { return boom }}
	_, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if !errors.Is(err, boom) {
		t.Fatalf("FetchStream() error = %v, want %v", err, boom)
	}
	if len(h.emitted) != 0 {
		t.Errorf("emitted %d items, want 0 (aborted on first emit)", len(h.emitted))
	}
}
