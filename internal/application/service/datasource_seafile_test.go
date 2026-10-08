package service

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	seafileConnector "github.com/Tencent/WeKnora/internal/datasource/connector/seafile"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	seafileTestRepo      = "0f1e2d3c-4b5a-4968-8776-655443322110"
	seafileTestOtherRepo = "ffffffff-0000-4000-8000-000000000001"
)

// seafileFakeServer is the smallest Seafile 10.0.1 stand-in the streaming
// service path needs: one library, one directory level and a separate
// fileserver. Directories are implied by the file paths.
type seafileFakeServer struct {
	api, fileserver *httptest.Server

	mu        sync.Mutex
	files     map[string][]byte // absolute path → body
	forbidden map[string]bool   // paths whose download link answers 403
	requests  int
}

func newSeafileFakeServer(t *testing.T) *seafileFakeServer {
	t.Helper()
	f := &seafileFakeServer{files: map[string][]byte{}, forbidden: map[string]bool{}}
	f.fileserver = httptest.NewServer(http.HandlerFunc(f.handleDownload))
	t.Cleanup(f.fileserver.Close)
	f.api = httptest.NewServer(http.HandlerFunc(f.handleAPI))
	t.Cleanup(f.api.Close)
	return f
}

func (f *seafileFakeServer) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests
}

func (f *seafileFakeServer) handleAPI(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Query().Get("p")
	switch r.URL.Path {
	case "/api2/repos/":
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"id": seafileTestRepo, "name": "Library", "type": "repo", "encrypted": false, "permission": "rw",
		}})
	case "/api2/repos/" + seafileTestRepo + "/dir/":
		entries, ok := f.listDir(p)
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(entries)
	case "/api2/repos/" + seafileTestRepo + "/file/":
		body, ok := f.files[p]
		switch {
		case !ok:
			http.NotFound(w, r)
		case f.forbidden[p]:
			w.WriteHeader(http.StatusForbidden)
		default:
			w.Header().Set("oid", fmt.Sprintf("%x", sha1.Sum(body)))
			_ = json.NewEncoder(w).Encode(f.fileserver.URL + "/f/" + url.PathEscape(p))
		}
	default:
		http.NotFound(w, r)
	}
}

// listDir renders the direct children of dir; dirs carry no size key.
func (f *seafileFakeServer) listDir(dir string) ([]map[string]any, bool) {
	entries := make([]map[string]any, 0)
	children := map[string]bool{}
	found := dir == "/"
	for p, body := range f.files {
		if path.Dir(p) == dir {
			found = true
			entries = append(entries, map[string]any{
				"id": fmt.Sprintf("%x", sha1.Sum(body)), "name": path.Base(p), "type": "file",
				"mtime": 1, "size": len(body), "permission": "rw",
			})
			continue
		}
		if strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/") {
			found = true
			rest := strings.TrimPrefix(p, strings.TrimSuffix(dir, "/")+"/")
			children[strings.SplitN(rest, "/", 2)[0]] = true
		}
	}
	for name := range children {
		entries = append(entries, map[string]any{
			"id": "dir-" + name, "name": name, "type": "dir", "mtime": 1, "permission": "rw",
		})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i]["name"].(string) < entries[j]["name"].(string) })
	return entries, found
}

func (f *seafileFakeServer) handleDownload(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	p, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/f/"))
	body, ok := f.files[p]
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

// seafileKnowledgeRepo keys live knowledge by data source and external ID so
// deletion scoping is observable.
type seafileKnowledgeRepo struct {
	interfaces.KnowledgeRepository
	live        map[string]*types.Knowledge
	hardDeleted []string
}

func (r *seafileKnowledgeRepo) FindByDataSourceExternalID(
	_ context.Context, _ uint64, _, dataSourceID, externalID string,
) (*types.Knowledge, error) {
	return r.live[dataSourceID+"|"+externalID], nil
}

func (r *seafileKnowledgeRepo) HardDeleteKnowledge(_ context.Context, _ uint64, id string) error {
	r.hardDeleted = append(r.hardDeleted, id)
	return nil
}

type seafileCreatedFile struct {
	fileName string
	channel  string
	metadata map[string]string
}

// seafileKnowledgeService records what the ingest path hands to the
// knowledge base.
type seafileKnowledgeService struct {
	interfaces.KnowledgeService
	repo    *seafileKnowledgeRepo
	created []seafileCreatedFile
	deleted []string
}

func (k *seafileKnowledgeService) GetRepository() interfaces.KnowledgeRepository { return k.repo }

func (k *seafileKnowledgeService) DeleteKnowledge(_ context.Context, id string) error {
	k.deleted = append(k.deleted, id)
	return nil
}

func (k *seafileKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context, _ string, _ *multipart.FileHeader, metadata map[string]string,
	_ *bool, customFileName string, _ []string, channel string, _ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	k.created = append(k.created, seafileCreatedFile{fileName: customFileName, channel: channel, metadata: metadata})
	return &types.Knowledge{ID: "created-" + customFileName}, nil
}

// seafileSyncHarness wires the real DataSourceService streaming path to the
// real Seafile connector against the fake server.
type seafileSyncHarness struct {
	server      *seafileFakeServer
	ds          *types.DataSource
	syncLog     *types.SyncLog
	syncLogRepo *processSyncSyncLogRepo
	repo        *seafileKnowledgeRepo
	knowledge   *seafileKnowledgeService
	svc         *DataSourceService
}

func newSeafileSyncHarness(t *testing.T, syncDeletions bool, cursor *types.SyncCursor) *seafileSyncHarness {
	t.Helper()
	withSSRFWhitelist(t, "127.0.0.1,localhost")
	server := newSeafileFakeServer(t)
	configJSON, err := (&types.DataSourceConfig{
		Type:        types.ConnectorTypeSeafile,
		Credentials: map[string]any{"base_url": server.api.URL, "api_token": "secret-token"},
		ResourceIDs: []string{seafileTestRepo + ":/docs"},
	}).ToJSON()
	require.NoError(t, err)

	ds := &types.DataSource{
		ID: "ds-seafile", TenantID: 1, KnowledgeBaseID: "kb-1", Name: "Seafile",
		Type: types.ConnectorTypeSeafile, Config: configJSON,
		SyncMode: types.SyncModeIncremental, Status: types.DataSourceStatusActive,
		SyncDeletions: syncDeletions,
	}
	if cursor != nil {
		ds.LastSyncCursor, err = cursor.ToJSON()
		require.NoError(t, err)
	}
	syncLog := &types.SyncLog{
		ID: "log-seafile", DataSourceID: ds.ID, TenantID: ds.TenantID,
		Status: types.SyncLogStatusRunning, StartedAt: time.Now().UTC(),
	}
	repo := &seafileKnowledgeRepo{live: map[string]*types.Knowledge{}}
	knowledge := &seafileKnowledgeService{repo: repo}
	syncLogRepo := &processSyncSyncLogRepo{logs: map[string]*types.SyncLog{syncLog.ID: syncLog}}
	registry := datasource.NewConnectorRegistry()
	require.NoError(t, registry.Register(seafileConnector.NewConnector()))
	kb := &types.KnowledgeBase{ID: ds.KnowledgeBaseID, TenantID: ds.TenantID}

	return &seafileSyncHarness{
		server: server, ds: ds, syncLog: syncLog, syncLogRepo: syncLogRepo, repo: repo, knowledge: knowledge,
		svc: &DataSourceService{
			dsRepo:            newKBDeleteDSRepo(ds.KnowledgeBaseID, ds),
			syncLogRepo:       syncLogRepo,
			knowledgeService:  knowledge,
			kbService:         &processSyncKBService{kb: kb},
			connectorRegistry: registry,
			tenantRepo:        &processSyncTenantRepo{tenant: &types.Tenant{ID: ds.TenantID}},
			tagService:        &processSyncTagService{},
		},
	}
}

func (h *seafileSyncHarness) run(t *testing.T, forceFull bool) (*types.SyncLog, error) {
	t.Helper()
	payload, err := json.Marshal(types.DataSourceSyncPayload{
		DataSourceID: h.ds.ID, TenantID: h.ds.TenantID, SyncLogID: h.syncLog.ID, ForceFull: forceFull,
	})
	require.NoError(t, err)
	err = h.svc.ProcessSync(context.Background(), asynq.NewTask(types.TypeDataSourceSync, payload))
	return h.syncLogRepo.logs[h.syncLog.ID], err
}

// cursorFiles decodes the persisted connector cursor's file map.
func (h *seafileSyncHarness) cursorFiles(t *testing.T) map[string]string {
	t.Helper()
	cur, err := h.ds.ParseSyncCursor()
	require.NoError(t, err)
	require.NotNil(t, cur)
	raw, err := json.Marshal(cur.ConnectorCursor["files"])
	require.NoError(t, err)
	files := map[string]string{}
	require.NoError(t, json.Unmarshal(raw, &files))
	return files
}

func seafileCursor(repoID string, files map[string]string) *types.SyncCursor {
	return &types.SyncCursor{ConnectorCursor: map[string]any{
		"schema_version": 1, "repo_id": repoID, "repo_name": "Library", "files": files,
	}}
}

func seafileExternalID(p string) string { return "seafile:" + seafileTestRepo + ":" + p }

// A connector error item reaches the sync log as a coded SyncItemError with
// the HTTP status as parameter, and the run ends partial rather than failed.
func TestProcessSync_SeafileErrorItemBecomesSyncItemError(t *testing.T) {
	h := newSeafileSyncHarness(t, false, nil)
	h.server.files["/docs/ok.pdf"] = []byte("ok")
	h.server.files["/docs/bad.pdf"] = []byte("bad")
	h.server.forbidden["/docs/bad.pdf"] = true

	log, err := h.run(t, false)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusPartial, log.Status)
	assert.Equal(t, 1, log.ItemsCreated)
	assert.Equal(t, 1, log.ItemsFailed)
	result, err := log.ParseResult()
	require.NoError(t, err)
	require.Len(t, result.Errors, 1)
	assert.Equal(t, "seafile_permission_denied", result.Errors[0].Code)
	assert.Equal(t, map[string]string{"code": "403"}, result.Errors[0].Params)
	assert.Equal(t, "Library/docs/bad.pdf", result.Errors[0].Title)
	assert.NotContains(t, result.Errors[0].Message, "secret-token")

	files := h.cursorFiles(t)
	assert.NotEmpty(t, files["/docs/ok.pdf"])
	_, known := files["/docs/bad.pdf"]
	assert.False(t, known, "a path that never synced must not enter the cursor")
}

// Same bytes under two paths are two documents: each reaches the knowledge
// base with its own external ID under the Seafile channel, whose duplicate
// check is scoped by source identity.
func TestProcessSync_SeafileSameContentDifferentPathsStayIndependent(t *testing.T) {
	h := newSeafileSyncHarness(t, false, nil)
	h.server.files["/docs/a.pdf"] = []byte("same bytes")
	h.server.files["/docs/sub/b.pdf"] = []byte("same bytes")

	log, err := h.run(t, false)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log.Status)
	assert.Equal(t, 2, log.ItemsCreated)
	require.Len(t, h.knowledge.created, 2)
	assert.Equal(t, "Library/docs/a.pdf", h.knowledge.created[0].fileName)
	assert.Equal(t, "Library/docs/sub/b.pdf", h.knowledge.created[1].fileName)
	for _, created := range h.knowledge.created {
		assert.Equal(t, types.ChannelSeafile, created.channel)
		assert.Equal(t, h.ds.ID, created.metadata["datasource_id"])
		assert.Equal(t, seafileTestRepo+":/", created.metadata["source_resource_id"])
		assert.True(t, usesSourceIdentityDuplicateCheck(created.channel))
	}
	assert.NotEqual(t, h.knowledge.created[0].metadata["external_id"], h.knowledge.created[1].metadata["external_id"])
	assert.Len(t, h.cursorFiles(t), 2)
}

// Deletions follow the data source's SyncDeletions switch and only touch the
// knowledge owned by this data source, even when another data source
// synced the same Seafile path.
func TestProcessSync_SeafileDeletionsGatedAndScoped(t *testing.T) {
	for _, syncDeletions := range []bool{true, false} {
		t.Run(fmt.Sprintf("sync_deletions=%v", syncDeletions), func(t *testing.T) {
			gone := seafileExternalID("/docs/gone.pdf")
			h := newSeafileSyncHarness(t, syncDeletions, seafileCursor(seafileTestRepo, map[string]string{
				"/docs/gone.pdf": "o:old|m:1|s:4",
			}))
			// The selected directory must still exist; only the tracked file is gone.
			h.server.files["/docs/keep.pdf"] = []byte("keep")
			h.repo.live["ds-seafile|"+gone] = &types.Knowledge{ID: "knowledge-mine"}
			h.repo.live["ds-other|"+gone] = &types.Knowledge{ID: "knowledge-other"}

			log, err := h.run(t, false)
			require.NoError(t, err)
			assert.Equal(t, types.SyncLogStatusSuccess, log.Status)
			assert.Equal(t, 1, log.ItemsCreated)
			if syncDeletions {
				assert.Equal(t, 1, log.ItemsDeleted)
				assert.Equal(t, []string{"knowledge-mine"}, h.knowledge.deleted)
				assert.Equal(t, []string{"knowledge-mine"}, h.repo.hardDeleted)
			} else {
				assert.Equal(t, 0, log.ItemsDeleted)
				assert.Empty(t, h.knowledge.deleted)
				assert.Empty(t, h.repo.hardDeleted)
			}
			// The connector forgets the path either way; a later re-enable
			// cannot resurrect the deletion, matching the other connectors.
			assert.Equal(t, []string{"/docs/keep.pdf"}, keysOf(h.cursorFiles(t)))
		})
	}
}

// A forced full run must receive the stored cursor as its deletion baseline:
// a file present in that cursor but gone from Seafile is deleted even though
// every surviving file is re-downloaded.
func TestProcessSync_SeafileFullStreamReceivesStoredCursor(t *testing.T) {
	gone := seafileExternalID("/docs/gone.pdf")
	h := newSeafileSyncHarness(t, true, seafileCursor(seafileTestRepo, map[string]string{
		"/docs/gone.pdf": "o:old|m:1|s:4",
		"/docs/keep.pdf": "o:stale|m:1|s:4",
	}))
	h.server.files["/docs/keep.pdf"] = []byte("keep")
	h.repo.live["ds-seafile|"+gone] = &types.Knowledge{ID: "knowledge-gone"}

	log, err := h.run(t, true)
	require.NoError(t, err)
	assert.Equal(t, types.SyncLogStatusSuccess, log.Status)
	assert.Equal(t, 1, log.ItemsDeleted)
	assert.Equal(t, 1, log.ItemsCreated)
	assert.Equal(t, []string{"knowledge-gone"}, h.knowledge.deleted)
	cur, err := h.ds.ParseSyncCursor()
	require.NoError(t, err)
	assert.Nil(t, cur.ConnectorCursor["full_sync"], "completed full sync must clear its state")
	assert.Equal(t, []string{"/docs/keep.pdf"}, keysOf(h.cursorFiles(t)))
}

// Pointing an already synced data source at another library fails before any
// request and leaves the stored cursor untouched.
func TestProcessSync_SeafileLibraryMismatchFailsWithoutTouchingCursor(t *testing.T) {
	h := newSeafileSyncHarness(t, true, seafileCursor(seafileTestOtherRepo, map[string]string{
		"/docs/a.pdf": "o:a|m:1|s:1",
	}))
	before := string(h.ds.LastSyncCursor)

	log, err := h.run(t, false)
	require.Error(t, err)
	assert.ErrorIs(t, err, datasource.ErrInvalidConfig)
	assert.Equal(t, types.SyncLogStatusFailed, log.Status)
	assert.Contains(t, log.ErrorMessage, "Library")
	assert.Equal(t, types.DataSourceStatusError, h.ds.Status)
	assert.Equal(t, before, string(h.ds.LastSyncCursor))
	assert.Zero(t, h.server.requestCount())
	assert.Empty(t, h.knowledge.created)
	assert.Empty(t, h.knowledge.deleted)
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
