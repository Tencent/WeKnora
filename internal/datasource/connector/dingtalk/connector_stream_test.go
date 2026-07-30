package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// ──────────────────────────────────────────────────────────────────────
// Stream test infrastructure
// ──────────────────────────────────────────────────────────────────────

// recordingHandler captures all Emit and Checkpoint calls for assertions.
type recordingHandler struct {
	emitted       []types.FetchedItem
	checkpoints   []dingtalkCursor
	emitErr       func(item types.FetchedItem) error
	checkpointErr error
}

func (h *recordingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	if h.emitErr != nil {
		return h.emitErr(item)
	}
	h.emitted = append(h.emitted, item)
	return nil
}

func (h *recordingHandler) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	if h.checkpointErr != nil {
		return h.checkpointErr
	}
	var dc dingtalkCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &dc)
	h.checkpoints = append(h.checkpoints, dc)
	return nil
}

func makeStreamCursor(t *testing.T, m map[string]map[string]string) *types.SyncCursor {
	t.Helper()
	dc := dingtalkCursor{
		SpaceNodeTimes: m,
	}
	b, _ := json.Marshal(dc)
	var raw map[string]interface{}
	_ = json.Unmarshal(b, &raw)
	return &types.SyncCursor{ConnectorCursor: raw}
}

func makeStreamConfig(cfg *Config, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{"app_key": cfg.AppKey, "app_secret": cfg.AppSecret, "base_url": cfg.BaseURL},
		ResourceIDs: resourceIDs,
	}
}

// ──────────────────────────────────────────────────────────────────────
// FetchStream tests
// ──────────────────────────────────────────────────────────────────────

func TestFetchStream_FullSync(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc 1", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "Doc 2", Type: "doc", EditTime: 200},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream error: %v", err)
	}

	// Both docs should be emitted.
	if len(h.emitted) != 2 {
		t.Fatalf("expected 2 emitted items, got %d", len(h.emitted))
	}

	// Cursor should contain both nodes.
	var fc dingtalkCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	if len(fc.SpaceNodeTimes["space1"]) != 2 {
		t.Errorf("cursor has %d nodes, want 2", len(fc.SpaceNodeTimes["space1"]))
	}
}

func TestFetchStream_IncrementalSkipsUnchanged(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "A", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "B", Type: "doc", EditTime: 200},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	h := &recordingHandler{}

	// First sync: both nodes emitted.
	cursor, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if len(h.emitted) != 2 {
		t.Fatalf("first sync: expected 2 items, got %d", len(h.emitted))
	}

	// Second sync with same cursor: 0 items (all unchanged).
	h2 := &recordingHandler{}
	_, err = c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), cursor, h2)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if len(h2.emitted) != 0 {
		t.Errorf("second sync: expected 0 items, got %d", len(h2.emitted))
	}
}

func TestFetchStream_FailedFetchRetainsPriorCursor(t *testing.T) {
	// A node whose fetch fails must NOT have its new edit time recorded in
	// the returned cursor: recording it would make the next sync's unchanged
	// fast-path skip it forever, silently dropping a document on a transient
	// failure. With a prior edit time known, the prior value is retained so
	// prev != current next run and the node is retried.
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc", Type: "doc", EditTime: 100},
	}
	ts, cfg := fakeDingTalkFailingDetail(nodes)
	defer ts.Close()

	cursor := makeStreamCursor(t, map[string]map[string]string{"space1": {"n1": "50"}}) // prior, older

	c := NewConnector()
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), cursor, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// A failure item must be surfaced, not silently dropped.
	if len(h.emitted) != 1 || h.emitted[0].Metadata["error"] == "" {
		t.Fatalf("expected 1 emitted failure item with error metadata, got %+v", h.emitted)
	}

	var fc dingtalkCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	got := fc.SpaceNodeTimes["space1"]["n1"]
	if got == "100" {
		t.Fatalf("failed node advanced to current edit time %q — it will be skipped forever", got)
	}
	if got != "50" {
		t.Errorf("failed node cursor = %q, want prior value \"50\" (retry next run)", got)
	}
}

func TestFetchStream_FailedFetchNoPriorOmitsFromCursor(t *testing.T) {
	// With no prior cursor entry, a failed fetch must leave the node out of
	// the returned cursor entirely, so the next run treats it as new and retries.
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc", Type: "doc", EditTime: 100},
	}
	ts, cfg := fakeDingTalkFailingDetail(nodes)
	defer ts.Close()

	c := NewConnector()
	h := &recordingHandler{}
	next, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	var fc dingtalkCursor
	b, _ := json.Marshal(next.ConnectorCursor)
	_ = json.Unmarshal(b, &fc)
	if v, ok := fc.SpaceNodeTimes["space1"]["n1"]; ok {
		t.Errorf("failed node recorded in cursor as %q; want absent so it is retried next run", v)
	}
}

func TestFetchStream_CheckpointsProgress(t *testing.T) {
	// Checkpoints must persist progress at page boundaries so a crash mid-sync
	// resumes from the last checkpoint. With the interval set to 1, each emitted
	// item triggers a checkpoint, and the first checkpoint must already contain
	// the first node's edit time.
	prev := dingtalkStreamCheckpointInterval
	dingtalkStreamCheckpointInterval = 1
	defer func() { dingtalkStreamCheckpointInterval = prev }()

	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc 1", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "Doc 2", Type: "doc", EditTime: 200},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h); err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	if len(h.checkpoints) == 0 {
		t.Fatalf("expected at least one checkpoint")
	}
	first := h.checkpoints[0]
	if _, ok := first.SpaceNodeTimes["space1"]["n1"]; !ok {
		t.Errorf("first checkpoint missing n1 progress: %+v", first.SpaceNodeTimes)
	}
}

func TestFetchStream_CheckpointsOnElapsedTime(t *testing.T) {
	// Checkpoints must ALSO fire on elapsed time, not only every N nodes.
	prevN := dingtalkStreamCheckpointInterval
	prevT := dingtalkStreamCheckpointMaxInterval
	dingtalkStreamCheckpointInterval = 1 << 30 // never fires by count
	dingtalkStreamCheckpointMaxInterval = 0     // fires by elapsed time every node
	defer func() {
		dingtalkStreamCheckpointInterval = prevN
		dingtalkStreamCheckpointMaxInterval = prevT
	}()

	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc 1", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "Doc 2", Type: "doc", EditTime: 200},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	h := &recordingHandler{}
	if _, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h); err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}
	if len(h.checkpoints) == 0 {
		t.Fatalf("expected time-based checkpoints even though node interval never fires")
	}
}

func TestFetchStream_EmitErrorAborts(t *testing.T) {
	// An Emit error aborts the stream immediately — the connector must return
	// the error and stop fetching further nodes.
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc 1", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "Doc 2", Type: "doc", EditTime: 200},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	boom := errors.New("ingest failed")
	c := NewConnector()
	h := &recordingHandler{emitErr: func(item types.FetchedItem) error { return boom }}
	_, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h)
	if !errors.Is(err, boom) {
		t.Fatalf("FetchStream() error = %v, want %v", err, boom)
	}
	if len(h.emitted) != 0 {
		t.Errorf("emitted %d items, want 0 (aborted on first emit)", len(h.emitted))
	}
}

func TestFetchStream_DetectsDeletedNodes(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "A", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "B", Type: "doc", EditTime: 200},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()

	// First sync: 2 docs.
	cursor, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, &recordingHandler{})
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Remove n2 (simulate deletion).
	nodes = nodes[:1]
	srv.Config.Handler = newFakeHandlerWithNodes(nodes)

	// Second sync: should emit n2 as deleted.
	h2 := &recordingHandler{}
	_, err = c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), cursor, h2)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}

	var deletedCount int
	for _, item := range h2.emitted {
		if item.IsDeleted {
			deletedCount++
			if item.ExternalID != "n2" {
				t.Errorf("deleted ExternalID = %q, want %q", item.ExternalID, "n2")
			}
		}
	}
	if deletedCount != 1 {
		t.Errorf("expected 1 deleted item, got %d", deletedCount)
	}
}

func TestFetchStream_ResumesFromCheckpoint(t *testing.T) {
	// A sync that resumes from a checkpoint cursor should skip nodes already
	// recorded at their current edit time and only fetch new/changed nodes.
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "A", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "B", Type: "doc", EditTime: 200},
		{NodeID: "n3", SpaceID: "space1", Name: "C", Type: "doc", EditTime: 300},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()

	// Simulate a checkpoint that already recorded n1 and n2 at their current
	// edit times (as if they were processed before a timeout).
	checkpoint := makeStreamCursor(t, map[string]map[string]string{
		"space1": {"n1": "100", "n2": "200"},
	})

	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), checkpoint, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// Only n3 should be emitted (n1 and n2 are unchanged).
	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (only n3), got %d", len(h.emitted))
	}
	if h.emitted[0].ExternalID != "n3" {
		t.Errorf("emitted ExternalID = %q, want %q", h.emitted[0].ExternalID, "n3")
	}
}

func TestFetchStream_SkipsUnsupportedTypes(t *testing.T) {
	nodes := []docNode{
		{NodeID: "n1", SpaceID: "space1", Name: "Doc", Type: "doc", EditTime: 100},
		{NodeID: "n2", SpaceID: "space1", Name: "Sheet", Type: "sheet", EditTime: 200},
		{NodeID: "n3", SpaceID: "space1", Name: "Folder", Type: "folder", EditTime: 300},
	}
	srv, cfg := fakeDingTalk(nodes)
	defer srv.Close()

	c := NewConnector()
	h := &recordingHandler{}
	_, err := c.FetchStream(context.Background(), makeStreamConfig(cfg, []string{"space1"}), nil, h)
	if err != nil {
		t.Fatalf("FetchStream() error: %v", err)
	}

	// Only "doc" type should be emitted.
	if len(h.emitted) != 1 {
		t.Fatalf("expected 1 emitted item (only doc), got %d", len(h.emitted))
	}
	if h.emitted[0].ExternalID != "n1" {
		t.Errorf("emitted ExternalID = %q, want %q", h.emitted[0].ExternalID, "n1")
	}
}

// ──────────────────────────────────────────────────────────────────────
// Fake server with failing node detail (for failed-fetch tests)
// ──────────────────────────────────────────────────────────────────────

// fakeDingTalkFailingDetail creates a server where the node detail endpoint
// always returns 500, simulating a transient content fetch failure.
func fakeDingTalkFailingDetail(nodes []docNode) (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{AccessToken: "fake-token", ExpireIn: 7200})
	})

	mux.HandleFunc("/v1.0/doc/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, spaceListResponse{
			Result: struct {
				Spaces    []docSpace `json:"spaces"`
				NextToken string     `json:"nextToken"`
			}{
				Spaces: []docSpace{{SpaceID: "space1", Name: "Test Space"}},
			},
		})
	})

	mux.HandleFunc("/v1.0/doc/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nodeListResponse{
			Result: struct {
				Nodes     []docNode `json:"nodes"`
				NextToken string    `json:"nextToken"`
			}{
				Nodes: nodes,
			},
		})
	})

	// Node detail always fails with 500.
	mux.HandleFunc("/v1.0/doc/nodes/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"code":500,"msg":"internal error"}`))
	})

	srv := httptest.NewServer(mux)
	cfg := &Config{
		AppKey:    "test-key",
		AppSecret: "test-secret",
		BaseURL:   srv.URL,
	}
	return srv, cfg
}

// Verify recordingHandler satisfies StreamHandler.
var _ datasource.StreamHandler = (*recordingHandler)(nil)
