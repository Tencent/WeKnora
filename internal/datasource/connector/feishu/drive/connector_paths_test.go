package drive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/Tencent/WeKnora/internal/types"
)

// ──────────────────────────────────────────────────────────────────────
// P1 directory mapping tests for the Drive connector: path-qualified
// FileName, deletion mirroring, partial-listing suppression, move following.
// ──────────────────────────────────────────────────────────────────────

// fakeDriveTree serves a mutable Drive folder tree keyed by folder_token.
// folders maps folder token → direct children (folders recurse, files sync);
// failing folders return HTTP 500 to model a partial listing; metaNames maps
// folder token → display name for the folder meta endpoint (absent = error).
type fakeDriveTree struct {
	folders   map[string][]core.DriveFile
	failing   map[string]bool
	metaNames map[string]string
	// listCalls / metaCalls count API hits per token, so tests can assert a
	// file token is never probed with folder APIs.
	listCalls map[string]int
	metaCalls map[string]int
	ts        *httptest.Server
	cfg       *core.Config
}

func newFakeDriveTree(t *testing.T, folders map[string][]core.DriveFile) *fakeDriveTree {
	t.Helper()
	f := &fakeDriveTree{
		folders: folders, failing: map[string]bool{},
		listCalls: map[string]int{}, metaCalls: map[string]int{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal",
		func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, core.TokenResponse{
				ApiResponse:       core.ApiResponse{Code: 0},
				TenantAccessToken: "fake-token", Expire: 7200,
			})
		})
	// Folder listing (paged endpoint, exact path) and file download (subtree).
	mux.HandleFunc("/open-apis/drive/v1/files", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("folder_token")
		f.listCalls[token]++
		if f.failing[token] {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":1663,"msg":"internal error"}`))
			return
		}
		writeJSON(w, core.DriveFileListResponse{
			ApiResponse: core.ApiResponse{Code: 0},
			Data:        core.DriveFileListData{Files: f.folders[token]},
		})
	})
	mux.HandleFunc("/open-apis/drive/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/download") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte("fake-drive-file-content"))
	})
	mux.HandleFunc("/open-apis/drive/explorer/v2/folder/", func(w http.ResponseWriter, r *http.Request) {
		// path: /open-apis/drive/explorer/v2/folder/<token>/meta
		p := strings.TrimPrefix(r.URL.Path, "/open-apis/drive/explorer/v2/folder/")
		token := strings.TrimSuffix(p, "/meta")
		f.metaCalls[token]++
		name, ok := f.metaNames[token]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":1061004,"msg":"not found"}`))
			return
		}
		writeJSON(w, map[string]interface{}{
			"code": 0,
			"data": map[string]interface{}{"name": name},
		})
	})

	f.ts = httptest.NewServer(mux)
	t.Cleanup(f.ts.Close)
	f.cfg = &core.Config{AppID: "test-app-id", AppSecret: "test-app-secret", BaseURL: f.ts.URL}
	return f
}

// driveFile builds a regular file entry; folder builds a folder entry.
func driveFile(token, name, parent string, modified string) core.DriveFile {
	return core.DriveFile{Token: token, Name: name, Type: "file", ParentToken: parent, ModifiedTime: modified}
}

func driveFolder(token, name, parent string) core.DriveFile {
	return core.DriveFile{Token: token, Name: name, Type: "folder", ParentToken: parent}
}

// fetchedByToken indexes emitted non-deleted items by external_id.
func fetchedByToken(items []types.FetchedItem) map[string]types.FetchedItem {
	m := make(map[string]types.FetchedItem, len(items))
	for _, it := range items {
		if !it.IsDeleted {
			m[it.ExternalID] = it
		}
	}
	return m
}

func deletedIDs(items []types.FetchedItem) []string {
	var ids []string
	for _, it := range items {
		if it.IsDeleted {
			ids = append(ids, it.ExternalID)
		}
	}
	return ids
}

// TestDriveFetchIncremental_PathQualifiedMultiLevel anchors the directory
// mapping: the selected root is the KB root, so paths are <子目录...>/<文件名>
// relative to it, Chinese names kept, "/" in a folder
// name sanitised to "_", multi-level nesting resolved from the same walk.
func TestDriveFetchIncremental_PathQualifiedMultiLevel(t *testing.T) {
	f := newFakeDriveTree(t, map[string][]core.DriveFile{
		"fold-root": {
			driveFolder("fold-spec", "规格", "fold-root"),
			driveFolder("fold-bad", "a/b", "fold-root"), // "/" in the name → "_"
			driveFile("f-root", "说明.pdf", "fold-root", "100"),
		},
		"fold-spec": {driveFile("f-spec", "详细.pdf", "fold-spec", "200")},
		"fold-bad":  {driveFile("f-bad", "child.pdf", "fold-bad", "300")},
	})
	f.metaNames = map[string]string{"fold-root": "团队资料"}

	c := NewDriveConnector(core.RegionFeishuDrive)
	items, _, err := c.FetchIncremental(context.Background(), makeDriveConfig(f.cfg, []string{"fold-root"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}

	want := map[string]string{
		// The selected root folder IS the KB root: direct children carry no
		// prefix, deeper ones only the sub-directory path.
		"f-root": "说明.pdf",
		"f-spec": "规格/详细.pdf",
		"f-bad":  "a_b/child.pdf",
	}
	got := fetchedByToken(items)
	if len(got) != len(want) {
		t.Fatalf("got %d items (%v), want %d", len(got), got, len(want))
	}
	for id, wantPath := range want {
		if got[id].FileName != wantPath {
			t.Errorf("FileName for %s = %q, want %q", id, got[id].FileName, wantPath)
		}
	}
}

// TestDriveFetchIncremental_DeletionMirror anchors the deletion engine: a
// second full listing that misses a previously synced token emits exactly one
// IsDeleted item for it; the first sync (no cursor) emits none.
func TestDriveFetchIncremental_DeletionMirror(t *testing.T) {
	f := newFakeDriveTree(t, map[string][]core.DriveFile{
		"fold-root": {
			driveFile("f-a", "a.pdf", "fold-root", "100"),
			driveFile("f-b", "b.pdf", "fold-root", "200"),
		},
	})
	f.metaNames = map[string]string{"fold-root": "团队资料"}

	c := NewDriveConnector(core.RegionFeishuDrive)
	cfg := makeDriveConfig(f.cfg, []string{"fold-root"})

	items1, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if ids := deletedIDs(items1); len(ids) != 0 {
		t.Errorf("first sync emitted IsDeleted items %v — deletion must be suppressed without a prior cursor", ids)
	}

	// f-a disappears from the tree.
	f.folders["fold-root"] = []core.DriveFile{driveFile("f-b", "b.pdf", "fold-root", "200")}

	items2, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	ids := deletedIDs(items2)
	if len(ids) != 1 || ids[0] != "f-a" {
		t.Errorf("second sync deleted ids = %v, want [f-a]", ids)
	}
}

// TestDriveFetchIncremental_PartialListingDoesNotDelete anchors the partial
// safety: when a sub-folder cannot be listed, deletion detection is suppressed
// even though a previously seen token is missing from the (incomplete) listing.
func TestDriveFetchIncremental_PartialListingDoesNotDelete(t *testing.T) {
	f := newFakeDriveTree(t, map[string][]core.DriveFile{
		"fold-root": {
			driveFolder("fold-doomed", "doomed", "fold-root"),
			driveFile("f-a", "a.pdf", "fold-root", "100"),
		},
		"fold-doomed": {driveFile("f-deep", "deep.pdf", "fold-doomed", "300")},
	})
	f.metaNames = map[string]string{"fold-root": "团队资料"}
	f.failing["fold-doomed"] = true

	c := NewDriveConnector(core.RegionFeishuDrive)
	cfg := makeDriveConfig(f.cfg, []string{"fold-root"})

	items1, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if _, ok := fetchedByToken(items1)["f-a"]; !ok {
		t.Fatalf("first sync missing f-a: %+v", items1)
	}

	// f-a disappears while the sub-folder listing stays broken: the listing is
	// partial, so the missing token must NOT be reported as deleted.
	f.folders["fold-root"] = []core.DriveFile{driveFolder("fold-doomed", "doomed", "fold-root")}

	items2, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	if ids := deletedIDs(items2); len(ids) != 0 {
		t.Errorf("partial listing emitted IsDeleted items %v — deletion must be suppressed on a partial listing", ids)
	}
}

// TestDriveFetchIncremental_MoveFollowsNewPath anchors the move story at the
// connector level: the same external_id re-fetched after being moved between
// folders comes back with the NEW path-qualified FileName (ingestion's
// update=delete+recreate then lands it in the new folder_path).
func TestDriveFetchIncremental_MoveFollowsNewPath(t *testing.T) {
	f := newFakeDriveTree(t, map[string][]core.DriveFile{
		"fold-root": {
			driveFolder("fold-a", "目录A", "fold-root"),
		},
		"fold-a": {driveFile("f-doc", "文档.pdf", "fold-a", "100")},
	})
	f.metaNames = map[string]string{"fold-root": "团队资料"}

	c := NewDriveConnector(core.RegionFeishuDrive)
	cfg := makeDriveConfig(f.cfg, []string{"fold-root"})

	items1, cursor, err := c.FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if got := fetchedByToken(items1)["f-doc"].FileName; got != "目录A/文档.pdf" {
		t.Fatalf("first sync f-doc FileName = %q, want 目录A/文档.pdf", got)
	}

	// Move f-doc to a new folder 目录B and bump its modified time.
	f.folders["fold-root"] = []core.DriveFile{
		driveFolder("fold-a", "目录A", "fold-root"),
		driveFolder("fold-b", "目录B", "fold-root"),
	}
	f.folders["fold-b"] = []core.DriveFile{driveFile("f-doc", "文档.pdf", "fold-b", "200")}

	items2, _, err := c.FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}
	moved, ok := fetchedByToken(items2)["f-doc"]
	if !ok {
		t.Fatalf("moved file not re-fetched on second sync: %+v", items2)
	}
	if moved.FileName != "目录B/文档.pdf" {
		t.Errorf("moved file FileName = %q, want 目录B/文档.pdf (same external_id, new folder)", moved.FileName)
	}
}

// TestDriveFetchIncremental_SingleFileSelectionSkipsFolderProbes anchors the
// sub-selection fix: a selected file's kind is read off the root subtree
// listing, so no folder meta/list API is ever called with the file token —
// probing used to fail with 91202/1061002 and log API errors on every sync.
func TestDriveFetchIncremental_SingleFileSelectionSkipsFolderProbes(t *testing.T) {
	f := newFakeDriveTree(t, map[string][]core.DriveFile{
		"fold-root": {driveFile("f-doc", "文档.docx", "fold-root", "100")},
	})

	c := NewDriveConnector(core.RegionFeishuDrive)
	items, _, err := c.FetchIncremental(context.Background(), makeDriveConfig(f.cfg, []string{"fold-root:f-doc"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}

	got := fetchedByToken(items)
	if len(got) != 1 || got["f-doc"].FileName != "文档.docx" {
		t.Fatalf("got %d items (%v), want exactly f-doc with its own name", len(got), got)
	}
	if f.listCalls["f-doc"] != 0 {
		t.Errorf("folder list probed the file token %d time(s)", f.listCalls["f-doc"])
	}
	if f.metaCalls["f-doc"] != 0 {
		t.Errorf("folder meta probed the file token %d time(s)", f.metaCalls["f-doc"])
	}
}

// TestDriveFetchIncremental_SubFolderSelection pins the sub-folder semantics:
// only the selected subtree syncs, prefixed with the sub-folder's own name.
func TestDriveFetchIncremental_SubFolderSelection(t *testing.T) {
	f := newFakeDriveTree(t, map[string][]core.DriveFile{
		"fold-root": {
			driveFolder("fold-spec", "规格", "fold-root"),
			driveFile("f-root", "说明.pdf", "fold-root", "100"),
		},
		"fold-spec": {driveFile("f-spec", "详细.pdf", "fold-spec", "200")},
	})
	f.metaNames = map[string]string{"fold-spec": "规格"}

	c := NewDriveConnector(core.RegionFeishuDrive)
	items, _, err := c.FetchIncremental(
		context.Background(), makeDriveConfig(f.cfg, []string{"fold-root:fold-spec"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}

	got := fetchedByToken(items)
	if len(got) != 1 {
		t.Fatalf("got %d items (%v), want only the selected subtree", len(got), got)
	}
	if got["f-spec"].FileName != "规格/详细.pdf" {
		t.Errorf("FileName for f-spec = %q, want %q", got["f-spec"].FileName, "规格/详细.pdf")
	}
}
