package seafile

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

var errRecordedEmit = errors.New("recorder emit failed")

type recorder struct {
	items         []types.FetchedItem
	checkpoints   []map[string]any
	failEmitAfter int
	cancelAfter   int
	cancel        context.CancelFunc
	checkpointErr error

	onEmit               func(types.FetchedItem)
	onCheckpoint         func(map[string]any)
	checkpointCalls      int
	checkpointsAfterStop int
	stopped              bool
}

var _ datasource.StreamHandler = (*recorder)(nil)

func (h *recorder) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	if h.onEmit != nil {
		h.onEmit(item)
	}
	if h.failEmitAfter > 0 && len(h.items) >= h.failEmitAfter {
		h.stopped = true
		return errRecordedEmit
	}
	if h.cancelAfter > 0 && len(h.items) >= h.cancelAfter {
		h.stopped = true
		h.cancel()
	}
	return nil
}

func (h *recorder) Checkpoint(ctx context.Context, cur *types.SyncCursor) error {
	h.checkpointCalls++
	if h.stopped || ctx.Err() != nil {
		h.checkpointsAfterStop++
	}
	snapshot := cloneConnectorCursor(cur.ConnectorCursor)
	h.checkpoints = append(h.checkpoints, snapshot)
	if h.checkpointErr != nil {
		h.stopped = true
		return h.checkpointErr
	}
	if h.onCheckpoint != nil {
		h.onCheckpoint(snapshot)
	}
	return nil
}

func (h *recorder) contentItems() []types.FetchedItem {
	var out []types.FetchedItem
	for _, item := range h.items {
		if !item.IsDeleted && item.Content != nil && item.Metadata["error"] == "" {
			out = append(out, item)
		}
	}
	return out
}

func (h *recorder) errorItems() []types.FetchedItem {
	var out []types.FetchedItem
	for _, item := range h.items {
		if item.Metadata["error"] != "" {
			out = append(out, item)
		}
	}
	return out
}

func (h *recorder) deletedItems() []types.FetchedItem {
	var out []types.FetchedItem
	for _, item := range h.items {
		if item.IsDeleted {
			out = append(out, item)
		}
	}
	return out
}

func (h *recorder) paths() []string {
	out := make([]string, 0, len(h.items))
	for _, item := range h.items {
		out = append(out, item.Metadata["seafile_path"])
	}
	slices.Sort(out)
	return out
}

func run(
	t *testing.T, c *Connector, cfg *types.DataSourceConfig, old *types.SyncCursor, h *recorder, full bool,
) (*types.SyncCursor, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	h.cancel = cancel
	if full {
		return c.FetchFullStream(ctx, cfg, old, h)
	}
	return c.FetchStream(ctx, cfg, old, h)
}

func cloneConnectorCursor(m map[string]any) map[string]any {
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	return out
}

func filesOf(cur *types.SyncCursor) map[string]string {
	if cur == nil {
		return nil
	}
	b, err := json.Marshal(cur.ConnectorCursor["files"])
	if err != nil {
		panic(err)
	}
	var files map[string]string
	if err := json.Unmarshal(b, &files); err != nil {
		panic(err)
	}
	return files
}

func syncConfig(f *fakeSeafile, roots ...string) *types.DataSourceConfig {
	cfg := &types.DataSourceConfig{Credentials: map[string]any{
		"base_url": f.baseURL(), "api_token": testToken,
	}}
	for _, p := range roots {
		cfg.ResourceIDs = append(cfg.ResourceIDs, testRepoID+":"+p)
	}
	return cfg
}

func syncFixture(t *testing.T, roots ...string) (*fakeSeafile, *Connector, *types.DataSourceConfig) {
	t.Helper()
	noWait(t)
	f := newFakeSeafile(t)
	f.addRepo(testRepoID, "Library", "repo", false)
	return f, NewConnector(), syncConfig(f, roots...)
}

func seededCursor(t *testing.T, name string, files map[string]string) *types.SyncCursor {
	t.Helper()
	c, _, err := prepareCursor(nil, testRepoID, false)
	if err != nil {
		t.Fatal(err)
	}
	c.RepoName = name
	c.Files = files
	return c.toSyncCursor()
}

func cursorJSON(t *testing.T, c *types.SyncCursor) string {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustCursor(t *testing.T, cur *types.SyncCursor) cursor {
	t.Helper()
	if cur == nil {
		t.Fatal("missing cursor")
	}
	c, err := decodeCursor(cur)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func checkpointCursor(t *testing.T, h *recorder) *types.SyncCursor {
	t.Helper()
	if len(h.checkpoints) == 0 {
		t.Fatal("missing checkpoint")
	}
	return &types.SyncCursor{ConnectorCursor: cloneConnectorCursor(h.checkpoints[len(h.checkpoints)-1])}
}

func mustRun(
	t *testing.T, c *Connector, cfg *types.DataSourceConfig, old *types.SyncCursor, full bool,
) (*types.SyncCursor, *recorder) {
	t.Helper()
	h := &recorder{}
	cur, err := run(t, c, cfg, old, h, full)
	if err != nil {
		t.Fatal(err)
	}
	if cur == nil {
		t.Fatal("successful run returned no cursor")
	}
	return cur, h
}

func assertPaths(t *testing.T, items []types.FetchedItem, want ...string) {
	t.Helper()
	h := &recorder{items: items}
	got := h.paths()
	want = slices.Clone(want)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	for _, item := range items {
		p := item.Metadata["seafile_path"]
		if item.ExternalID != "seafile:"+testRepoID+":"+p || item.SourceResourceID != testRepoID+":/" {
			t.Fatalf("item identity = %+v", item)
		}
	}
}

func addNumberedFiles(f *fakeSeafile, dir string, n int) {
	for i := 0; i < n; i++ {
		f.addFile(testRepoID, fmt.Sprintf("%s/%03d.pdf", dir, i), []byte("x"), 1)
	}
}

// assertFullSyncCleared checks that a completed full run dropped both the
// flag and the baseline from the persisted cursor.
func assertFullSyncCleared(t *testing.T, cur *types.SyncCursor) {
	t.Helper()
	if cur == nil {
		t.Fatal("missing cursor")
	}
	_, flag := cur.ConnectorCursor["full_sync"]
	_, baseline := cur.ConnectorCursor["full_sync_baseline"]
	if flag || baseline {
		t.Fatalf("completed full sync kept its state: %v", cur.ConnectorCursor)
	}
}

func assertPending(t *testing.T, cur *types.SyncCursor, p string) {
	t.Helper()
	if fp, ok := filesOf(cur)[p]; !ok || fp != "" {
		t.Fatalf("%s should be retained with empty fingerprint: %v", p, filesOf(cur))
	}
}

func TestSyncRecursiveScopeAndMetadata(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs", "/docs/产品")
	f.addFile(testRepoID, "/docs/产品/需求: v2.docx", []byte("body"), 7)
	f.addFile(testRepoID, "/docs/map.xmind", []byte("map"), 1)
	f.addFile(testRepoID, "/docs/skip.zip", []byte("zip"), 1)
	f.addFile(testRepoID, "/docs/skip.mdx", []byte("mdx"), 1)
	f.addFile(testRepoID, "/docs-sibling/secret.pdf", []byte("secret"), 1)
	cur, h := mustRun(t, c, cfg, nil, false)
	assertPaths(t, h.contentItems(), "/docs/产品/需求: v2.docx", "/docs/map.xmind")
	if len(h.errorItems()) != 0 || len(filesOf(cur)) != 2 || f.count("dir:/docs/产品") != 1 ||
		f.count("dir:/docs-sibling") != 0 || f.count("file:/docs/skip.zip") != 0 ||
		f.count("file:/docs/skip.mdx") != 0 {
		t.Fatalf("scope traversal: counts=%v, files=%v", f.counts(), filesOf(cur))
	}
	for _, item := range h.contentItems() {
		p := item.Metadata["seafile_path"]
		if item.FileName != knowledgeFileName("Library", p) || item.Title != "Library"+p {
			t.Fatalf("display names = %+v", item)
		}
		if item.Metadata["channel"] != "seafile" || item.Metadata["source_type"] == "" ||
			item.Metadata["seafile_repo_id"] != testRepoID || item.Metadata["seafile_repo_name"] != "Library" {
			t.Fatalf("metadata = %v", item.Metadata)
		}
		if strings.HasSuffix(p, ".docx") {
			oid := fmt.Sprintf("%x", sha1.Sum([]byte("body")))
			if string(item.Content) != "body" || item.Metadata["seafile_object_id"] != oid ||
				item.Metadata["seafile_mtime"] != "7" || item.Metadata["seafile_size"] != "4" {
				t.Fatalf("content metadata = %+v", item)
			}
		}
	}
	f.addFile(testRepoID, "/docs/new/deep/new.pdf", []byte("new"), 2)
	cur, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.contentItems(), "/docs/new/deep/new.pdf")
	if len(filesOf(cur)) != 3 {
		t.Fatalf("descendant missing from cursor: %v", filesOf(cur))
	}
}

func TestSyncSingleFileAndDisappearance(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs/a.pdf")
	f.addFile(testRepoID, "/docs/a.pdf", []byte("a"), 1)
	f.addFile(testRepoID, "/docs/b.pdf", []byte("b"), 1)
	f.addFile(testRepoID, "/docs/sub/c.pdf", []byte("c"), 1)
	old, h := mustRun(t, c, cfg, nil, false)
	assertPaths(t, h.contentItems(), "/docs/a.pdf")
	// The root is probed once as a directory (404), then its parent is listed once.
	if f.count("dir:/docs") != 1 || f.count("dir:/docs/a.pdf") != 1 || f.countPrefix("dir:") != 2 ||
		f.countPrefix("download:") != 1 {
		t.Fatalf("single-file selection requests = %v", f.counts())
	}
	before := cursorJSON(t, old)
	f.removePath(testRepoID, "/docs/a.pdf")
	h = &recorder{}
	cur, err := run(t, c, cfg, old, h, false)
	if !errors.Is(err, datasource.ErrResourceNotFound) || len(h.deletedItems()) != 0 {
		t.Fatalf("selected file disappeared: %v, items=%+v", err, h.items)
	}
	if cursorJSON(t, old) != before {
		t.Fatal("failed run mutated the old cursor")
	}
	if cur != nil {
		if _, ok := filesOf(cur)["/docs/a.pdf"]; !ok {
			t.Fatal("returned cursor forgot the selected file")
		}
	}
	for _, cp := range h.checkpoints {
		if _, ok := filesOf(&types.SyncCursor{ConnectorCursor: cp})["/docs/a.pdf"]; !ok {
			t.Fatal("checkpoint forgot the selected file")
		}
	}
}

func TestSyncUnsupportedFileRootIsSkipped(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs/archive.zip", "/docs/sub")
	f.addFile(testRepoID, "/docs/archive.zip", []byte("zip"), 1)
	f.addFile(testRepoID, "/docs/sub/a.pdf", []byte("a"), 1)
	cur, h := mustRun(t, c, cfg, nil, false)
	assertPaths(t, h.contentItems(), "/docs/sub/a.pdf")
	if len(h.errorItems()) != 0 || f.count("file:/docs/archive.zip") != 0 || len(filesOf(cur)) != 1 {
		t.Fatalf("unsupported file root: items=%+v, counts=%v, files=%v", h.items, f.counts(), filesOf(cur))
	}
	// A selected directory replaced by an extensionless file loses its
	// children through reconciliation instead of importing the replacement.
	f.removePath(testRepoID, "/docs/sub")
	f.addFile(testRepoID, "/docs/sub", []byte("blob"), 2)
	cur, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.contentItems())
	assertPaths(t, h.deletedItems(), "/docs/sub/a.pdf")
	if f.count("file:/docs/sub") != 0 || len(filesOf(cur)) != 0 {
		t.Fatalf("directory replaced by unsupported file: counts=%v, files=%v", f.counts(), filesOf(cur))
	}
}

func TestSyncFileReplacedByDirectory(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/a.pdf", []byte("a"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.removePath(testRepoID, "/docs/a.pdf")
	f.addFile(testRepoID, "/docs/a.pdf/child.pdf", []byte("child"), 2)
	cur, h := mustRun(t, c, cfg, old, false)
	assertPaths(t, h.contentItems(), "/docs/a.pdf/child.pdf")
	assertPaths(t, h.deletedItems(), "/docs/a.pdf")
	if len(filesOf(cur)) != 1 || f.count("dir:/docs/a.pdf") != 1 {
		t.Fatalf("directory without size not traversed: %v, %v", filesOf(cur), f.counts())
	}
}

func TestSyncNoChangeAndMtimeOnly(t *testing.T) {
	f, c, cfg := syncFixture(t, "/")
	f.addFile(testRepoID, "/docs/a.pdf", []byte("a"), 1)
	f.addFile(testRepoID, "/docs/sub/b.pdf", []byte("b"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.resetCounts()
	cur, h := mustRun(t, c, cfg, old, false)
	if len(h.items) != 0 || f.countPrefix("download:") != 0 || f.countPrefix("dir:") != 3 ||
		f.count("repos") != 1 || len(h.checkpoints) > 1 {
		t.Fatalf("unchanged run: counts=%v, items=%d, checkpoints=%d", f.counts(), len(h.items), len(h.checkpoints))
	}
	f.touch(testRepoID, "/docs/a.pdf")
	f.resetCounts()
	next, h := mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.contentItems(), "/docs/a.pdf")
	if f.count("download:/docs/a.pdf") != 1 || filesOf(next)["/docs/a.pdf"] == filesOf(cur)["/docs/a.pdf"] {
		t.Fatal("mtime-only change did not re-download and update the fingerprint")
	}
}

func TestSyncFileFailures(t *testing.T) {
	cases := []struct {
		name, code string
		status     int
		inject     func(*fakeSeafile, string)
	}{
		{"forbidden", "seafile_permission_denied", 403, func(f *fakeSeafile, p string) { f.fail("file:"+p, 403, -1) }},
		{"missing", "seafile_not_found", 404, func(f *fakeSeafile, p string) { f.fail("file:"+p, 404, -1) }},
		{"server error", "seafile_fetch_failed", 500, func(f *fakeSeafile, p string) { f.fail("file:"+p, 500, -1) }},
		{"empty", "seafile_empty_file", 0, func(f *fakeSeafile, p string) { f.setBody(testRepoID, p, nil) }},
		{"listed oversize", "seafile_file_too_large", 0, func(f *fakeSeafile, p string) {
			f.setBody(testRepoID, p, []byte(strings.Repeat("x", (1<<20)+1)))
		}},
		{"chunked oversize", "seafile_file_too_large", 0, func(f *fakeSeafile, p string) {
			f.chunkedOversize[p] = (1 << 20) + 1
		}},
		{"changed object", "seafile_source_changed", 0, func(f *fakeSeafile, p string) {
			f.linkOID[p] = "different-object"
		}},
		{"malformed link", "seafile_invalid_response", 0, func(f *fakeSeafile, p string) {
			f.rawBody["file:"+p] = "{"
		}},
		{"SSRF", "seafile_ssrf_blocked", 0, func(f *fakeSeafile, p string) {
			f.linkURL[p] = "http://169.254.169.254/blocked"
		}},
	}
	for _, tc := range cases {
		for _, known := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/known=%v", tc.name, known), func(t *testing.T) {
				f, c, cfg := syncFixture(t, "/docs")
				// GetMaxFileSize reads this environment variable without caching.
				t.Setenv("MAX_FILE_SIZE_MB", "1")
				p := "/docs/a.pdf"
				f.addFile(testRepoID, p, []byte("body"), 1)
				var old *types.SyncCursor
				if known {
					old, _ = mustRun(t, c, cfg, nil, false)
					f.touch(testRepoID, p)
				}
				tc.inject(f, p)
				cur, h := mustRun(t, c, cfg, old, false)
				assertPaths(t, h.errorItems(), p)
				if len(h.items) != 1 {
					t.Fatalf("failure emitted extra items: %+v", h.items)
				}
				item := h.errorItems()[0]
				if item.Content != nil || item.IsDeleted || item.Metadata["error_reason_code"] != tc.code ||
					item.Metadata["error_reason"] == "" {
					t.Fatalf("error item = %+v", item)
				}
				if value := item.Metadata["error_reason_code_value"]; tc.status != 0 && value != fmt.Sprint(tc.status) {
					t.Fatalf("HTTP reason value = %q, status=%d", value, tc.status)
				}
				if known {
					assertPending(t, cur, p)
				} else if _, ok := filesOf(cur)[p]; ok {
					t.Fatalf("unknown failure entered cursor: %v", filesOf(cur))
				}
				f.mu.Lock()
				delete(f.failures, "file:"+p)
				delete(f.chunkedOversize, p)
				delete(f.linkOID, p)
				delete(f.linkURL, p)
				delete(f.rawBody, "file:"+p)
				f.mu.Unlock()
				f.setBody(testRepoID, p, []byte("recovered"))
				f.resetCounts()
				next, retry := mustRun(t, c, cfg, cur, false)
				assertPaths(t, retry.contentItems(), p)
				if filesOf(next)[p] == "" || f.count("download:"+p) != 1 {
					t.Fatalf("pending file not retried: %v, %v", filesOf(next), f.counts())
				}
			})
		}
	}
}

func TestSyncExactSizeLimit(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	body := []byte(strings.Repeat("x", 1<<20))
	f.addFile(testRepoID, "/docs/exact.pdf", body, 1)
	_, h := mustRun(t, c, cfg, nil, false)
	assertPaths(t, h.contentItems(), "/docs/exact.pdf")
	if len(h.contentItems()[0].Content) != len(body) || len(h.errorItems()) != 0 {
		t.Fatal("file exactly at limit was not accepted")
	}
}

func TestSyncCheckpointCadenceAndResume(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	addNumberedFiles(f, "/docs", 120)
	cur, h := mustRun(t, c, cfg, nil, false)
	if len(h.contentItems()) != 120 || len(h.checkpoints) < 3 {
		t.Fatalf("items=%d, checkpoints=%d", len(h.contentItems()), len(h.checkpoints))
	}
	previous := 0
	for _, cp := range h.checkpoints {
		n := len(filesOf(&types.SyncCursor{ConnectorCursor: cp}))
		if n < previous || n-previous > 50 {
			t.Fatalf("checkpoint sizes: previous=%d, current=%d", previous, n)
		}
		previous = n
	}
	if previous != 120 || len(filesOf(cur)) != 120 {
		t.Fatalf("final checkpoint=%d, files=%d", previous, len(filesOf(cur)))
	}
	// From an initialized cursor every mutation is a file, so the first
	// checkpoint lands exactly on the 50th file.
	h = &recorder{}
	h.onCheckpoint = func(cp map[string]any) {
		if len(filesOf(&types.SyncCursor{ConnectorCursor: cp})) >= 50 {
			h.cancel()
		}
	}
	_, err := run(t, c, cfg, seededCursor(t, "Library", map[string]string{}), h, false)
	if !errors.Is(err, context.Canceled) || h.checkpointsAfterStop != 0 {
		t.Fatalf("checkpoint cancellation = %v; late checkpoints=%d", err, h.checkpointsAfterStop)
	}
	saved := checkpointCursor(t, h)
	savedFiles := filesOf(saved)
	if len(savedFiles) != 50 {
		t.Fatalf("first progress checkpoint saved %d files", len(savedFiles))
	}
	f.resetCounts()
	cur, resumed := mustRun(t, c, cfg, saved, false)
	if len(resumed.contentItems()) != 70 || len(filesOf(cur)) != 120 {
		t.Fatalf("resume items=%d, files=%d", len(resumed.contentItems()), len(filesOf(cur)))
	}
	for p := range savedFiles {
		if f.count("download:"+p) != 0 {
			t.Fatalf("saved file downloaded again: %s", p)
		}
	}
}

// Known-file failures flip fingerprints to empty; those mutations must
// respect the same 50-change checkpoint bound as successful downloads.
func TestSyncKnownFailuresCheckpointCadence(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	addNumberedFiles(f, "/docs", 120)
	old, _ := mustRun(t, c, cfg, nil, false)
	for i := 0; i < 120; i++ {
		p := fmt.Sprintf("/docs/%03d.pdf", i)
		f.touch(testRepoID, p)
		f.fail("file:"+p, 500, -1)
	}
	pending := func(cp map[string]any) int {
		n := 0
		for _, fp := range filesOf(&types.SyncCursor{ConnectorCursor: cp}) {
			if fp == "" {
				n++
			}
		}
		return n
	}
	h := &recorder{}
	h.onCheckpoint = func(cp map[string]any) {
		if pending(cp) >= 50 {
			h.cancel()
		}
	}
	_, err := run(t, c, cfg, old, h, false)
	if !errors.Is(err, context.Canceled) || h.checkpointsAfterStop != 0 {
		t.Fatalf("failure cadence = %v; late checkpoints=%d", err, h.checkpointsAfterStop)
	}
	if got := pending(h.checkpoints[len(h.checkpoints)-1]); got != 50 || len(h.errorItems()) != 50 {
		t.Fatalf("first failure checkpoint saved %d pending paths after %d error items", got, len(h.errorItems()))
	}

	h = &recorder{checkpointErr: errors.New("checkpoint unavailable")}
	if _, err := run(t, c, cfg, old, h, false); !errors.Is(err, h.checkpointErr) || len(h.errorItems()) != 50 {
		t.Fatalf("checkpoint failure on the failure path = %v after %d error items", err, len(h.errorItems()))
	}
}

func TestSyncHandlerFailuresStopImmediately(t *testing.T) {
	checkpointFailure := errors.New("checkpoint unavailable")
	for _, tc := range []struct {
		name               string
		handler            *recorder
		want               error
		emits, checkpoints int
	}{
		{"early cancel", &recorder{cancelAfter: 1}, context.Canceled, 1, 0},
		{"cancel after progress", &recorder{cancelAfter: 51}, context.Canceled, 51, 1},
		{"emit failure", &recorder{failEmitAfter: 51}, errRecordedEmit, 51, 1},
		{"checkpoint failure", &recorder{checkpointErr: checkpointFailure}, checkpointFailure, 50, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, cfg := syncFixture(t, "/docs")
			addNumberedFiles(f, "/docs", 120)
			_, err := run(t, c, cfg, seededCursor(t, "Library", map[string]string{}), tc.handler, false)
			h := tc.handler
			if !errors.Is(err, tc.want) || len(h.items) != tc.emits || len(h.checkpoints) != tc.checkpoints ||
				h.checkpointsAfterStop != 0 || f.countPrefix("download:") != tc.emits ||
				f.countPrefix("file:") != tc.emits {
				t.Fatalf("err=%v, emits=%d, checkpoints=%d, late=%d, counts=%v",
					err, len(h.items), len(h.checkpoints), h.checkpointsAfterStop, f.counts())
			}
		})
	}
}

func TestSyncDeletionAndCheckpointBeforeDeletion(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/gone.pdf", []byte("gone"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.removePath(testRepoID, "/docs/gone.pdf")
	f.addFile(testRepoID, "/docs/new.pdf", []byte("new"), 1)
	h := &recorder{}
	h.onEmit = func(item types.FetchedItem) {
		if item.IsDeleted {
			saved := filesOf(checkpointCursor(t, h))
			if saved["/docs/new.pdf"] == "" || saved["/docs/gone.pdf"] == "" {
				t.Fatalf("pre-deletion checkpoint lost progress/baseline: %v", saved)
			}
		}
	}
	cur, err := run(t, c, cfg, old, h, false)
	if err != nil {
		t.Fatal(err)
	}
	assertPaths(t, h.deletedItems(), "/docs/gone.pdf")
	assertPaths(t, h.contentItems(), "/docs/new.pdf")
	if len(filesOf(cur)) != 1 {
		t.Fatalf("deletion not reflected in cursor: %v", filesOf(cur))
	}
	_, again := mustRun(t, c, cfg, cur, false)
	assertPaths(t, again.deletedItems())
}

// Cancellation raised inside a handler callback during reconciliation must stop
// the connector itself: no further deletion is emitted and nothing is saved.
func TestSyncCancelDuringReconciliationStopsImmediately(t *testing.T) {
	for _, tc := range []struct {
		name  string
		wire  func(h *recorder)
		emits int
	}{
		{"pre-deletion checkpoint", func(h *recorder) {
			h.onCheckpoint = func(map[string]any) { h.cancel() }
		}, 1},
		{"first deletion", func(h *recorder) {
			h.onEmit = func(item types.FetchedItem) {
				if item.IsDeleted {
					h.cancel()
				}
			}
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, cfg := syncFixture(t, "/docs")
			f.addFile(testRepoID, "/docs/gone-a.pdf", []byte("a"), 1)
			f.addFile(testRepoID, "/docs/gone-b.pdf", []byte("b"), 1)
			old, _ := mustRun(t, c, cfg, nil, false)
			f.removePath(testRepoID, "/docs/gone-a.pdf")
			f.removePath(testRepoID, "/docs/gone-b.pdf")
			f.addFile(testRepoID, "/docs/new.pdf", []byte("new"), 1)
			h := &recorder{}
			tc.wire(h)
			_, err := run(t, c, cfg, old, h, false)
			if !errors.Is(err, context.Canceled) || len(h.items) != tc.emits || len(h.checkpoints) != 1 {
				t.Fatalf("cancel in %s = %v, items=%d, checkpoints=%d", tc.name, err, len(h.items), len(h.checkpoints))
			}
		})
	}
}

func TestSyncDirectoryFailuresKeepProgressAndSuppressDeletion(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inject func(*fakeSeafile)
		want   error
		reason string
	}{
		{"500", func(f *fakeSeafile) { f.fail("dir:/z", 500, -1) }, datasource.ErrFetchFailed, ""},
		{"503 exhausted", func(f *fakeSeafile) { f.fail("dir:/z", 503, -1) }, datasource.ErrFetchFailed, ""},
		{"subdir forbidden", func(f *fakeSeafile) {
			f.addDir(testRepoID, "/z/private")
			f.forbidden["/z/private"] = true
		}, datasource.ErrFetchFailed, "seafile_permission_denied"},
		{"malformed entry", func(f *fakeSeafile) {
			f.rawBody["dir:/z"] = `[{"id":"bad","name":"../outside.pdf","type":"file","size":1,"mtime":1}]`
		}, datasource.ErrFetchFailed, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c, cfg := syncFixture(t, "/a", "/z")
			f.addFile(testRepoID, "/a/gone.pdf", []byte("gone"), 1)
			f.addFile(testRepoID, "/z/keep.pdf", []byte("keep"), 1)
			old, _ := mustRun(t, c, cfg, nil, false)
			f.removePath(testRepoID, "/a/gone.pdf")
			f.addFile(testRepoID, "/a/new.pdf", []byte("new"), 1)
			tc.inject(f)
			h := &recorder{}
			_, err := run(t, c, cfg, old, h, false)
			if !errors.Is(err, tc.want) || (tc.reason != "" && !strings.Contains(err.Error(), tc.reason)) {
				t.Fatalf("directory failure = %v, want %v / %s", err, tc.want, tc.reason)
			}
			assertPaths(t, h.deletedItems())
			assertPaths(t, h.contentItems(), "/a/new.pdf")
			saved := filesOf(checkpointCursor(t, h))
			for _, p := range []string{"/a/gone.pdf", "/a/new.pdf", "/z/keep.pdf"} {
				if saved[p] == "" {
					t.Fatalf("directory failure lost %s: %v", p, saved)
				}
			}
		})
	}
}

func TestSyncFileFailureDoesNotSuppressOtherDeletions(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/fail.pdf", []byte("fail"), 1)
	f.addFile(testRepoID, "/docs/gone.pdf", []byte("gone"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.touch(testRepoID, "/docs/fail.pdf")
	f.fail("file:/docs/fail.pdf", 500, -1)
	f.removePath(testRepoID, "/docs/gone.pdf")
	cur, h := mustRun(t, c, cfg, old, false)
	assertPaths(t, h.errorItems(), "/docs/fail.pdf")
	assertPaths(t, h.deletedItems(), "/docs/gone.pdf")
	assertPending(t, cur, "/docs/fail.pdf")
	if len(filesOf(cur)) != 1 {
		t.Fatalf("cursor = %v", filesOf(cur))
	}
}

func TestSyncLongRetryAfterAborts(t *testing.T) {
	f, c, cfg := syncFixture(t, "/a", "/b", "/z")
	f.addFile(testRepoID, "/a/gone.pdf", []byte("gone"), 1)
	f.addDir(testRepoID, "/b")
	f.addFile(testRepoID, "/z/later.pdf", []byte("later"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.removePath(testRepoID, "/a/gone.pdf")
	f.addFile(testRepoID, "/a/progress.pdf", []byte("progress"), 1)
	f.addFile(testRepoID, "/b/stop.pdf", []byte("stop"), 1)
	f.addFile(testRepoID, "/b/zz-later.pdf", []byte("later"), 1)
	f.failWithRetryAfter("file:/b/stop.pdf", 429, -1, "120")
	f.resetCounts()
	h := &recorder{}
	var atAbort map[string]int
	h.onCheckpoint = func(map[string]any) { atAbort = f.counts() }
	_, err := run(t, c, cfg, old, h, false)
	if !errors.Is(err, datasource.ErrFetchFailed) || f.count("file:/b/stop.pdf") != 1 ||
		f.count("download:/b/stop.pdf") != 0 || f.count("dir:/z") != 0 ||
		f.countPrefix("file:") != 2 || f.countPrefix("download:") != 1 {
		t.Fatalf("Retry-After abort = %v, counts=%v", err, f.counts())
	}
	assertPaths(t, h.deletedItems())
	saved := filesOf(checkpointCursor(t, h))
	if saved["/a/progress.pdf"] == "" || saved["/a/gone.pdf"] == "" ||
		!reflect.DeepEqual(atAbort, f.counts()) {
		t.Fatalf("abort checkpoint or subsequent requests: saved=%v, before=%v, after=%v", saved, atAbort, f.counts())
	}
}

func TestSyncMoveDuringScan(t *testing.T) {
	f, c, cfg := syncFixture(t, "/A", "/B")
	f.addFile(testRepoID, "/A/first.pdf", []byte("first"), 1)
	f.addFile(testRepoID, "/B/moving.pdf", []byte("moving"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.touch(testRepoID, "/A/first.pdf")
	f.resetCounts()
	h := &recorder{}
	moved := false
	h.onEmit = func(item types.FetchedItem) {
		if item.Metadata["seafile_path"] == "/A/first.pdf" && item.Content != nil {
			if f.count("dir:/B") != 0 {
				t.Fatal("B was listed before the move")
			}
			f.movePath(testRepoID, "/B/moving.pdf", "/A/moving.pdf")
			moved = true
		}
	}
	cur, err := run(t, c, cfg, old, h, false)
	if err != nil || !moved {
		t.Fatalf("move during scan = %v, moved=%v", err, moved)
	}
	assertPaths(t, h.contentItems(), "/A/first.pdf")
	assertPaths(t, h.deletedItems(), "/B/moving.pdf")
	_, next := mustRun(t, c, cfg, cur, false)
	assertPaths(t, next.contentItems(), "/A/moving.pdf")
	assertPaths(t, next.deletedItems())
}

func TestSyncRenameAndMoveOutOfScope(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/old.pdf", []byte("body"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.movePath(testRepoID, "/docs/old.pdf", "/docs/new.pdf")
	cur, h := mustRun(t, c, cfg, old, false)
	assertPaths(t, h.contentItems(), "/docs/new.pdf")
	assertPaths(t, h.deletedItems(), "/docs/old.pdf")
	f.movePath(testRepoID, "/docs/new.pdf", "/elsewhere/new.pdf")
	cur, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.contentItems())
	assertPaths(t, h.deletedItems(), "/docs/new.pdf")
	if len(filesOf(cur)) != 0 || f.count("dir:/elsewhere") != 0 {
		t.Fatalf("move out of scope: files=%v, counts=%v", filesOf(cur), f.counts())
	}
}

func TestSyncScopeShrinkAndExpand(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/a/one.pdf", []byte("one"), 1)
	f.addFile(testRepoID, "/docs/b/two.pdf", []byte("two"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	cfg.ResourceIDs = []string{testRepoID + ":/docs/a"}
	cur, h := mustRun(t, c, cfg, old, false)
	assertPaths(t, h.deletedItems(), "/docs/b/two.pdf")
	assertPaths(t, h.contentItems())
	cfg.ResourceIDs = []string{testRepoID + ":/docs"}
	cur, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.contentItems(), "/docs/b/two.pdf")
	assertPaths(t, h.deletedItems())
	if len(filesOf(cur)) != 2 {
		t.Fatalf("expanded cursor = %v", filesOf(cur))
	}
}

func TestSyncSelectedDirectoryDisappears(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/a.pdf", []byte("a"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.removePath(testRepoID, "/docs")
	h := &recorder{}
	cur, err := run(t, c, cfg, old, h, false)
	if !errors.Is(err, datasource.ErrResourceNotFound) {
		t.Fatalf("missing selected directory = %v", err)
	}
	assertPaths(t, h.deletedItems())
	if filesOf(old)["/docs/a.pdf"] == "" {
		t.Fatal("lost the old directory's file")
	}
	if cur != nil && filesOf(cur)["/docs/a.pdf"] == "" {
		t.Fatal("returned cursor lost the old directory's file")
	}
	for _, cp := range h.checkpoints {
		if filesOf(&types.SyncCursor{ConnectorCursor: cp})["/docs/a.pdf"] == "" {
			t.Fatal("checkpoint lost the old directory's file")
		}
	}
}

func TestSyncForcedFullCancelResumeAndUnionDeletions(t *testing.T) {
	for _, addedThenRemoved := range []bool{false, true} {
		t.Run(fmt.Sprintf("added-then-removed=%v", addedThenRemoved), func(t *testing.T) {
			f, c, cfg := syncFixture(t, "/docs")
			addNumberedFiles(f, "/docs", 65)
			f.addFile(testRepoID, "/docs/vanished.pdf", []byte("gone"), 1)
			old, _ := mustRun(t, c, cfg, nil, false)
			before := cursorJSON(t, old)
			f.removePath(testRepoID, "/docs/vanished.pdf")
			if addedThenRemoved {
				f.addFile(testRepoID, "/docs/000-new.pdf", []byte("new"), 1)
			}
			f.resetCounts()
			h := &recorder{}
			h.onCheckpoint = func(map[string]any) { h.cancel() }
			_, err := run(t, c, cfg, old, h, true)
			if !errors.Is(err, context.Canceled) || h.checkpointsAfterStop != 0 {
				t.Fatalf("full cancel = %v, late checkpoints=%d", err, h.checkpointsAfterStop)
			}
			saved := checkpointCursor(t, h)
			active := mustCursor(t, saved)
			// Switching into full sync is itself a mutation, so the first batch
			// holds 49 files.
			batch := len(active.Files)
			if !active.FullSync || !reflect.DeepEqual(active.FullSyncBaseline, filesOf(old)) ||
				batch != 49 || len(h.contentItems()) != batch || f.countPrefix("download:") != batch {
				t.Fatalf("full checkpoint files=%d downloads=%d", batch, f.countPrefix("download:"))
			}
			if cursorJSON(t, old) != before {
				t.Fatal("forced full mutated its input baseline")
			}
			wantDeleted := []string{"/docs/vanished.pdf"}
			if addedThenRemoved {
				if active.Files["/docs/000-new.pdf"] == "" {
					t.Fatal("new file was not saved before cancellation")
				}
				f.removePath(testRepoID, "/docs/000-new.pdf")
				wantDeleted = append(wantDeleted, "/docs/000-new.pdf")
			}
			f.resetCounts()
			cur, resumed := mustRun(t, c, cfg, saved, false)
			assertPaths(t, resumed.deletedItems(), wantDeleted...)
			expectedRemaining := 65 - len(active.Files)
			if addedThenRemoved {
				expectedRemaining++
			}
			if len(resumed.contentItems()) != expectedRemaining {
				t.Fatalf("resume content=%d, want %d", len(resumed.contentItems()), expectedRemaining)
			}
			for p := range active.Files {
				if f.count("download:"+p) != 0 {
					t.Fatalf("full resume downloaded saved file %s", p)
				}
			}
			assertFullSyncCleared(t, cur)
			if len(filesOf(cur)) != 65 {
				t.Fatalf("full completion files = %d", len(filesOf(cur)))
			}
			_, next := mustRun(t, c, cfg, cur, false)
			assertPaths(t, next.items)
		})
	}
}

func TestSyncFullDirectoryFailureResumesIncrementally(t *testing.T) {
	f, c, cfg := syncFixture(t, "/a", "/z")
	f.addFile(testRepoID, "/a/one.pdf", []byte("one"), 1)
	f.addFile(testRepoID, "/z/two.pdf", []byte("two"), 1)
	f.addFile(testRepoID, "/z/gone.pdf", []byte("gone"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.removePath(testRepoID, "/z/gone.pdf")
	f.fail("dir:/z", 500, -1)
	h := &recorder{}
	_, err := run(t, c, cfg, old, h, true)
	if !errors.Is(err, datasource.ErrFetchFailed) {
		t.Fatalf("full directory failure = %v", err)
	}
	assertPaths(t, h.contentItems(), "/a/one.pdf")
	assertPaths(t, h.deletedItems())
	saved := checkpointCursor(t, h)
	active := mustCursor(t, saved)
	if !active.FullSync || !reflect.DeepEqual(active.FullSyncBaseline, filesOf(old)) ||
		active.Files["/a/one.pdf"] == "" {
		t.Fatalf("interrupted full cursor = %+v", active)
	}
	f.fail("dir:/z", 500, 0)
	f.resetCounts()
	cur, resumed := mustRun(t, c, cfg, saved, false)
	assertPaths(t, resumed.contentItems(), "/z/two.pdf")
	assertPaths(t, resumed.deletedItems(), "/z/gone.pdf")
	assertFullSyncCleared(t, cur)
	if f.count("download:/a/one.pdf") != 0 || len(filesOf(cur)) != 2 {
		t.Fatalf("completed cursor=%v, requests=%v", filesOf(cur), f.counts())
	}
}

func TestSyncLibraryRenameKeepsFolderName(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	f.addFile(testRepoID, "/docs/a.pdf", []byte("a"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.renameRepo(testRepoID, "Renamed library")
	f.resetCounts()
	cur, h := mustRun(t, c, cfg, old, false)
	if len(h.items) != 0 || f.countPrefix("download:") != 0 || mustCursor(t, cur).RepoName != "Library" {
		t.Fatalf("library rename caused churn: items=%+v, counts=%v, cursor=%+v", h.items, f.counts(), cur)
	}
	f.addFile(testRepoID, "/docs/b.pdf", []byte("b"), 1)
	_, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.contentItems(), "/docs/b.pdf")
	if h.contentItems()[0].FileName != "Library/docs/b.pdf" {
		t.Fatalf("renamed library changed destination folder: %q", h.contentItems()[0].FileName)
	}
}

func TestSyncFullFailureRetainsPendingThenRetriesAndDeletes(t *testing.T) {
	f, c, cfg := syncFixture(t, "/docs")
	p := "/docs/a.pdf"
	f.addFile(testRepoID, p, []byte("body"), 1)
	old, _ := mustRun(t, c, cfg, nil, false)
	f.fail("file:"+p, 500, -1)
	cur, h := mustRun(t, c, cfg, old, true)
	assertPaths(t, h.errorItems(), p)
	assertPaths(t, h.deletedItems())
	assertPending(t, cur, p)
	assertFullSyncCleared(t, cur)
	f.resetCounts()
	cur, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.errorItems(), p)
	assertPending(t, cur, p)
	if f.count("file:"+p) == 0 {
		t.Fatal("incremental did not retry the empty fingerprint")
	}
	f.removePath(testRepoID, p)
	cur, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.deletedItems(), p)
	if len(filesOf(cur)) != 0 {
		t.Fatalf("deleted pending path remains: %v", filesOf(cur))
	}
	_, h = mustRun(t, c, cfg, cur, false)
	assertPaths(t, h.deletedItems())
}

func compactRecorder() *recorder {
	h := &recorder{}
	// Scale runs need the latest resume point, not hundreds of full maps.
	// Each callback still receives a JSON-deep-copied snapshot.
	h.onCheckpoint = func(cp map[string]any) {
		h.checkpoints = []map[string]any{cp}
	}
	return h
}
