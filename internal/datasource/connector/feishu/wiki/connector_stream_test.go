package wiki

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeFeishuFailingExport serves the auth/spaces/nodes endpoints normally but
// fails every document export (code != 0), so fetchNodeContent returns an error
// for each supported node — modelling a rate-limited / broken export.
func fakeFeishuFailingExport(nodes []core.WikiNode) (*httptest.Server, *core.Config) {
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.TokenResponse{ApiResponse: core.ApiResponse{Code: 0}, TenantAccessToken: "fake-token", Expire: 7200})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.WikiSpaceListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiSpaceListData{Items: []core.WikiSpace{{SpaceID: "space1", Name: "Test Space"}}},
		})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.WikiNodeListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiNodeListData{Items: nodes},
		})
	})
	// Export creation fails for every document.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.ApiResponse{Code: 1, Msg: "export unavailable"})
	})
	ts := httptest.NewServer(mux)
	return ts, &core.Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: ts.URL}
}

// A node whose core.Fetch fails must NOT have its new edit time recorded in the
// returned cursor: recording it would make the next sync's unchanged fast-path
// core.Skip it forever, silently dropping a document on a transient export failure
// (Tencent/WeKnora#2136). With a prior edit time known, the prior value is
// retained so prev != current next run and the node is retried.
func TestFetchStream_FailedFetchRetainsPriorCursor(t *testing.T) {
	nodes := []core.WikiNode{{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"}}
	ts, cfg := fakeFeishuFailingExport(nodes)
	defer ts.Close()

	cursor := makeStreamCursor(t, map[string]map[string]string{"space1": {"nt1": "50"}}) // prior, older

	c := NewConnector(core.RegionFeishu)
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), cursor, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// A failure item must be surfaced, not silently dropped.
	if len(h.emitted) != 1 || h.emitted[0].Metadata["error"] == "" {
		t.Fatalf("expected 1 emitted failure item with error metadata, got %+v", h.emitted)
	}

	var fc core.FeishuCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	got := fc.SpaceNodeTimes["space1"]["nt1"]
	if got == "100" {
		t.Fatalf("failed node advanced to current edit time %q — it will be core.Skipped forever", got)
	}
	if got != "50" {
		t.Errorf("failed node cursor = %q, want prior value \"50\" (retry next run)", got)
	}
}

// With no prior cursor entry, a failed core.Fetch must leave the node out of the
// returned cursor entirely, so the next run treats it as new and retries it.
func TestFetchStream_FailedFetchNoPriorOmitsFromCursor(t *testing.T) {
	nodes := []core.WikiNode{{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"}}
	ts, cfg := fakeFeishuFailingExport(nodes)
	defer ts.Close()

	c := NewConnector(core.RegionFeishu)
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	var fc core.FeishuCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	if v, ok := fc.SpaceNodeTimes["space1"]["nt1"]; ok {
		t.Errorf("failed node recorded in cursor as %q; want absent so it is retried next run", v)
	}
}

// recordingHandler captures the items and checkpoints a streaming core.Fetch emits.
// Checkpoints are snapshotted (JSON-encoded) at call time — mirroring the
// service, which serializes the cursor synchronously inside Checkpoint — so the
// test observes the cursor state as it was when Checkpoint was called, not the
// connector's later-mutated map.
type recordingHandler struct {
	emitted     []types.FetchedItem
	checkpoints []core.FeishuCursor
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
	var fc core.FeishuCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	h.checkpoints = append(h.checkpoints, fc)
	return nil
}

func makeStreamCursor(t *testing.T, spaceNodeTimes map[string]map[string]string) *types.SyncCursor {
	t.Helper()
	prev := core.FeishuCursor{SpaceNodeTimes: spaceNodeTimes}
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
	nodes := []core.WikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt2", ObjToken: "obj2", ObjType: "mindnote", Title: "Brain", ObjEditTime: "200"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector(core.RegionFeishu)
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

	var fc core.FeishuCursor
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
// This is the resume/incremental-core.Skip behavior that lets a timed-out sync
// converge across retries instead of re-exporting everything.
func TestFetchStream_SkipsUnchangedNodesFromCursor(t *testing.T) {
	nodes := []core.WikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	cursor := makeStreamCursor(t, map[string]map[string]string{
		"space1": {"nt1": "100"}, // nt1 unchanged; nt3 unknown → new
	})

	c := NewConnector(core.RegionFeishu)
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
// resumes from the last Checkpoint. With the interval set to 1, each emitted
// item triggers a Checkpoint, and the first Checkpoint must already contain the
// first node's edit time.
func TestFetchStream_CheckpointsProgress(t *testing.T) {
	prev := core.FeishuStreamCheckpointInterval
	core.FeishuStreamCheckpointInterval = 1
	defer func() { core.FeishuStreamCheckpointInterval = prev }()

	nodes := []core.WikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector(core.RegionFeishu)
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h); err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.checkpoints) == 0 {
		t.Fatalf("expected at least one Checkpoint")
	}
	first := h.checkpoints[0]
	if _, ok := first.SpaceNodeTimes["space1"]["nt1"]; !ok {
		t.Errorf("first Checkpoint missing nt1 progress: %+v", first.SpaceNodeTimes)
	}
}

// Checkpoints must ALSO fire on elapsed time, not only every N nodes.
// Otherwise a sync with fewer than the node interval of slow (rate-limited)
// exports reaches the 2h task timeout having never checkpointed, and resumes
// from scratch forever — exactly the #2136 "never fully syncs" case. With the
// node interval effectively disabled and the time interval at 0, every
// processed node must still produce a Checkpoint.
func TestFetchStream_CheckpointsOnElapsedTime(t *testing.T) {
	prevN := core.FeishuStreamCheckpointInterval
	prevT := core.FeishuStreamCheckpointMaxInterval
	core.FeishuStreamCheckpointInterval = 1 << 30 // never fires by count
	core.FeishuStreamCheckpointMaxInterval = 0    // fires by elapsed time every node
	defer func() {
		core.FeishuStreamCheckpointInterval = prevN
		core.FeishuStreamCheckpointMaxInterval = prevT
	}()

	nodes := []core.WikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector(core.RegionFeishu)
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
	nodes := []core.WikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", ObjEditTime: "100"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "docx", Title: "Doc3", ObjEditTime: "300"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	boom := errors.New("ingest failed")
	c := NewConnector(core.RegionFeishu)
	h := &recordingHandler{emitErr: func(item types.FetchedItem) error { return boom }}
	_, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if !errors.Is(err, boom) {
		t.Fatalf("FetchStream() error = %v, want %v", err, boom)
	}
	if len(h.emitted) != 0 {
		t.Errorf("emitted %d items, want 0 (aborted on first Emit)", len(h.emitted))
	}
}

// ──────────────────────────────────────────────────────────────────────
// Stream integration: docx blocks fan-out (multi-item) through FetchStream
// ──────────────────────────────────────────────────────────────────────

// TestFetchStream_DocxMultiItem is a stream-level integration test proving that a
// docx node fans out to a main Markdown item + an attachment item through the real
// FetchStream → fetchNodeContent → core.FetchDocxWithBlocks path. The fake server
// serves the blocks API (one text block + one file block) and the drive download;
// no export endpoint is registered, so any accidental fall-through to the export
// path would 404 and surface as a core.Fetch error.
func TestFetchStream_DocxMultiItem(t *testing.T) {
	const (
		nodeToken = "nt-blocks"
		objToken  = "obj-blocks"
		attToken  = "ft-stream-att"
		attName   = "slides.pdf"
	)
	// Attachment content must exceed core.MinAttachmentBytes (2 KiB).
	attContent := bytes.Repeat([]byte("a"), core.MinAttachmentBytes+1)

	nodes := []core.WikiNode{{
		NodeToken:   nodeToken,
		ObjToken:    objToken,
		ObjType:     "docx",
		Title:       "Stream Blocks Doc",
		ObjEditTime: "500",
	}}
	ts, cfg := fakeFeishuWithBlocks(nodes, objToken, attToken, attName, attContent)
	defer ts.Close()

	c := NewConnector(core.RegionFeishu)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.emitted) != 2 {
		t.Fatalf("expected 2 emitted items (main doc + attachment), got %d: %+v", len(h.emitted), h.emitted)
	}

	main := h.emitted[0]
	if main.ExternalID != nodeToken {
		t.Errorf("items[0].ExternalID = %q, want %q", main.ExternalID, nodeToken)
	}
	if main.ContentType != "text/markdown" {
		t.Errorf("items[0].ContentType = %q, want text/markdown", main.ContentType)
	}
	if !main.ReplacesSubtree {
		t.Errorf("items[0].ReplacesSubtree = false, want true")
	}

	att := h.emitted[1]
	wantAttID := nodeToken + "#file#" + attToken
	if att.ExternalID != wantAttID {
		t.Errorf("items[1].ExternalID = %q, want %q", att.ExternalID, wantAttID)
	}
	if att.Metadata["attachment"] != "true" {
		t.Errorf("items[1].Metadata[attachment] = %q, want \"true\"", att.Metadata["attachment"])
	}
}

// fakeFeishuWithBlocksFallback returns a server where the blocks API returns HTTP 500
// (simulating a permission/scope error) and the export task path returns success.
// This wires the fallback path: core.FetchDocxWithBlocks → blocks error → fetchViaExport.
func fakeFeishuWithBlocksFallback(nodes []core.WikiNode, docToken string) (*httptest.Server, *core.Config) {
	mux := http.NewServeMux()

	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.TokenResponse{
			ApiResponse:       core.ApiResponse{Code: 0},
			TenantAccessToken: "fake-token",
			Expire:            7200,
		})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.WikiSpaceListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiSpaceListData{Items: []core.WikiSpace{{SpaceID: "space1", Name: "Test Space"}}},
		})
	})
	mux.HandleFunc("/open-apis/wiki/v2/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.WikiNodeListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.WikiNodeListData{Items: nodes},
		})
	})

	// Blocks API — always returns HTTP 500 (missing scope / permission denied).
	blocksPath := "/open-apis/docx/v1/documents/" + docToken + "/blocks"
	mux.HandleFunc(blocksPath, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":99991400,"msg":"insufficient scope"}`))
	})

	// Export task creation → returns ticket.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, core.ExportTaskCreateResponse{
				ApiResponse: core.ApiResponse{Code: 0},
				Data:        core.ExportTaskCreateData{Ticket: "ticket-fb"},
			})
			return
		}
		http.NotFound(w, r)
	})
	// Export task status polling.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/ticket-fb", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, core.ExportTaskStatusResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data: core.ExportTaskStatusData{
				Result: core.ExportTaskResult{
					FileToken: "ft-export-fallback",
					FileSize:  512,
					JobStatus: 0, // done
					FileName:  "fallback.docx",
				},
			},
		})
	})
	// Export file download.
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/file/ft-export-fallback/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("fake-exported-fallback-binary"))
	})
	// Drive file download endpoint (not expected to be called in the fallback path,
	// but registered to avoid 404 panic if the mux catches a stray request).
	mux.HandleFunc("/open-apis/drive/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("should-not-be-called"))
			return
		}
		http.NotFound(w, r)
	})

	ts := httptest.NewServer(mux)
	return ts, &core.Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: ts.URL}
}

// TestFetchStream_DocxBlocksFallback proves that when the blocks API returns HTTP 500
// (e.g. missing docx:document:readonly scope), the connector automatically falls back
// to the export-API path and emits exactly one item with ContentType
// "application/octet-stream", not "text/markdown". No hard failure occurs —
// the sync completes successfully with the exported binary.
func TestFetchStream_DocxBlocksFallback(t *testing.T) {
	const (
		nodeToken = "nt-fallback"
		objToken  = "obj-fallback"
	)
	nodes := []core.WikiNode{{
		NodeToken:   nodeToken,
		ObjToken:    objToken,
		ObjType:     "docx",
		Title:       "Fallback Doc",
		ObjEditTime: "600",
	}}
	ts, cfg := fakeFeishuWithBlocksFallback(nodes, objToken)
	defer ts.Close()

	c := NewConnector(core.RegionFeishu)
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// The fallback path must Emit exactly one item (exported binary, not multi-item).
	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (export fallback), got %d: %+v", len(h.emitted), h.emitted)
	}
	item := h.emitted[0]
	if item.ExternalID != nodeToken {
		t.Errorf("item.ExternalID = %q, want %q", item.ExternalID, nodeToken)
	}
	if item.ContentType == "text/markdown" {
		t.Errorf("item.ContentType = %q; want application/octet-stream (export fallback, not blocks path)", item.ContentType)
	}
	if item.ContentType != "application/octet-stream" {
		t.Errorf("item.ContentType = %q, want application/octet-stream", item.ContentType)
	}
	if item.Metadata["error"] != "" {
		t.Errorf("fallback item should have no error metadata, got %q", item.Metadata["error"])
	}
	// The export fallback cannot re-enumerate the doc's attachments, so it must
	// NOT set ReplacesSubtree — otherwise a transient blocks-API failure would
	// sweep and permanently delete the good attachment children from the prior
	// blocks-path sync with nothing to replace them.
	if item.ReplacesSubtree {
		t.Error("export-fallback item must not set ReplacesSubtree (would delete good prior attachments on a transient failure)")
	}
}
