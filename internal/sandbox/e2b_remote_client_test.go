package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	e2b "github.com/matiasinsaurralde/go-e2b"
	"github.com/stretchr/testify/require"
)

type e2bTestNetError struct {
	timeout bool
}

func (e e2bTestNetError) Error() string   { return "network failure" }
func (e e2bTestNetError) Timeout() bool   { return e.timeout }
func (e e2bTestNetError) Temporary() bool { return false }

var _ net.Error = e2bTestNetError{}

// e2bMockServer emulates the E2B control-plane endpoints used by
// E2BRemoteClient. Per-sandbox envd traffic is outside this mock.
type e2bMockServer struct {
	server *httptest.Server

	createCount  atomic.Int32
	listCount    atomic.Int32
	v2ListCount  atomic.Int32
	connectCount atomic.Int32
	infoCount    atomic.Int32
	deleteCount  atomic.Int32

	nextID      atomic.Int64
	createBody  map[string]any
	connectBody map[string]any
	connectID   string
	deleteID    string

	mu                      sync.Mutex
	v2Queries               []url.Values
	snapshotQueries         []url.Values
	repeatV2NextToken       bool
	unsafeV2NextToken       string
	repeatSnapshotNextToken bool
	snapshotPageSize        int
	snapshotDeleteFailWith  int
	snapshotCreateBody      map[string]any
	sandboxes               map[string]map[string]any // sandboxID -> SandboxInfo JSON
	snapshots               map[string]map[string]any // snapshotID -> SnapshotInfo JSON
}

func newE2BMockServer(t *testing.T) *e2bMockServer {
	t.Helper()
	m := &e2bMockServer{
		sandboxes: map[string]map[string]any{},
		snapshots: map[string]map[string]any{},
	}
	m.server = httptest.NewServer(http.HandlerFunc(m.handle))
	t.Cleanup(m.server.Close)
	return m
}

func (m *e2bMockServer) URL() string { return m.server.URL }

func (m *e2bMockServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/sandboxes" && r.Method == http.MethodPost:
		m.createCount.Add(1)
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &m.createBody)
		id := "e2b-" + strconv.FormatInt(m.nextID.Add(1), 10)
		m.sandboxes[id] = map[string]any{
			"sandboxID":   id,
			"templateID":  m.createBody["templateID"],
			"state":       "running",
			"envdVersion": "test",
			"startedAt":   time.Now().UTC().Format(time.RFC3339),
			"metadata":    m.createBody["metadata"],
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sandboxID":       id,
			"envdAccessToken": "token-" + id,
		})

	case r.URL.Path == "/sandboxes" && r.Method == http.MethodGet:
		m.listCount.Add(1)
		items := make([]map[string]any, 0, len(m.sandboxes))
		for _, info := range m.sandboxes {
			if info["state"] == "running" {
				items = append(items, info)
			}
		}
		writeJSON(w, http.StatusOK, items)

	case r.URL.Path == "/v2/sandboxes" && r.Method == http.MethodGet:
		m.handleListV2(w, r)

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") &&
		strings.HasSuffix(r.URL.Path, "/snapshots") &&
		r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sandboxes/"), "/snapshots")
		if _, ok := m.sandboxes[id]; !ok {
			http.NotFound(w, r)
			return
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &m.snapshotCreateBody)
		snapshotID := "snap-" + strconv.Itoa(len(m.snapshots)+1)
		name, _ := m.snapshotCreateBody["name"].(string)
		names := []string{}
		if name != "" {
			names = append(names, name)
		}
		m.snapshots[snapshotID] = map[string]any{
			"snapshotID": snapshotID,
			"sandboxID":  id,
			"names":      names,
		}
		writeJSON(w, http.StatusCreated, m.snapshots[snapshotID])

	case r.URL.Path == "/snapshots" && r.Method == http.MethodGet:
		m.handleListSnapshots(w, r)

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") &&
		strings.HasSuffix(r.URL.Path, "/connect") &&
		r.Method == http.MethodPost:
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/sandboxes/"), "/connect")
		info, ok := m.sandboxes[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		m.connectCount.Add(1)
		m.connectID = id
		_ = json.NewDecoder(r.Body).Decode(&m.connectBody)
		info["state"] = "running"
		writeJSON(w, http.StatusOK, map[string]any{
			"sandboxID":       id,
			"envdAccessToken": "reconnected-token-" + id,
		})

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") &&
		!strings.HasSuffix(r.URL.Path, "/connect") &&
		r.Method == http.MethodGet:
		// Per-sandbox info endpoint (GET /sandboxes/{id}). Read-only: it
		// reports the stored state without mutating it.
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		info, ok := m.sandboxes[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		m.infoCount.Add(1)
		writeJSON(w, http.StatusOK, info)

	case strings.HasPrefix(r.URL.Path, "/sandboxes/") && r.Method == http.MethodDelete:
		id := strings.TrimPrefix(r.URL.Path, "/sandboxes/")
		if _, ok := m.sandboxes[id]; !ok {
			http.NotFound(w, r)
			return
		}
		m.deleteCount.Add(1)
		m.deleteID = id
		delete(m.sandboxes, id)
		w.WriteHeader(http.StatusNoContent)

	case strings.HasPrefix(r.URL.Path, "/templates/") && r.Method == http.MethodDelete:
		if m.snapshotDeleteFailWith != 0 {
			http.Error(w, "delete failed", m.snapshotDeleteFailWith)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/templates/")
		if _, ok := m.snapshots[id]; !ok {
			http.NotFound(w, r)
			return
		}
		delete(m.snapshots, id)
		w.WriteHeader(http.StatusNoContent)

	default:
		http.NotFound(w, r)
	}
}

func (m *e2bMockServer) handleListV2(w http.ResponseWriter, r *http.Request) {
	m.v2ListCount.Add(1)
	m.mu.Lock()
	m.v2Queries = append(m.v2Queries, r.URL.Query())
	repeatToken := m.repeatV2NextToken
	unsafeToken := m.unsafeV2NextToken
	m.mu.Unlock()
	if len(r.URL.Query()["metadata"]) > 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"code":    http.StatusBadRequest,
			"message": "metadata accepts a single value",
		})
		return
	}

	states := make(map[string]bool)
	for _, state := range r.URL.Query()["state"] {
		states[state] = true
	}
	metadata := make(map[string]string)
	for _, item := range r.URL.Query()["metadata"] {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			metadata[key] = value
		}
	}

	ids := make([]string, 0, len(m.sandboxes))
	for id, info := range m.sandboxes {
		state, _ := info["state"].(string)
		if len(states) > 0 && !states[state] {
			continue
		}
		if len(states) == 0 && state != "running" && state != "paused" {
			continue
		}
		if !mockMetadataMatches(info["metadata"], metadata) {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	start := 0
	if raw := r.URL.Query().Get("nextToken"); raw != "" && raw != "repeat" {
		start, _ = strconv.Atoi(raw)
	}
	if start > len(ids) {
		start = len(ids)
	}
	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	items := make([]map[string]any, 0, end-start)
	for _, id := range ids[start:end] {
		items = append(items, m.sandboxes[id])
	}
	if unsafeToken != "" {
		w.Header().Set("X-Next-Token", unsafeToken)
	} else if repeatToken {
		w.Header().Set("X-Next-Token", "repeat")
	} else if end < len(ids) {
		w.Header().Set("X-Next-Token", strconv.Itoa(end))
	}
	writeJSON(w, http.StatusOK, items)
}

func (m *e2bMockServer) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.snapshotQueries = append(m.snapshotQueries, r.URL.Query())
	repeatToken := m.repeatSnapshotNextToken
	pageSize := m.snapshotPageSize
	m.mu.Unlock()

	sandboxID := r.URL.Query().Get("sandboxID")
	ids := make([]string, 0, len(m.snapshots))
	for id, info := range m.snapshots {
		if sandboxID != "" && info["sandboxID"] != sandboxID {
			continue
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	limit := 100
	if pageSize > 0 {
		limit = pageSize
	}
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && pageSize == 0 {
			limit = parsed
		}
	}
	start := 0
	if raw := r.URL.Query().Get("nextToken"); raw != "" && raw != "repeat" {
		start, _ = strconv.Atoi(raw)
	}
	if start > len(ids) {
		start = len(ids)
	}
	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	items := make([]map[string]any, 0, end-start)
	for _, id := range ids[start:end] {
		items = append(items, m.snapshots[id])
	}
	if repeatToken {
		w.Header().Set("X-Next-Token", "repeat")
	} else if end < len(ids) {
		w.Header().Set("X-Next-Token", strconv.Itoa(end))
	}
	writeJSON(w, http.StatusOK, items)
}

func mockMetadataMatches(raw any, expected map[string]string) bool {
	if len(expected) == 0 {
		return true
	}
	actual, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func newTestE2BRemoteClient(t *testing.T, mock *e2bMockServer) *E2BRemoteClient {
	t.Helper()
	cfg := &Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     mock.URL(),
		E2BTemplate:   "template-a",
		E2BSandboxTTL: 10 * time.Minute,
	}
	client, err := NewE2BRemoteClient(cfg)
	require.NoError(t, err)
	return client
}

func TestE2BRemoteClientProviderAndCapabilities(t *testing.T) {
	client := newTestE2BRemoteClient(t, newE2BMockServer(t))

	require.Equal(t, SandboxTypeE2B, client.Provider())
	require.Equal(t, RemoteSandboxCapabilities{
		SupportsReconnect:             true,
		SupportsMetadata:              true,
		SupportsListSandboxes:         true,
		SupportsPauseResume:           true,
		SupportsTimeoutRefresh:        true,
		SupportsFilesystemEnumeration: true,
		SupportsSnapshots:             true,
		SupportsVolumes:               true,
	}, client.Capabilities())
}

func TestE2BRemoteClientCreateSnapshot(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)

	ref, err := client.CreateSnapshot(ctx, handle.ID(), "weknora-sk-cfg1-g1")

	require.NoError(t, err)
	require.Equal(t, "snap-1", ref.ID)
	require.Equal(t, []string{"weknora-sk-cfg1-g1"}, ref.Names)
	require.Equal(t, "weknora-sk-cfg1-g1", mock.snapshotCreateBody["name"])
	require.Equal(t, int32(1), mock.connectCount.Load())
}

func TestE2BRemoteClientCreateSnapshotRejectsEmptySandboxID(t *testing.T) {
	client := newTestE2BRemoteClient(t, newE2BMockServer(t))

	_, err := client.CreateSnapshot(context.Background(), "  ", "n")

	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestE2BRemoteClientDeleteSnapshotTreatsMissingAsSuccess(t *testing.T) {
	client := newTestE2BRemoteClient(t, newE2BMockServer(t))

	err := client.DeleteSnapshot(context.Background(), "snap-missing")

	require.NoError(t, err, "a missing snapshot must not fail the delete path")
}

func TestE2BRemoteClientDeleteSnapshotRejectsEmptySnapshotID(t *testing.T) {
	client := newTestE2BRemoteClient(t, newE2BMockServer(t))

	err := client.DeleteSnapshot(context.Background(), "  ")

	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestE2BRemoteClientDeleteSnapshotReturnsUnexpectedErrors(t *testing.T) {
	mock := newE2BMockServer(t)
	mock.snapshotDeleteFailWith = http.StatusInternalServerError
	client := newTestE2BRemoteClient(t, mock)

	err := client.DeleteSnapshot(context.Background(), "snap-any")

	require.Error(t, err)
	require.False(t, IsRemoteNotFound(err))
}

func TestE2BRemoteClientListSnapshotsRejectsStuckPagination(t *testing.T) {
	mock := newE2BMockServer(t)
	mock.repeatSnapshotNextToken = true
	client := newTestE2BRemoteClient(t, mock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := client.ListSnapshots(ctx, "")

	require.Error(t, err)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestE2BRemoteClientListSnapshotsPagesAllResults(t *testing.T) {
	mock := newE2BMockServer(t)
	mock.snapshotPageSize = 1
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()
	first, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	second, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	firstRef, err := client.CreateSnapshot(ctx, first.ID(), "weknora-sk-cfg1-g1")
	require.NoError(t, err)
	secondRef, err := client.CreateSnapshot(ctx, first.ID(), "weknora-sk-cfg2-g1")
	require.NoError(t, err)
	_, err = client.CreateSnapshot(ctx, second.ID(), "other")
	require.NoError(t, err)

	list, err := client.ListSnapshots(ctx, first.ID())

	require.NoError(t, err)
	require.Equal(t, []RemoteSnapshotRef{firstRef, secondRef}, list)
	require.Len(t, mock.snapshotQueries, 2)
	require.Equal(t, first.ID(), mock.snapshotQueries[0].Get("sandboxID"))
	require.Equal(t, "100", mock.snapshotQueries[0].Get("limit"))
	require.Equal(t, "1", mock.snapshotQueries[1].Get("nextToken"))
}

func TestE2BRemoteClientListTemplatesReconcilesStandardTemplateBuildStatus(t *testing.T) {
	var statusRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-ready",
				"buildID":     "build-success",
				"names":       []string{"project/weknora"},
				"aliases":     []string{"weknora"},
				"buildStatus": "waiting",
				"envdVersion": "0.4.0",
			}})
		case "/templates/template-ready/builds/build-success/status":
			statusRequests.Add(1)
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-ready",
				"buildID":    "build-success",
				"status":     "ready",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BTemplate:   "template-ready",
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.True(t, templates[0].Standard)
	require.Equal(t, "project/weknora", templates[0].Name)
	require.Equal(t, "ready", templates[0].Status)
	require.Equal(t, int32(1), statusRequests.Load())
}

// An older successful build does not make the current one spawnable: E2B boots
// the current build only, and reporting "ready" here let the UI hand out a
// template that every sandbox creation rejected with a 404.
func TestE2BRemoteClientListTemplatesIgnoresOlderSuccessfulBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-queued",
				"buildID":     "build-queued",
				"names":       []string{"project/weknora"},
				"buildStatus": "waiting",
			}})
		case "/templates/template-queued/builds/build-queued/status":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-queued",
				"buildID":    "build-queued",
				"status":     "waiting",
			})
		case "/templates/template-queued":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-queued",
				"names":      []string{"project/weknora"},
				"builds": []map[string]any{
					{"buildID": "build-queued", "status": "waiting"},
					{"buildID": "build-older", "status": "success"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "waiting", templates[0].Status)
}

// E2B reports the all-zero UUID for a template that never got a build attached.
// Asking the build endpoint about it only yields a 400, and the template cannot
// be spawned, so the pending list status stands.
func TestE2BRemoteClientListTemplatesKeepsPendingWithoutBuildReference(t *testing.T) {
	var buildRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-unbuilt",
				"buildID":     "00000000-0000-0000-0000-000000000000",
				"names":       []string{"project/weknora"},
				"buildStatus": "building",
				"spawnCount":  0,
			}})
		case strings.Contains(r.URL.Path, "/builds/"):
			buildRequests.Add(1)
			http.Error(w, `{"code":400,"message":"Build not found"}`, http.StatusBadRequest)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "building", templates[0].Status)
	require.Equal(t, int32(0), buildRequests.Load())
}

func TestE2BRemoteClientListTemplatesKeepsPendingWithoutSuccessfulBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-fresh",
				"buildID":     "build-fresh",
				"names":       []string{"project/weknora"},
				"buildStatus": "building",
			}})
		case "/templates/template-fresh/builds/build-fresh/status":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-fresh",
				"buildID":    "build-fresh",
				"status":     "building",
			})
		case "/templates/template-fresh":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-fresh",
				"builds": []map[string]any{
					{"buildID": "build-fresh", "status": "building"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "building", templates[0].Status)
}

// Builds that finished but carry no default tag look exactly like "still
// building" in the template list, while sandbox creation rejects the template
// outright. The catalog has to name that state so the UI stops waiting.
func TestE2BRemoteClientListTemplatesReportsUntaggedBuilds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-untagged",
				"buildID":     "00000000-0000-0000-0000-000000000000",
				"names":       []string{"project/weknora"},
				"buildStatus": "waiting",
				"buildCount":  2,
			}})
		case "/templates/template-untagged":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-untagged",
				"builds": []map[string]any{
					{"buildID": "build-1", "status": "success"},
					{"buildID": "build-2", "status": "success"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, TemplateStatusUntagged, templates[0].Status)
}

// A template whose first build is genuinely still running has no finished build
// to find, so it must keep reporting the pending status.
func TestE2BRemoteClientListTemplatesKeepsPendingOnFirstBuild(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-first",
				"buildID":     "00000000-0000-0000-0000-000000000000",
				"names":       []string{"project/weknora"},
				"buildStatus": "building",
				"buildCount":  1,
			}})
		case "/templates/template-first":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-first",
				"builds":     []map[string]any{{"buildID": "build-1", "status": "building"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "building", templates[0].Status)
}

// Past spawns say nothing about the build that is current now, so they must not
// upgrade a pending status either.
func TestE2BRemoteClientListTemplatesIgnoresSpawnCount(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID":  "template-spawned",
				"buildID":     "build-x",
				"names":       []string{"weknora"},
				"buildStatus": "waiting",
				"spawnCount":  4,
			}})
		case "/templates/template-spawned/builds/build-x/status":
			writeJSON(w, http.StatusOK, map[string]any{
				"templateID": "template-spawned",
				"buildID":    "build-x",
				"status":     "waiting",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	client, err := NewE2BRemoteClient(&Config{
		E2BAPIKey:     "key-test",
		E2BAPIURL:     server.URL,
		E2BSandboxTTL: time.Minute,
	})
	require.NoError(t, err)

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "waiting", templates[0].Status)
}

func TestNormalizeE2BTemplateBuildStatus(t *testing.T) {
	tests := map[string]string{
		"READY":      "ready",
		"success":    "ready",
		"completed":  "ready",
		"processing": "building",
		"uploaded":   "waiting",
		"failed":     "error",
		"custom":     "custom",
	}
	for raw, want := range tests {
		require.Equalf(t, want, normalizeE2BTemplateBuildStatus(raw), "status %q", raw)
	}
}

func TestE2BRemoteClientCreateWritesMetadataAndPauseLifecycle(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	metadata := map[string]string{"owner": "session-1"}

	handle, err := client.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:       RemoteTimeoutExplicit,
			Value:      15 * time.Minute,
			Action:     RemoteOnTimeoutPause,
			AutoResume: true,
		},
		Metadata: metadata,
		EnvVars:  map[string]string{"LANG": "C.UTF-8"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, handle.ID())
	require.Equal(t, SandboxTypeE2B, handle.Provider())
	require.Equal(t, metadata, handle.Metadata())
	metadata["owner"] = "mutated-request"
	handle.Metadata()["owner"] = "mutated-return"
	require.Equal(t, "session-1", handle.Metadata()["owner"])
	require.Equal(t, "template-a", mock.createBody["templateID"])
	require.Equal(t, float64(900), mock.createBody["timeout"])
	require.Equal(t, map[string]any{"LANG": "C.UTF-8"}, mock.createBody["envVars"])
	require.Equal(t, map[string]any{"owner": "session-1"}, mock.createBody["metadata"])
	require.Equal(t, true, mock.createBody["autoPause"])
	require.Equal(t, true, mock.createBody["autoPauseMemory"])
	require.Equal(t, map[string]any{"enabled": true}, mock.createBody["autoResume"])
	// Network defaults match the Cube adapter: public traffic on, internet
	// egress on, and Secure=true so the response carries an envd access
	// token. Regressing these keeps `pip install` broken silently, so we
	// pin the wire payload here.
	require.Equal(t, true, mock.createBody["secure"])
	require.Equal(t, true, mock.createBody["allow_internet_access"])
	networkPayload, ok := mock.createBody["network"].(map[string]any)
	require.True(t, ok, "network payload missing: %#v", mock.createBody["network"])
	require.Equal(t, true, networkPayload["allowPublicTraffic"])

}

func TestE2BRemoteClientCreateValidatesTimeoutAction(t *testing.T) {
	tests := []struct {
		name       string
		action     RemoteTimeoutAction
		autoResume bool
		wantError  bool
	}{
		{name: "default is kill"},
		{name: "explicit kill", action: RemoteOnTimeoutKill},
		{name: "invalid action", action: RemoteTimeoutAction("hibernate"), wantError: true},
		{name: "kill with auto resume", action: RemoteOnTimeoutKill, autoResume: true, wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newE2BMockServer(t)
			client := newTestE2BRemoteClient(t, mock)
			_, err := client.Create(context.Background(), RemoteCreateRequest{
				TemplateID: "template-a",
				Timeout: RemoteTimeoutPolicy{
					Action:     tt.action,
					AutoResume: tt.autoResume,
				},
			})
			if tt.wantError {
				require.True(t, IsRemoteInvalidRequest(err))
				require.Zero(t, mock.createCount.Load())
				return
			}
			require.NoError(t, err)
			require.NotContains(t, mock.createBody, "autoPause")
			require.NotContains(t, mock.createBody, "autoResume")
		})
	}
}

func TestE2BRemoteClientCreateForwardsNetworkPolicy(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	deny := false
	privateSandbox := false

	_, err := client.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "template-a",
		Network: RemoteNetworkPolicy{
			AllowInternetAccess: &deny,
			AllowPublicTraffic:  &privateSandbox,
			AllowOut:            []string{"*.example.com"},
			DenyOut:             []string{"0.0.0.0/0"},
		},
	})
	require.NoError(t, err)

	// Top-level allow_internet_access flips off as the caller requested,
	// AllowPublicTraffic hides the sandbox behind a traffic access token,
	// and the L3/L4 allow/deny lists both reach the server. Regressing any
	// of these silently opens (or closes) the sandbox's network policy in
	// a way callers cannot observe from unit tests, so pin the wire.
	require.Equal(t, false, mock.createBody["allow_internet_access"])
	networkPayload, ok := mock.createBody["network"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, networkPayload["allowPublicTraffic"])
	require.Equal(t, []any{"*.example.com"}, networkPayload["allowOut"])
	require.Equal(t, []any{"0.0.0.0/0"}, networkPayload["denyOut"])
}

func TestE2BRemoteClientCreatePreservesTimeoutModes(t *testing.T) {
	t.Run("server default falls back to config TTL", func(t *testing.T) {
		mock := newE2BMockServer(t)
		client := newTestE2BRemoteClient(t, mock)
		_, err := client.Create(context.Background(), RemoteCreateRequest{
			TemplateID: "template-a",
			Timeout: RemoteTimeoutPolicy{
				Mode:   RemoteTimeoutServerDefault,
				Action: RemoteOnTimeoutKill,
			},
		})
		require.NoError(t, err)
		require.Equal(t, float64(600), mock.createBody["timeout"])
	})

	t.Run("never rejected", func(t *testing.T) {
		client := newTestE2BRemoteClient(t, newE2BMockServer(t))
		_, err := client.Create(context.Background(), RemoteCreateRequest{
			TemplateID: "template-a",
			Timeout: RemoteTimeoutPolicy{
				Mode:   RemoteTimeoutExplicit,
				Value:  -time.Hour,
				Action: RemoteOnTimeoutKill,
			},
		})
		require.True(t, IsRemoteInvalidRequest(err))
	})

	t.Run("missing template rejected before wire", func(t *testing.T) {
		cfg := &Config{E2BAPIKey: "key-test", E2BAPIURL: newE2BMockServer(t).URL()}
		client, err := NewE2BRemoteClient(cfg)
		require.NoError(t, err)
		_, err = client.Create(context.Background(), RemoteCreateRequest{})
		require.True(t, IsRemoteInvalidRequest(err))
	})
}

func TestE2BTimeoutSeconds(t *testing.T) {
	tests := []struct {
		name     string
		policy   RemoteTimeoutPolicy
		fallback time.Duration
		want     int
		wantErr  bool
	}{
		{
			name:     "zero fallback uses SDK default",
			policy:   RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
			fallback: 0,
		},
		{
			name:     "sub-second fallback rejected",
			policy:   RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
			fallback: 999 * time.Millisecond,
			wantErr:  true,
		},
		{
			name:     "fallback rounds up",
			policy:   RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
			fallback: 1500 * time.Millisecond,
			want:     2,
		},
		{
			name:   "zero explicit uses SDK default",
			policy: RemoteTimeoutPolicy{Mode: RemoteTimeoutExplicit},
		},
		{
			name:    "sub-second explicit rejected",
			policy:  RemoteTimeoutPolicy{Mode: RemoteTimeoutExplicit, Value: time.Millisecond},
			wantErr: true,
		},
		{
			name:   "explicit rounds up",
			policy: RemoteTimeoutPolicy{Mode: RemoteTimeoutExplicit, Value: 1500 * time.Millisecond},
			want:   2,
		},
		{
			name:    "never rejected",
			policy:  RemoteTimeoutPolicy{Mode: RemoteTimeoutExplicit, Value: -time.Second},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e2bTimeoutSeconds(tt.policy, tt.fallback)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestE2BRemoteClientCreateRejectsSubSecondTimeout(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)

	_, err := client.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "template-a",
		Timeout: RemoteTimeoutPolicy{
			Mode:  RemoteTimeoutExplicit,
			Value: time.Millisecond,
		},
	})

	require.True(t, IsRemoteInvalidRequest(err))
	require.Zero(t, mock.createCount.Load())
}

func TestE2BRemoteClientConnectAcrossClients(t *testing.T) {
	mock := newE2BMockServer(t)
	first := newTestE2BRemoteClient(t, mock)
	handle, err := first.Create(context.Background(), RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "session-1"},
	})
	require.NoError(t, err)
	mock.sandboxes[handle.ID()]["state"] = "paused"

	second := newTestE2BRemoteClient(t, mock)
	reconnected, err := second.Connect(context.Background(), handle.ID())
	require.NoError(t, err)
	require.Equal(t, handle.ID(), reconnected.ID())
	require.Nil(t, reconnected.Metadata())
	require.Equal(t, handle.ID(), mock.connectID)
	require.Equal(t, int32(1), mock.connectCount.Load())
	require.Equal(t, float64(600), mock.connectBody["timeout"])
	require.Equal(t, "running", mock.sandboxes[handle.ID()]["state"])
}

func TestE2BRemoteClientListFiltersByMetadataAndState(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()

	_, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "wanted"},
	})
	require.NoError(t, err)
	// A matching paused sandbox proves the state filter is applied after
	// metadata conversion.
	paused, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "wanted"},
	})
	require.NoError(t, err)
	mock.sandboxes[paused.ID()]["state"] = "paused"
	// A running sandbox with different metadata proves both filters are
	// required for a match.
	_, err = client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "other"},
	})
	require.NoError(t, err)

	running, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{"owner": "wanted"},
		States:   []RemoteSandboxState{RemoteStateRunning},
	})
	require.NoError(t, err)
	require.Len(t, running, 1)
	require.Equal(t, RemoteStateRunning, running[0].State)
	require.Equal(t, map[string]string{"owner": "wanted"}, running[0].Metadata)
	require.Equal(t, int32(1), mock.v2ListCount.Load())
	require.Zero(t, mock.listCount.Load(), "List must use the V2 endpoint")
	require.Equal(t, []string{"running"}, mock.v2Queries[0]["state"])
	require.Equal(t, []string{"owner=wanted"}, mock.v2Queries[0]["metadata"])
	require.Equal(t, "100", mock.v2Queries[0].Get("limit"))

	pausedOnly, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{"owner": "wanted"},
		States:   []RemoteSandboxState{RemoteStatePaused},
	})
	require.NoError(t, err)
	require.Len(t, pausedOnly, 1)
	require.Equal(t, RemoteStatePaused, pausedOnly[0].State)
	require.Equal(t, []string{"paused"}, mock.v2Queries[1]["state"])

	all, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{"owner": "wanted"},
	})
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, RemoteStateRunning, all[0].State)
	require.Equal(t, RemoteStatePaused, all[1].State)
	require.Empty(t, mock.v2Queries[2]["state"], "empty state filter must request V2 defaults")
}

func TestE2BRemoteClientListUsesOneServerMetadataFilterAndMatchesAllLocally(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()

	_, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "wanted", "scope": "alpha"},
	})
	require.NoError(t, err)
	_, err = client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{"owner": "wanted", "scope": "beta"},
	})
	require.NoError(t, err)

	summaries, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{"owner": "wanted", "scope": "alpha"},
	})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Equal(t, map[string]string{"owner": "wanted", "scope": "alpha"}, summaries[0].Metadata)
	require.Len(t, mock.v2Queries[0]["metadata"], 1)
}

func TestE2BRemoteClientListDoesNotClaimSameSessionAcrossTenants(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()
	targetMetadata := map[string]string{
		remoteMetadataTenantID:       "tenant-a",
		remoteMetadataSessionID:      "shared-session",
		remoteMetadataProvider:       string(SandboxTypeE2B),
		remoteMetadataBindingVersion: "1",
	}
	otherTenantMetadata := cloneMetadata(targetMetadata)
	otherTenantMetadata[remoteMetadataTenantID] = "tenant-b"

	target, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   targetMetadata,
	})
	require.NoError(t, err)
	_, err = client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   otherTenantMetadata,
	})
	require.NoError(t, err)

	summaries, err := client.List(ctx, RemoteListFilter{Metadata: targetMetadata})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Equal(t, target.ID(), summaries[0].ID)
	require.Equal(t, "tenant-a", summaries[0].Metadata[remoteMetadataTenantID])
	require.Equal(
		t,
		[]string{remoteMetadataSessionID + "=shared-session"},
		mock.v2Queries[0]["metadata"],
		"server query must narrow by session only; complete ownership filtering is local",
	)
}

func TestE2BRemoteClientListSkipsUnsafeServerMetadataAndMatchesLocally(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()
	unsafeKey := "owner&+=%"
	unsafeValue := "value&+=%"

	_, err := client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{unsafeKey: unsafeValue},
	})
	require.NoError(t, err)
	_, err = client.Create(ctx, RemoteCreateRequest{
		TemplateID: "template-a",
		Metadata:   map[string]string{unsafeKey: "other"},
	})
	require.NoError(t, err)

	summaries, err := client.List(ctx, RemoteListFilter{
		Metadata: map[string]string{unsafeKey: unsafeValue},
	})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Equal(t, map[string]string{unsafeKey: unsafeValue}, summaries[0].Metadata)
	require.Empty(t, mock.v2Queries[0]["metadata"])
}

func TestE2BRemoteClientListSkipsWireForUnmappableStates(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)

	summaries, err := client.List(context.Background(), RemoteListFilter{
		States: []RemoteSandboxState{RemoteStateTerminal, RemoteStateUnknown},
	})
	require.NoError(t, err)
	require.Empty(t, summaries)
	require.Zero(t, mock.v2ListCount.Load())
}

func TestE2BRemoteClientListMapsKnownStatesFromMixedFilter(t *testing.T) {
	mock := newE2BMockServer(t)
	mock.sandboxes["running"] = map[string]any{"sandboxID": "running", "state": "running"}
	mock.sandboxes["paused"] = map[string]any{"sandboxID": "paused", "state": "paused"}
	client := newTestE2BRemoteClient(t, mock)

	summaries, err := client.List(context.Background(), RemoteListFilter{
		States: []RemoteSandboxState{RemoteStateRunning, RemoteStateUnknown},
	})
	require.NoError(t, err)
	require.Len(t, summaries, 1)
	require.Equal(t, "running", summaries[0].ID)
	require.Equal(t, int32(1), mock.v2ListCount.Load())
	require.Equal(t, []string{"running"}, mock.v2Queries[0]["state"])
}

func TestE2BRemoteSummaryClonesMetadata(t *testing.T) {
	source := map[string]string{"owner": "session-1"}
	summary := e2bRemoteSummary(e2b.SandboxInfo{ID: "e2b-1", Metadata: source})
	source["owner"] = "mutated-source"
	require.Equal(t, "session-1", summary.Metadata["owner"])
	summary.Metadata["owner"] = "mutated-summary"
	require.Equal(t, "mutated-source", source["owner"])
}

func TestE2BRemoteClientGetUsesConnectAndInfo(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()
	handle, err := client.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)

	summary, err := client.Get(ctx, handle.ID())
	require.NoError(t, err)
	require.Equal(t, handle.ID(), summary.ID)
	require.Equal(t, RemoteStateRunning, summary.State)
	// TemplateID only comes from the info endpoint; Connect alone cannot
	// supply it, so its presence proves Get fetched full info by ID.
	require.Equal(t, "template-a", summary.TemplateID)
	require.Equal(t, int32(1), mock.connectCount.Load(), "Get reattaches via Connect")
	require.Equal(t, int32(1), mock.infoCount.Load(), "Get reads info by ID")
	require.Zero(t, mock.listCount.Load(), "Get must not scan the sandbox list")
	require.Zero(t, mock.v2ListCount.Load(), "Get must not scan the V2 sandbox list")

	// Unknown ID is a NotFound: Connect 404s and is normalized to NotFound.
	_, err = client.Get(ctx, "no-such-sandbox")
	require.True(t, IsRemoteNotFound(err))
}

func TestE2BRemoteClientGetResumesPausedSandbox(t *testing.T) {
	mock := newE2BMockServer(t)
	client := newTestE2BRemoteClient(t, mock)
	handle, err := client.Create(context.Background(), RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	mock.sandboxes[handle.ID()]["state"] = "paused"

	summary, err := client.Get(context.Background(), handle.ID())
	require.NoError(t, err)
	require.Equal(t, handle.ID(), summary.ID)
	// Get reattaches via Connect, which wakes the paused sandbox, so the
	// subsequent info read reports it running. This is intended: Get's only
	// caller connects (and therefore wakes) the sandbox immediately after.
	require.Equal(t, RemoteStateRunning, summary.State)
	require.Equal(t, int32(1), mock.connectCount.Load())
	require.Zero(t, mock.v2ListCount.Load(), "Get must not scan the V2 sandbox list")
}

func TestE2BRemoteClientListTraversesAllV2Pages(t *testing.T) {
	mock := newE2BMockServer(t)
	for index := 0; index < 101; index++ {
		id := fmt.Sprintf("sandbox-%03d", index)
		mock.sandboxes[id] = map[string]any{
			"sandboxID":  id,
			"templateID": "template-a",
			"state":      "running",
			"startedAt":  time.Now().UTC().Format(time.RFC3339),
		}
	}
	client := newTestE2BRemoteClient(t, mock)

	summaries, err := client.List(context.Background(), RemoteListFilter{})
	require.NoError(t, err)
	require.Len(t, summaries, 101)
	require.Equal(t, int32(2), mock.v2ListCount.Load())

	// Get is a direct by-ID lookup (Connect+info); it never touches the list.
	mock.v2ListCount.Store(0)
	summary, err := client.Get(context.Background(), "sandbox-100")
	require.NoError(t, err)
	require.Equal(t, "sandbox-100", summary.ID)
	require.Zero(t, mock.v2ListCount.Load(), "Get must not scan the V2 sandbox list")
	require.Equal(t, int32(1), mock.connectCount.Load())
	require.Equal(t, int32(1), mock.infoCount.Load())
}

func TestE2BRemoteClientListRejectsRepeatedV2NextToken(t *testing.T) {
	mock := newE2BMockServer(t)
	mock.repeatV2NextToken = true
	mock.sandboxes["sandbox-1"] = map[string]any{
		"sandboxID":  "sandbox-1",
		"templateID": "template-a",
		"state":      "running",
		"startedAt":  time.Now().UTC().Format(time.RFC3339),
	}
	client := newTestE2BRemoteClient(t, mock)

	_, err := client.List(context.Background(), RemoteListFilter{})
	require.Error(t, err)
	var remoteErr *RemoteError
	require.ErrorAs(t, err, &remoteErr)
	require.Equal(t, RemoteErrorKindInternal, remoteErr.Kind)
	require.Equal(t, "List", remoteErr.Op)
	require.Equal(t, int32(2), mock.v2ListCount.Load())
}

func TestE2BRemoteClientListRejectsUnsafeV2NextTokenBeforeNextRequest(t *testing.T) {
	mock := newE2BMockServer(t)
	mock.unsafeV2NextToken = "next&token"
	mock.sandboxes["sandbox-1"] = map[string]any{
		"sandboxID": "sandbox-1",
		"state":     "running",
	}
	client := newTestE2BRemoteClient(t, mock)

	_, err := client.List(context.Background(), RemoteListFilter{})
	require.Error(t, err)
	var remoteErr *RemoteError
	require.ErrorAs(t, err, &remoteErr)
	require.Equal(t, RemoteErrorKindInternal, remoteErr.Kind)
	require.Equal(t, "List", remoteErr.Op)
	require.Contains(t, remoteErr.Error(), "unsafe")
	require.Equal(t, int32(1), mock.v2ListCount.Load())
}

func TestE2BRemoteClientDeleteAcrossClients(t *testing.T) {
	mock := newE2BMockServer(t)
	creator := newTestE2BRemoteClient(t, mock)
	ctx := context.Background()

	handle, err := creator.Create(ctx, RemoteCreateRequest{TemplateID: "template-a"})
	require.NoError(t, err)
	deleter := newTestE2BRemoteClient(t, mock)
	require.NoError(t, deleter.Delete(ctx, handle.ID()))
	require.Equal(t, handle.ID(), mock.connectID)
	require.Equal(t, int32(1), mock.connectCount.Load())
	require.Equal(t, handle.ID(), mock.deleteID)
	require.Equal(t, int32(1), mock.deleteCount.Load())

	err = deleter.Delete(ctx, handle.ID())
	require.True(t, IsRemoteNotFound(err))
	require.Equal(t, int32(1), mock.deleteCount.Load())
}

func TestE2BRemoteClientFilesystemOperationsRejectEmptyPaths(t *testing.T) {
	client := newTestE2BRemoteClient(t, newE2BMockServer(t))
	handle := &e2bRemoteHandle{sandbox: &e2b.Sandbox{ID: "e2b-1"}}

	_, err := client.ListDir(context.Background(), handle, " ")
	require.True(t, IsRemoteInvalidRequest(err))
	err = client.MakeDir(context.Background(), handle, " ")
	require.True(t, IsRemoteInvalidRequest(err))
	err = client.Remove(context.Background(), handle, " ")
	require.True(t, IsRemoteInvalidRequest(err))
	_, err = client.Stat(context.Background(), handle, " ")
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestE2BRemoteClientRejectsForeignHandle(t *testing.T) {
	client := newTestE2BRemoteClient(t, newE2BMockServer(t))
	_, err := client.ReadFile(
		context.Background(),
		&contractHandle{id: "cube-1", provider: SandboxTypeCube},
		"/workspace/file",
	)
	require.True(t, IsRemoteInvalidRequest(err))
}

func TestNormalizeE2BState(t *testing.T) {
	tests := map[string]RemoteSandboxState{
		"":            RemoteStateUnknown,
		"running":     RemoteStateRunning,
		"paused":      RemoteStatePaused,
		"pausing":     RemoteStateTransitioning,
		"resuming":    RemoteStateTransitioning,
		"pending":     RemoteStateTransitioning,
		"killed":      RemoteStateTerminal,
		"terminated":  RemoteStateTerminal,
		"deleted":     RemoteStateTerminal,
		"failed":      RemoteStateTerminal,
		"stopped":     RemoteStateTerminal,
		"error":       RemoteStateTerminal,
		"weird-state": RemoteStateUnknown,
	}
	for raw, want := range tests {
		require.Equalf(t, want, normalizeE2BState(raw), "state %q", raw)
	}
}

func TestNormalizeE2BError(t *testing.T) {
	tests := []struct {
		name string
		op   string
		err  error
		kind RemoteErrorKind
	}{
		{"sandbox not found", "Get", &e2b.SandboxNotFoundError{SandboxID: "e2b-1"}, RemoteErrorKindNotFound},
		{"file not found", "ReadFile", &e2b.FileNotFoundError{Path: "/x"}, RemoteErrorKindNotFound},
		{"template not found", "Create", &e2b.TemplateNotFoundError{TemplateID: "t"}, RemoteErrorKindInvalidRequest},
		{"unauthorized", "Health", &e2b.Error{StatusCode: http.StatusUnauthorized}, RemoteErrorKindAuthentication},
		{"rate limited", "Create", &e2b.Error{StatusCode: http.StatusTooManyRequests}, RemoteErrorKindCapacity},
		{"gone", "Get", &e2b.Error{StatusCode: http.StatusGone}, RemoteErrorKindTerminal},
		{"unavailable", "List", &e2b.Error{StatusCode: http.StatusBadGateway}, RemoteErrorKindUnavailable},
		{"deadline", "Exec", context.DeadlineExceeded, RemoteErrorKindTimeout},
		{"network timeout", "List", e2bTestNetError{timeout: true}, RemoteErrorKindTimeout},
		{"network unavailable", "List", e2bTestNetError{}, RemoteErrorKindUnavailable},
		{"connect canceled", "List", connect.NewError(connect.CodeCanceled, errors.New("canceled")), RemoteErrorKindTimeout},
		{"connect deadline", "List", connect.NewError(connect.CodeDeadlineExceeded, errors.New("deadline")), RemoteErrorKindTimeout},
		{"connect unavailable", "List", connect.NewError(connect.CodeUnavailable, errors.New("unavailable")), RemoteErrorKindUnavailable},
		{"connect unauthenticated", "List", connect.NewError(connect.CodeUnauthenticated, errors.New("unauthenticated")), RemoteErrorKindAuthentication},
		{"connect permission denied", "List", connect.NewError(connect.CodePermissionDenied, errors.New("permission denied")), RemoteErrorKindAuthentication},
		{"connect resource exhausted", "List", connect.NewError(connect.CodeResourceExhausted, errors.New("resource exhausted")), RemoteErrorKindCapacity},
		{"connect invalid argument", "List", connect.NewError(connect.CodeInvalidArgument, errors.New("invalid argument")), RemoteErrorKindInvalidRequest},
		{"connect out of range", "List", connect.NewError(connect.CodeOutOfRange, errors.New("out of range")), RemoteErrorKindInvalidRequest},
		{"connect not found", "List", connect.NewError(connect.CodeNotFound, errors.New("not found")), RemoteErrorKindNotFound},
		{"connect already exists", "List", connect.NewError(connect.CodeAlreadyExists, errors.New("already exists")), RemoteErrorKindConflict},
		{"connect aborted", "List", connect.NewError(connect.CodeAborted, errors.New("aborted")), RemoteErrorKindConflict},
		{"connect failed precondition", "List", connect.NewError(connect.CodeFailedPrecondition, errors.New("failed precondition")), RemoteErrorKindConflict},
		{"connect unimplemented", "List", connect.NewError(connect.CodeUnimplemented, errors.New("unimplemented")), RemoteErrorKindUnsupported},
		{"connect internal", "List", connect.NewError(connect.CodeInternal, errors.New("internal")), RemoteErrorKindInternal},
		{"connect data loss", "List", connect.NewError(connect.CodeDataLoss, errors.New("data loss")), RemoteErrorKindInternal},
		{"connect unknown", "List", connect.NewError(connect.CodeUnknown, errors.New("unknown")), RemoteErrorKindInternal},
		{"unknown", "List", errors.New("mystery"), RemoteErrorKindInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := normalizeE2BError(tt.op, tt.err)
			var remoteErr *RemoteError
			require.ErrorAs(t, err, &remoteErr)
			require.Equal(t, tt.kind, remoteErr.Kind)
			require.Equal(t, SandboxTypeE2B, remoteErr.Provider)
			require.ErrorIs(t, err, tt.err)
		})
	}

	t.Run("context canceled remains discoverable", func(t *testing.T) {
		err := normalizeE2BError("List", fmt.Errorf("wrapped: %w", context.Canceled))
		require.ErrorIs(t, err, context.Canceled)
	})
}
