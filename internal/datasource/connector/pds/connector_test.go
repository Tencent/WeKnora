package pds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestConnector_Type(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypePDS {
		t.Fatalf("Type() = %q, want %q", got, types.ConnectorTypePDS)
	}
}

// TestValidate_HappyPath: a configured fake server with one drive accepts
// the credentials.
func TestValidate_HappyPath(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(pdsDrive{DriveID: "drive-1", Name: "Drive 1"})

	c := NewConnector()
	if err := c.Validate(context.Background(), f.config("")); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if f.callCount("drive/list") != 1 {
		t.Errorf("expected 1 drive/list call, got %d", f.callCount("drive/list"))
	}
}

// TestValidate_HappyPath_AKSK: with AK/SK configured, Validate routes the
// request through the official SDK client (ACS3-HMAC-SHA256 signing). We
// verify the request shape, not the signature bytes.
func TestValidate_HappyPath_AKSK(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(pdsDrive{DriveID: "drive-1", Name: "Drive 1"})

	c := NewConnector()
	if err := c.Validate(context.Background(), f.configAKSK("")); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	h := f.lastHeaders
	if h == nil {
		t.Fatal("fake server did not capture request headers")
	}
	auth := h.Get("Authorization")
	if auth == "" {
		t.Fatal("Authorization header is empty")
	}
	// AK/SK mode must NOT send a Bearer token.
	if strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("AK/SK mode should not send Bearer token, got %q", auth)
	}
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json prefix", ct)
	}
}

// TestValidate_RefreshTokenExchange: a config with only a refresh_token
// exchanges it via /v2/oauth/token before the first API call.
func TestValidate_RefreshTokenExchange(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(pdsDrive{DriveID: "drive-1", Name: "Drive 1"})

	c := NewConnector()
	if err := c.Validate(context.Background(), f.configRefresh("")); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if f.tokenExchanges != 1 {
		t.Errorf("expected 1 oauth/token exchange, got %d", f.tokenExchanges)
	}
	// The API call must carry the EXCHANGED token, not the refresh token.
	if got := f.lastHeaders.Get("Authorization"); got != "Bearer exchanged-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer exchanged-token")
	}
}

// TestClient_TokenRefreshOn401: when the server rejects the cached access
// token with a 401, the client re-exchanges the refresh_token and retries
// once — PDS access tokens expire (typically ~2h) mid-sync.
func TestClient_TokenRefreshOn401(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(pdsDrive{DriveID: "drive-1", Name: "Drive 1"})
	f.forceUnauthorized() // first API call returns 401

	c := NewConnector()
	if err := c.Validate(context.Background(), f.configRefresh("")); err != nil {
		t.Fatalf("Validate should recover from 401 via refresh: %v", err)
	}
	if f.tokenExchanges != 2 {
		t.Errorf("expected 2 oauth/token exchanges (initial + refresh), got %d", f.tokenExchanges)
	}
	if f.callCount("drive/list") != 2 {
		t.Errorf("expected 2 drive/list attempts (401 + retry), got %d", f.callCount("drive/list"))
	}
}

// TestValidate_NoDrives: a token with zero drives still validates (logs a
// warning, but does not error).
func TestValidate_NoDrives(t *testing.T) {
	f := newFakePDS(t)
	c := NewConnector()
	if err := c.Validate(context.Background(), f.config("")); err != nil {
		t.Fatalf("Validate with zero drives should not error: %v", err)
	}
}

// TestValidate_RejectsMissingDomain: parseConfig rejects empty domain_id.
func TestValidate_RejectsMissingDomain(t *testing.T) {
	f := newFakePDS(t)
	cfg := f.config("")
	delete(cfg.Credentials, "domain_id")
	c := NewConnector()
	err := c.Validate(context.Background(), cfg)
	if err == nil {
		t.Fatalf("expected error for missing domain_id")
	}
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got %v", err)
	}
}

// TestValidate_RejectsNoAuth: parseConfig rejects an empty credentials map.
func TestValidate_RejectsNoAuth(t *testing.T) {
	f := newFakePDS(t)
	cfg := f.config("")
	cfg.Credentials = map[string]interface{}{
		"endpoint":  f.server.URL,
		"domain_id": "test-domain",
	}
	c := NewConnector()
	if err := c.Validate(context.Background(), cfg); err == nil {
		t.Fatalf("expected error for missing auth")
	}
}

// TestListResources_RootLevel_ReturnsDrives: parentID == "" returns the
// configured drives as Type=="drive" with HasChildren=true, keyed
// "<driveID>:" so children (whose ParentID normalizes to the same token)
// attach to them in the frontend tree.
func TestListResources_RootLevel_ReturnsDrives(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(
		pdsDrive{DriveID: "d1", Name: "Drive One"},
		pdsDrive{DriveID: "d2", Name: "Drive Two"},
	)
	c := NewConnector()
	resources, err := c.ListResources(context.Background(), f.config(""), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	for _, r := range resources {
		if r.Type != "drive" {
			t.Errorf("drive %s: Type = %q, want \"drive\"", r.ExternalID, r.Type)
		}
		if !r.HasChildren {
			t.Errorf("drive %s: HasChildren should be true", r.ExternalID)
		}
	}
	if resources[0].ExternalID != "d1:" || resources[1].ExternalID != "d2:" {
		t.Errorf("drive ExternalIDs = %q, %q; want \"d1:\", \"d2:\"",
			resources[0].ExternalID, resources[1].ExternalID)
	}
}

// TestListResources_FolderLevel_ReturnsChildren: expanding a drive node
// ("<driveID>:" — and the bare "<driveID>" legacy form) returns its
// top-level entries, and their ParentID points back at the drive node so
// the frontend tree attaches them correctly.
func TestListResources_FolderLevel_ReturnsChildren(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(pdsDrive{DriveID: "d1", Name: "Drive 1"})
	f.setFiles("root",
		pdsFile{FileID: "f1", Name: "report.pdf", Type: "file", ParentID: "root"},
		pdsFile{FileID: "f2", Name: "subdir", Type: "folder", ParentID: "root"},
	)
	f.setFiles("f2",
		pdsFile{FileID: "f2-1", Name: "inner.txt", Type: "file", ParentID: "f2"},
	)
	c := NewConnector()

	for _, parent := range []string{"d1:", "d1"} {
		children, err := c.ListResources(context.Background(), f.config("d1"), parent)
		if err != nil {
			t.Fatalf("ListResources(%q): %v", parent, err)
		}
		if len(children) != 2 {
			t.Fatalf("parent %q: expected 2 children, got %d", parent, len(children))
		}
		for _, child := range children {
			if child.ParentID != "d1:" {
				t.Errorf("parent %q: child %s ParentID = %q, want \"d1:\"",
					parent, child.ExternalID, child.ParentID)
			}
		}
	}

	// Expanding the folder uses "<driveID>:<folderID>".
	grandchildren, err := c.ListResources(context.Background(), f.config("d1"), "d1:f2")
	if err != nil {
		t.Fatalf("ListResources(d1:f2): %v", err)
	}
	if len(grandchildren) != 1 || grandchildren[0].ParentID != "d1:f2" {
		t.Fatalf("grandchildren = %+v, want one child with ParentID d1:f2", grandchildren)
	}
}

// TestResolveResourceAncestors_TopLevel: a top-level drive selection
// (resourceID "<driveID>:" or bare "<driveID>") returns just the drive
// resourceID.
func TestResolveResourceAncestors_TopLevel(t *testing.T) {
	f := newFakePDS(t)
	f.setDrives(pdsDrive{DriveID: "d1", Name: "Drive 1"})
	c := NewConnector()
	for _, rid := range []string{"d1:", "d1"} {
		ancestors, err := c.ResolveResourceAncestors(context.Background(), f.config("d1"), []string{rid})
		if err != nil {
			t.Fatalf("ResolveResourceAncestors(%q): %v", rid, err)
		}
		if len(ancestors) != 1 || ancestors[0] != "d1:" {
			t.Errorf("rid %q: expected [\"d1:\"], got %v", rid, ancestors)
		}
	}
}

// TestResolveResourceAncestors_DeepSelection: a deeply-nested file
// selection returns the drive root plus every intermediate folder
// resourceID so the picker can reveal it.
func TestResolveResourceAncestors_DeepSelection(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root",
		pdsFile{FileID: "A", Name: "A", Type: "folder", ParentID: "root"},
	)
	f.setFiles("A",
		pdsFile{FileID: "B", Name: "B", Type: "folder", ParentID: "A"},
	)
	f.setFiles("B",
		pdsFile{FileID: "file.pdf", Name: "file.pdf", Type: "file", ParentID: "B"},
	)
	c := NewConnector()
	ancestors, err := c.ResolveResourceAncestors(
		context.Background(),
		f.config("d1"),
		[]string{"d1:file.pdf"},
	)
	if err != nil {
		t.Fatalf("ResolveResourceAncestors: %v", err)
	}
	// Expect the chain (drive root + intermediate folders), but not the
	// selected resource itself.
	expected := map[string]bool{"d1:": false, "d1:A": false, "d1:B": false}
	for _, a := range ancestors {
		if _, ok := expected[a]; ok {
			expected[a] = true
		}
	}
	for k, seen := range expected {
		if !seen {
			t.Errorf("missing ancestor %q in %v", k, ancestors)
		}
	}
	for _, a := range ancestors {
		if a == "d1:file.pdf" {
			t.Errorf("selected resourceID should not appear in ancestors: %v", ancestors)
		}
	}
}

// TestFetchAll_RecursiveWalksTree: FetchAll descends into folders and
// emits one FetchedItem per file with non-empty Content.
func TestFetchAll_RecursiveWalksTree(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root",
		pdsFile{FileID: "a", Name: "a.txt", Type: "file", ParentID: "root", UpdatedAt: time.Now()},
		pdsFile{FileID: "dir", Name: "dir", Type: "folder", ParentID: "root"},
	)
	f.setFiles("dir",
		pdsFile{FileID: "b", Name: "b.md", Type: "file", ParentID: "dir", UpdatedAt: time.Now()},
	)
	f.setDownload("a", []byte("aaa"), "text/plain")
	f.setDownload("b", []byte("bbb"), "text/markdown")

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), f.config("d1"), nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d (%s)", len(items), describeItems(items))
	}
	a := mustFindItem(t, items, pdsFileExternalID("d1", "a"))
	if string(a.Content) != "aaa" {
		t.Errorf("a.Content = %q, want \"aaa\"", a.Content)
	}
	b := mustFindItem(t, items, pdsFileExternalID("d1", "b"))
	if string(b.Content) != "bbb" {
		t.Errorf("b.Content = %q, want \"bbb\"", b.Content)
	}
}

// TestFetchAll_PopulatesFolderPathMetadata: nested files carry pds_path /
// pds_folder metadata so the KB folder tree can group them; drive-root
// files carry neither.
func TestFetchAll_PopulatesFolderPathMetadata(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root",
		pdsFile{FileID: "top", Name: "top.txt", Type: "file", ParentID: "root", UpdatedAt: time.Now()},
		pdsFile{FileID: "02", Name: "02", Type: "folder", ParentID: "root"},
	)
	f.setFiles("02",
		pdsFile{FileID: "pg", Name: "pgsql.md", Type: "file", ParentID: "02", UpdatedAt: time.Now()},
	)
	f.setDownload("top", []byte("top"), "text/plain")
	f.setDownload("pg", []byte("pg"), "text/markdown")

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), f.config("d1"), nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	pg := mustFindItem(t, items, pdsFileExternalID("d1", "pg"))
	if pg.Metadata["pds_path"] != "/02/pgsql.md" {
		t.Errorf("pds_path = %q, want \"/02/pgsql.md\"", pg.Metadata["pds_path"])
	}
	if pg.Metadata["pds_folder"] != "/02" {
		t.Errorf("pds_folder = %q, want \"/02\"", pg.Metadata["pds_folder"])
	}
	if pg.Metadata["channel"] != "pds" {
		t.Errorf("channel = %q, want \"pds\"", pg.Metadata["channel"])
	}
	// The walker stamps the full readable path into FileName so ingest
	// preserves the folder structure.
	if pg.FileName != "/02/pgsql.md" {
		t.Errorf("FileName = %q, want \"/02/pgsql.md\"", pg.FileName)
	}

	top := mustFindItem(t, items, pdsFileExternalID("d1", "top"))
	if _, ok := top.Metadata["pds_folder"]; ok {
		t.Errorf("drive-root file should not carry pds_folder: %v", top.Metadata)
	}
}

// TestFetchAll_MissingDriveID: without any drive selection the sync fails
// with a clear, actionable error instead of syncing nothing.
func TestFetchAll_MissingDriveID(t *testing.T) {
	f := newFakePDS(t)
	cfg := f.config("") // empty drive_id, no resource_ids
	c := NewConnector()
	if _, err := c.FetchAll(context.Background(), cfg, nil); err == nil {
		t.Fatalf("expected error for missing drive_id")
	}
}

// TestFetchAll_DriveIDFromResourceIDs: when Settings.drive_id is blank,
// the picker selection in ResourceIDs determines the drive — both the
// canonical "<driveID>:" form and a bare "<driveID>".
func TestFetchAll_DriveIDFromResourceIDs(t *testing.T) {
	for _, rid := range []string{"d1:", "d1"} {
		f := newFakePDS(t)
		f.setFiles("root",
			pdsFile{FileID: "a", Name: "a.txt", Type: "file", ParentID: "root", UpdatedAt: time.Now()},
		)
		f.setDownload("a", []byte("aaa"), "text/plain")
		cfg := f.config("")
		cfg.ResourceIDs = []string{rid}

		c := NewConnector()
		items, err := c.FetchAll(context.Background(), cfg, nil)
		if err != nil {
			t.Fatalf("rid %q: FetchAll: %v", rid, err)
		}
		if len(items) != 1 {
			t.Fatalf("rid %q: expected 1 item, got %d", rid, len(items))
		}
	}
}

// TestFetchAll_FileTypeFilter: the file_types whitelist filters by
// extension.
func TestFetchAll_FileTypeFilter(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root",
		pdsFile{FileID: "a", Name: "a.pdf", Type: "file", ParentID: "root", UpdatedAt: time.Now()},
		pdsFile{FileID: "b", Name: "b.exe", Type: "file", ParentID: "root", UpdatedAt: time.Now()},
	)
	f.setDownload("a", []byte("pdf"), "application/pdf")
	f.setDownload("b", []byte("exe"), "application/octet-stream")

	cfg := f.config("d1")
	cfg.Settings = map[string]interface{}{
		"drive_id":   "d1",
		"file_types": []string{".pdf"},
	}
	c := NewConnector()
	items, err := c.FetchAll(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != pdsFileExternalID("d1", "a") {
		t.Fatalf("expected only a.pdf, got %s", describeItems(items))
	}
}

// TestFetchIncremental_FirstRunBootstraps: with no prior cursor the sync
// does a full walk, then captures the latest delta cursor so subsequent
// syncs are truly incremental.
func TestFetchIncremental_FirstRunBootstraps(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root", pdsFile{FileID: "a", Name: "a.txt", Type: "file", ParentID: "root", UpdatedAt: time.Now()})
	f.setDownload("a", []byte("hello"), "text/plain")
	f.setLastCursor("cursor-1")

	c := NewConnector()
	items, cursor, err := c.FetchIncremental(context.Background(), f.config("d1"), nil)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item, got %d", len(items))
	}
	if f.callCount("file/get_last_cursor") != 1 {
		t.Errorf("expected 1 file/get_last_cursor call on first run, got %d",
			f.callCount("file/get_last_cursor"))
	}
	if cursor == nil {
		t.Fatalf("expected non-nil cursor")
	}
	var pc pdsCursor
	b, _ := json.Marshal(cursor.ConnectorCursor)
	_ = json.Unmarshal(b, &pc)
	if pc.ListDeltaCursor != "cursor-1" {
		t.Errorf("expected cursor to record \"cursor-1\", got %q", pc.ListDeltaCursor)
	}
	if _, ok := pc.DriveFiles["a"]; !ok {
		t.Errorf("expected DriveFiles to contain \"a\"")
	}
	// Ordering regression: the delta cursor must be captured BEFORE the
	// full walk starts. The reverse order loses files uploaded during the
	// walk window (missed by the walk, and before the persisted cursor,
	// so no later list_delta ever returns them).
	cursorIdx, listIdx := f.actionIndex("file/get_last_cursor"), f.actionIndex("file/list")
	if cursorIdx < 0 || listIdx < 0 {
		t.Fatalf("expected both get_last_cursor and file/list calls (got %d, %d)", cursorIdx, listIdx)
	}
	if cursorIdx > listIdx {
		t.Errorf("get_last_cursor (idx %d) must precede the first file/list (idx %d)", cursorIdx, listIdx)
	}
}

// TestFetchIncremental_SubsequentRunUsesListDelta: with a persisted cursor
// AND a non-empty drive_files baseline, the sync resumes from the delta
// feed — no re-walk, no re-handshake. Crucially, files that are merely
// UNCHANGED (in the baseline but absent from the delta page) must NOT be
// tombstoned: the delta feed only returns changed items.
func TestFetchIncremental_SubsequentRunUsesListDelta(t *testing.T) {
	f := newFakePDS(t)
	f.setDelta("saved-cursor", []pdsDeltaItem{
		{File: pdsFile{FileID: "b", Name: "b.txt", Type: "file", UpdatedAt: time.Now()}, Op: "update"},
	}, false)
	f.setDownload("b", []byte("hello B"), "text/plain")

	c := NewConnector()
	cursor := &types.SyncCursor{
		LastSyncTime: time.Now().Add(-time.Hour),
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "saved-cursor",
			"drive_files": map[string]string{
				"a": time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	items, _, err := c.FetchIncremental(context.Background(), f.config("d1"), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if _, ok := findItem(items, pdsFileExternalID("d1", "b")); !ok {
		t.Errorf("expected item for \"b\" in %s", describeItems(items))
	}
	// "a" was unchanged — no tombstone, no re-fetch.
	if it, ok := findItem(items, pdsFileExternalID("d1", "a")); ok {
		t.Errorf("unchanged file \"a\" must not be emitted (got deleted=%v): %s",
			it.IsDeleted, describeItems(items))
	}
	if f.callCount("file/list") != 0 {
		t.Errorf("expected 0 file/list calls (no re-walk), got %d", f.callCount("file/list"))
	}
	if f.callCount("file/get_last_cursor") != 0 {
		t.Errorf("expected 0 file/get_last_cursor calls on resumed run, got %d",
			f.callCount("file/get_last_cursor"))
	}
}

// TestFetchIncremental_DeletionViaOp: deletions are detected via the delta
// item's op == "delete" — the ONLY reliable signal in a change feed.
func TestFetchIncremental_DeletionViaOp(t *testing.T) {
	f := newFakePDS(t)
	f.setDelta("cursor-X", []pdsDeltaItem{
		{File: pdsFile{FileID: "present", Name: "present.txt", Type: "file", UpdatedAt: time.Now()}, Op: "update"},
		{File: pdsFile{FileID: "gone", Name: "gone.txt", Type: "file"}, Op: "delete"},
	}, false)
	f.setDownload("present", []byte("present"), "text/plain")

	c := NewConnector()
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "cursor-X",
			"drive_files": map[string]string{
				"present":   time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
				"gone":      time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
				"unchanged": time.Now().Add(-3 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	items, newCursor, err := c.FetchIncremental(context.Background(), f.config("d1"), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	// "gone" → tombstone.
	tombstone, ok := findItem(items, pdsFileExternalID("d1", "gone"))
	if !ok || !tombstone.IsDeleted {
		t.Fatalf("expected IsDeleted tombstone for \"gone\" in %s", describeItems(items))
	}
	// "present" → re-fetched content.
	if _, ok := findItem(items, pdsFileExternalID("d1", "present")); !ok {
		t.Errorf("expected item for \"present\" in %s", describeItems(items))
	}
	// "unchanged" → nothing at all.
	if it, ok := findItem(items, pdsFileExternalID("d1", "unchanged")); ok {
		t.Errorf("unchanged file must not be emitted (got deleted=%v)", it.IsDeleted)
	}
	// Baseline bookkeeping: "gone" dropped, "unchanged" kept.
	var pc pdsCursor
	b, _ := json.Marshal(newCursor.ConnectorCursor)
	_ = json.Unmarshal(b, &pc)
	if _, ok := pc.DriveFiles["gone"]; ok {
		t.Errorf("deleted file should be dropped from DriveFiles")
	}
	if _, ok := pc.DriveFiles["unchanged"]; !ok {
		t.Errorf("unchanged file should remain in DriveFiles")
	}
}

// TestFetchIncremental_FolderPathResolvedViaGetFile: a file created inside
// a folder must keep its folder path on the incremental path. Delta pages
// carry no folder context, so the connector walks up the parent chain via
// file/get — without this the document lands in the KB root (the reported
// regression: a file uploaded into "02" synced to the repository root).
func TestFetchIncremental_FolderPathResolvedViaGetFile(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root",
		pdsFile{FileID: "02", Name: "02", Type: "folder", ParentID: "root"},
	)
	f.setFiles("02",
		pdsFile{FileID: "old", Name: "old.txt", Type: "file", ParentID: "02", UpdatedAt: time.Now()},
	)
	// Delta page contains ONLY the new file — no folder entries, no
	// name_path — exactly what the real feed returns for an upload.
	f.setDelta("cursor-X", []pdsDeltaItem{
		{
			File: pdsFile{FileID: "new", Name: "new.pdf", Type: "file", ParentID: "02", UpdatedAt: time.Now()},
			Op:   "create",
		},
	}, false)
	f.setDownload("new", []byte("new body"), "application/pdf")

	c := NewConnector()
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "cursor-X",
			"drive_files": map[string]string{
				"02":  time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
				"old": time.Now().Add(-2 * time.Hour).Format(time.RFC3339),
			},
		},
	}
	items, _, err := c.FetchIncremental(context.Background(), f.config("d1"), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	item := mustFindItem(t, items, pdsFileExternalID("d1", "new"))
	if item.FileName != "/02/new.pdf" {
		t.Errorf("FileName = %q, want \"/02/new.pdf\" (file must keep its folder)", item.FileName)
	}
	if item.Metadata["pds_path"] != "/02/new.pdf" {
		t.Errorf("pds_path = %q, want \"/02/new.pdf\"", item.Metadata["pds_path"])
	}
	if item.Metadata["pds_folder"] != "/02" {
		t.Errorf("pds_folder = %q, want \"/02\"", item.Metadata["pds_folder"])
	}
	if f.callCount("file/get") == 0 {
		t.Errorf("expected at least one file/get call to resolve the parent chain")
	}
}

// TestFetchIncremental_CursorExpired_Recovers: an InvalidCursor on the
// first list_delta call triggers a re-handshake and a successful retry.
func TestFetchIncremental_CursorExpired_Recovers(t *testing.T) {
	f := newFakePDS(t)
	f.setLastCursor("fresh-cursor")
	f.setDelta("fresh-cursor", []pdsDeltaItem{
		{File: pdsFile{FileID: "x", Name: "x.txt", Type: "file", UpdatedAt: time.Now()}, Op: "create"},
	}, false)
	f.setDownload("x", []byte("x body"), "text/plain")
	f.expireNextDelta() // next list_delta returns InvalidCursor, then the retry succeeds

	c := NewConnector()
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "stale-cursor",
			"drive_files":       map[string]string{"prior": time.Now().Format(time.RFC3339)},
		},
	}
	items, _, err := c.FetchIncremental(context.Background(), f.config("d1"), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental should recover from cursor expiry: %v", err)
	}
	if _, ok := findItem(items, pdsFileExternalID("d1", "x")); !ok {
		t.Errorf("expected item for \"x\" after recovery in %s", describeItems(items))
	}
	if f.callCount("file/get_last_cursor") != 1 {
		t.Errorf("expected 1 re-handshake, got %d", f.callCount("file/get_last_cursor"))
	}
}

// TestFetchIncremental_StaleHandshakeReboots: when the persisted cursor
// has list_delta_cursor set but drive_files empty (the "stale handshake"
// state), the sync re-bootstraps via a full walk instead of running
// list_delta(now), which would return nothing — the old bug returned zero
// items forever.
func TestFetchIncremental_StaleHandshakeReboots(t *testing.T) {
	f := newFakePDS(t)
	f.setFiles("root",
		pdsFile{FileID: "s1", Name: "s1.txt", Type: "file", ParentID: "root", UpdatedAt: time.Now()},
	)
	f.setDownload("s1", []byte("s1 body"), "text/plain")
	f.setLastCursor("new-cursor")

	c := NewConnector()
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "old-handshake-cursor",
			"drive_files":       map[string]string{}, // empty -> stale handshake
		},
	}
	items, _, err := c.FetchIncremental(context.Background(), f.config("d1"), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item from re-bootstrap, got %d (%s)", len(items), describeItems(items))
	}
	if f.callCount("file/list_delta") != 0 {
		t.Errorf("stale handshake must not call list_delta, got %d calls",
			f.callCount("file/list_delta"))
	}
}

// testStreamHandler is a scriptable datasource.StreamHandler.
type testStreamHandler struct {
	emitFn       func(ctx context.Context, item types.FetchedItem) error
	checkpointFn func(ctx context.Context, cursor *types.SyncCursor) error
}

func (h *testStreamHandler) Emit(ctx context.Context, item types.FetchedItem) error {
	if h.emitFn != nil {
		return h.emitFn(ctx, item)
	}
	return nil
}

func (h *testStreamHandler) Checkpoint(ctx context.Context, cursor *types.SyncCursor) error {
	if h.checkpointFn != nil {
		return h.checkpointFn(ctx, cursor)
	}
	return nil
}

// TestFetchStream_PaginatesAndCheckpoints: the delta loop follows
// has_more/next_cursor across pages, emits every changed item, and
// checkpoints at page boundaries. Unchanged baseline files are not
// tombstoned.
func TestFetchStream_PaginatesAndCheckpoints(t *testing.T) {
	f := newFakePDS(t)
	now := time.Now()
	f.setDelta("c0", []pdsDeltaItem{
		{File: pdsFile{FileID: "p1", Name: "p1.txt", Type: "file", UpdatedAt: now}, Op: "create"},
	}, true)
	f.setDelta("c0_next", []pdsDeltaItem{
		{File: pdsFile{FileID: "p2", Name: "p2.txt", Type: "file", UpdatedAt: now}, Op: "update"},
	}, false)
	f.setDownload("p1", []byte("p1 body"), "text/plain")
	f.setDownload("p2", []byte("p2 body"), "text/plain")

	emitted := []string{}
	checkpointCount := 0
	h := &testStreamHandler{
		emitFn: func(_ context.Context, item types.FetchedItem) error {
			emitted = append(emitted, item.ExternalID)
			return nil
		},
		checkpointFn: func(_ context.Context, _ *types.SyncCursor) error {
			checkpointCount++
			return nil
		},
	}
	c := NewConnector()
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "c0",
			"drive_files": map[string]string{
				"prior": time.Now().Add(-time.Hour).Format(time.RFC3339),
			},
		},
	}
	_, err := c.FetchStream(context.Background(), f.config("d1"), cursor, h)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	hasP1, hasP2 := false, false
	for _, id := range emitted {
		switch id {
		case pdsFileExternalID("d1", "p1"):
			hasP1 = true
		case pdsFileExternalID("d1", "p2"):
			hasP2 = true
		case pdsFileExternalID("d1", "prior"):
			t.Errorf("unchanged baseline file \"prior\" must not be emitted")
		}
	}
	if !hasP1 || !hasP2 {
		t.Errorf("expected emits for p1 and p2, got %v", emitted)
	}
	if checkpointCount < 1 {
		t.Errorf("expected at least 1 page-boundary checkpoint, got %d", checkpointCount)
	}
}

// TestFetchStream_BootstrapCheckpoints: during a first-run full walk the
// connector checkpoints every checkpointEveryFiles files so a timed-out
// bootstrap resumes instead of restarting.
func TestFetchStream_BootstrapCheckpoints(t *testing.T) {
	f := newFakePDS(t)
	rootFiles := make([]pdsFile, 0, checkpointEveryFiles+1)
	for i := 0; i <= checkpointEveryFiles; i++ {
		id := fmt.Sprintf("file-%02d", i)
		rootFiles = append(rootFiles, pdsFile{
			FileID: id, Name: id + ".txt", Type: "file", ParentID: "root", UpdatedAt: time.Now(),
		})
		f.setDownload(id, []byte("body "+id), "text/plain")
	}
	f.setFiles("root", rootFiles...)
	f.setLastCursor("boot-cursor")

	emitted := 0
	checkpointCount := 0
	h := &testStreamHandler{
		emitFn: func(_ context.Context, _ types.FetchedItem) error {
			emitted++
			return nil
		},
		checkpointFn: func(_ context.Context, _ *types.SyncCursor) error {
			checkpointCount++
			return nil
		},
	}
	c := NewConnector()
	if _, err := c.FetchStream(context.Background(), f.config("d1"), nil, h); err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if emitted != checkpointEveryFiles+1 {
		t.Errorf("expected %d emits, got %d", checkpointEveryFiles+1, emitted)
	}
	if checkpointCount < 1 {
		t.Errorf("expected at least 1 bootstrap checkpoint after %d files, got %d",
			checkpointEveryFiles, checkpointCount)
	}
}

// TestFetchStream_StaleHandshakeReboots: FetchStream variant of the stale
// handshake regression — full walk, then a fresh delta cursor.
func TestFetchStream_StaleHandshakeReboots(t *testing.T) {
	f := newFakePDS(t)
	now := time.Now()
	f.setFiles("root",
		pdsFile{FileID: "s1", Name: "s1.txt", Type: "file", ParentID: "root", UpdatedAt: now},
	)
	f.setDownload("s1", []byte("s1 body"), "text/plain")
	f.setLastCursor("new-cursor")

	emitted := []string{}
	h := &testStreamHandler{
		emitFn: func(_ context.Context, item types.FetchedItem) error {
			emitted = append(emitted, item.ExternalID)
			return nil
		},
	}
	c := NewConnector()
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"list_delta_cursor": "old-handshake-cursor",
			"drive_files":       map[string]string{},
		},
	}
	newCursor, err := c.FetchStream(context.Background(), f.config("d1"), cursor, h)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if len(emitted) != 1 || emitted[0] != pdsFileExternalID("d1", "s1") {
		t.Fatalf("expected [s1], got %v", emitted)
	}
	var pc pdsCursor
	b, _ := json.Marshal(newCursor.ConnectorCursor)
	_ = json.Unmarshal(b, &pc)
	if pc.ListDeltaCursor != "new-cursor" {
		t.Errorf("expected re-bootstrapped cursor \"new-cursor\", got %q", pc.ListDeltaCursor)
	}
}
