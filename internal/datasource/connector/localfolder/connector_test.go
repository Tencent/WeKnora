package localfolder

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// settled is an mtime age safely outside the quiet period.
const settled = time.Minute

// newVault creates a temporary folder allowed by AllowedRootsEnv and a data
// source config pointing at it.
func newVault(t *testing.T) (string, *types.DataSourceConfig) {
	t.Helper()
	root := t.TempDir()
	t.Setenv(AllowedRootsEnv, root)
	return root, &types.DataSourceConfig{
		Type:     types.ConnectorTypeLocalFolder,
		Settings: map[string]interface{}{"root_path": root},
	}
}

// writeFile writes rel under root and backdates its mtime by age.
func writeFile(t *testing.T, root, rel, content string, age time.Duration) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	mtime := time.Now().Add(-age)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// syncFolder runs an incremental sync with a fresh connector and round-trips
// the returned cursor through its persisted JSON form, as
// DataSource.LastSyncCursor does between syncs and across restarts.
func syncFolder(
	t *testing.T, cfg *types.DataSourceConfig, cursor *types.SyncCursor,
) (map[string]types.FetchedItem, *types.SyncCursor) {
	t.Helper()
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	raw, err := next.ToJSON()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := (&types.DataSource{LastSyncCursor: raw}).ParseSyncCursor()
	if err != nil {
		t.Fatal(err)
	}
	return byExternalID(t, items), restored
}

func byExternalID(t *testing.T, items []types.FetchedItem) map[string]types.FetchedItem {
	t.Helper()
	out := make(map[string]types.FetchedItem, len(items))
	for _, item := range items {
		if _, dup := out[item.ExternalID]; dup {
			t.Fatalf("item %s emitted twice", item.ExternalID)
		}
		out[item.ExternalID] = item
	}
	return out
}

func assertItems(t *testing.T, items map[string]types.FetchedItem, want ...string) {
	t.Helper()
	got := slices.Sorted(maps.Keys(items))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("items = %v, want %v", got, want)
	}
}

func TestFirstSyncEmitsIncludedFilesOnly(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "notes/MPC.md", "# MPC", time.Hour)
	writeFile(t, root, "index.md", "# Index", time.Hour)
	writeFile(t, root, "attachments/diagram.png", "png", time.Hour)
	writeFile(t, root, ".obsidian/workspace.md", "{}", time.Hour)
	writeFile(t, root, ".git/notes.md", "git", time.Hour)
	writeFile(t, root, ".trash/old.md", "old", time.Hour)

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:index.md", "local_folder:notes/MPC.md")
	item := items["local_folder:notes/MPC.md"]
	if string(item.Content) != "# MPC" || item.FileName != "notes/MPC.md" || !item.UpdateInPlace || item.IsDeleted {
		t.Fatalf("unexpected item: %+v", item)
	}
}

func TestIncrementalSyncClassifiesNewChangedDeletedAndUnchangedFiles(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "keep.md", "unchanged", time.Hour)
	writeFile(t, root, "edit.md", "before", time.Hour)
	writeFile(t, root, "gone.md", "bye", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)

	writeFile(t, root, "edit.md", "after!", settled) // same size, newer mtime
	writeFile(t, root, "new.md", "hello", settled)
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}

	items, _ := syncFolder(t, cfg, cursor)

	assertItems(t, items, "local_folder:edit.md", "local_folder:new.md", "local_folder:gone.md")
	if got := string(items["local_folder:edit.md"].Content); got != "after!" {
		t.Fatalf("edited content = %q", got)
	}
	if !items["local_folder:gone.md"].IsDeleted || items["local_folder:new.md"].IsDeleted {
		t.Fatalf("deletion flags wrong: %+v", items)
	}
}

func TestTouchedFileWithSameContentIsNotReingested(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "note.md", "same", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)

	writeFile(t, root, "note.md", "same", settled)
	items, cursor := syncFolder(t, cfg, cursor)
	assertItems(t, items)

	items, _ = syncFolder(t, cfg, cursor)
	assertItems(t, items)
}

func TestIncrementalStateSurvivesRestart(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "a.md", "one", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)
	persisted, err := cursor.ToJSON()
	if err != nil {
		t.Fatal(err)
	}

	// A new process only has the stored cursor JSON and a new connector.
	restored, err := (&types.DataSource{LastSyncCursor: persisted}).ParseSyncCursor()
	if err != nil {
		t.Fatal(err)
	}
	items, _ := syncFolder(t, cfg, restored)
	assertItems(t, items)

	writeFile(t, root, "a.md", "two", settled)
	items, _ = syncFolder(t, cfg, restored)
	assertItems(t, items, "local_folder:a.md")
}

func TestSameFileNameInDifferentFoldersHasDistinctIdentity(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "projects/a/README.md", "a", time.Hour)
	writeFile(t, root, "projects/b/README.md", "b", time.Hour)

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:projects/a/README.md", "local_folder:projects/b/README.md")
}

func TestCustomIncludeAndExcludePatterns(t *testing.T) {
	root, cfg := newVault(t)
	cfg.Settings["include"] = "**/*.md\n**/*.txt"
	cfg.Settings["exclude"] = []interface{}{"drafts/**", "**/*.tmp.md"}
	writeFile(t, root, "a.md", "a", time.Hour)
	writeFile(t, root, "notes/b.txt", "b", time.Hour)
	writeFile(t, root, "c.pdf", "c", time.Hour)
	writeFile(t, root, "drafts/d.md", "d", time.Hour)
	writeFile(t, root, "x/e.tmp.md", "e", time.Hour)
	writeFile(t, root, ".obsidian/f.md", "defaults are replaced", time.Hour)

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:a.md", "local_folder:notes/b.txt", "local_folder:.obsidian/f.md")
}

func TestInvalidPatternIsRejected(t *testing.T) {
	_, cfg := newVault(t)
	cfg.Settings["include"] = "notes/[.md"

	if err := NewConnector().Validate(context.Background(), cfg); err == nil {
		t.Fatal("Validate accepted an invalid pattern")
	}
}

func TestQuietPeriodDefersRecentlyModifiedFiles(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "settled.md", "old", time.Hour)
	writeFile(t, root, "typing.md", "dra", 0)
	items, cursor := syncFolder(t, cfg, nil)
	assertItems(t, items, "local_folder:settled.md")

	writeFile(t, root, "settled.md", "being edited", 0)
	items, cursor = syncFolder(t, cfg, cursor)
	assertItems(t, items) // neither updated nor reported deleted while settling

	writeFile(t, root, "settled.md", "being edited", settled)
	writeFile(t, root, "typing.md", "draft done", settled)
	items, _ = syncFolder(t, cfg, cursor)
	assertItems(t, items, "local_folder:settled.md", "local_folder:typing.md")
}

func TestManualSyncSkipsQuietPeriod(t *testing.T) {
	root, cfg := newVault(t)
	cfg.ManualTrigger = true
	writeFile(t, root, "just-saved.md", "fresh", 0)

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:just-saved.md")
}

func TestDefaultIncludeCoversDocumentsButNotImages(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "note.md", "md", time.Hour)
	writeFile(t, root, "papers/a.pdf", "pdf", time.Hour)
	writeFile(t, root, "report.docx", "docx", time.Hour)
	writeFile(t, root, "attachments/diagram.png", "png", time.Hour)
	writeFile(t, root, "voice.m4a", "audio", time.Hour)
	writeFile(t, root, "data.json", "{}", time.Hour)

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:note.md", "local_folder:papers/a.pdf", "local_folder:report.docx")
}

func TestPatternsMatchExtensionsCaseInsensitively(t *testing.T) {
	root, cfg := newVault(t)
	cfg.Settings["exclude"] = "**/*.tmp"
	writeFile(t, root, "REPORT.PDF", "pdf", time.Hour)
	writeFile(t, root, "notes/Idea.Md", "md", time.Hour)
	writeFile(t, root, "Draft.TMP", "tmp", time.Hour)
	writeFile(t, root, "img/Photo.PNG", "png", time.Hour)

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:REPORT.PDF", "local_folder:notes/Idea.Md")
}

func TestUnsupportedExtensionsAreSkippedEvenWhenIncluded(t *testing.T) {
	root, cfg := newVault(t)
	cfg.Settings["include"] = "**/*"
	writeFile(t, root, "note.md", "md", time.Hour)
	writeFile(t, root, "board.canvas", "{}", time.Hour)
	writeFile(t, root, "sketch.excalidraw", "{}", time.Hour)
	writeFile(t, root, "noext", "x", time.Hour)
	writeFile(t, root, "SCAN.PDF", "pdf", time.Hour)
	writeFile(t, root, "img/photo.png", "png", time.Hour)
	writeFile(t, root, "map.xmind", "xmind", time.Hour)

	items, _ := syncFolder(t, cfg, nil)

	for _, item := range items {
		if item.Metadata["error"] != "" {
			t.Fatalf("skipped formats must not surface as failures: %+v", item)
		}
	}
	assertItems(t, items, "local_folder:note.md", "local_folder:SCAN.PDF", "local_folder:img/photo.png",
		"local_folder:map.xmind")
}

func TestSymlinksAreNotFollowed(t *testing.T) {
	root, cfg := newVault(t)
	outside := t.TempDir()
	writeFile(t, outside, "secret.md", "secret", time.Hour)
	writeFile(t, root, "note.md", "ok", time.Hour)
	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked-dir")); err != nil {
		t.Fatal(err)
	}

	items, _ := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:note.md")
}

func TestRootMustResolveInsideAllowedRoots(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	vault := filepath.Join(allowed, "vault")
	if err := os.Mkdir(vault, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, env, root string
		ok              bool
	}{
		{"inside an allowed root", allowed, vault, true},
		{"the allowed root itself", allowed, allowed, true},
		{"disabled when the allowlist is unset", "", vault, false},
		{"outside every allowed root", allowed, outside, false},
		{"dot-dot traversal", allowed, vault + "/../../" + filepath.Base(outside), false},
		{"relative path", allowed, "vault", false},
		{"missing directory", allowed, filepath.Join(allowed, "missing"), false},
	}
	escape := filepath.Join(allowed, "escape")
	if err := os.Symlink(outside, escape); err == nil {
		cases = append(cases, struct {
			name, env, root string
			ok              bool
		}{"symlink escaping the allowed root", allowed, escape, false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(AllowedRootsEnv, tc.env)
			cfg := &types.DataSourceConfig{Settings: map[string]interface{}{"root_path": tc.root}}
			err := NewConnector().Validate(context.Background(), cfg)
			if (err == nil) != tc.ok {
				t.Fatalf("Validate(%q) error = %v, want ok=%v", tc.root, err, tc.ok)
			}
		})
	}
}

func TestValidateReadsFolderSettingsFromCredentialsRequest(t *testing.T) {
	root, _ := newVault(t)
	cfg := &types.DataSourceConfig{Credentials: map[string]interface{}{"root_path": root}}

	if err := NewConnector().Validate(context.Background(), cfg); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestEmptiedRootIsNotTreatedAsMassDeletion(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "a.md", "a", time.Hour)
	writeFile(t, root, "b.md", "b", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)

	// A bind mount whose host folder is gone shows up as an empty directory.
	for _, name := range []string{"a.md", "b.md"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)

	if err == nil || len(items) != 0 || next != nil {
		t.Fatalf("got items=%v next=%v err=%v, want an error and no deletions", items, next, err)
	}
}

func TestManualSyncReconcilesIntentionallyEmptiedRoot(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "a.md", "a", time.Hour)
	writeFile(t, root, "b.md", "b", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)

	for _, name := range []string{"a.md", "b.md"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Clicking "sync now" is the user confirming this folder's current state.
	cfg.ManualTrigger = true
	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("manual sync of an emptied root: %v", err)
	}

	got := byExternalID(t, items)
	assertItems(t, got, "local_folder:a.md", "local_folder:b.md")
	for id, item := range got {
		if !item.IsDeleted {
			t.Fatalf("%s should be reported deleted: %+v", id, item)
		}
	}
	if files := decodeCursor(next).Files; len(files) != 0 {
		t.Fatalf("cursor should be empty after the folder was emptied: %v", files)
	}
}

func TestInaccessibleRootFailsWithoutDeletions(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "a.md", "a", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	items, next, err := NewConnector().FetchIncremental(context.Background(), cfg, cursor)

	if err == nil || len(items) != 0 || next != nil {
		t.Fatalf("got items=%v next=%v err=%v, want an error and no deletions", items, next, err)
	}
}

func TestFullSyncReemitsUnchangedFilesAndReportsDeletions(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "keep.md", "keep", time.Hour)
	writeFile(t, root, "gone.md", "gone", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}

	h := &streamRecorder{}
	next, err := NewConnector().FetchFullStream(context.Background(), cfg, cursor, h)
	if err != nil {
		t.Fatal(err)
	}

	got := byExternalID(t, h.items)
	assertItems(t, got, "local_folder:keep.md", "local_folder:gone.md")
	if got["local_folder:keep.md"].IsDeleted || !got["local_folder:gone.md"].IsDeleted {
		t.Fatalf("deletion flags wrong: %+v", got)
	}
	if files := decodeCursor(next).Files; len(files) != 1 {
		t.Fatalf("cursor files = %v, want only keep.md", files)
	}
	if state := decodeCursor(next); state.FullSync || state.FullBaseline != nil {
		t.Fatalf("a finished full sync must clear its baseline: %+v", state)
	}
}

func TestOversizedFileIsReportedWithoutReadingIt(t *testing.T) {
	root, cfg := newVault(t)
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	writeFile(t, root, "huge.md", strings.Repeat("x", 1024*1024+1), time.Hour)

	items, cursor := syncFolder(t, cfg, nil)

	assertItems(t, items, "local_folder:huge.md")
	item := items["local_folder:huge.md"]
	if item.Metadata["error"] == "" || len(item.Content) != 0 {
		t.Fatalf("oversized file should surface as a failed item, got %+v", item)
	}
	if files := decodeCursor(cursor).Files; len(files) != 0 {
		t.Fatalf("an unread file must not enter the cursor: %v", files)
	}
}

func TestFileThatBecomesEmptyIsEmittedButNewEmptyFileIsNot(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "note.md", "content", time.Hour)
	writeFile(t, root, "blank.md", "", time.Hour)
	items, cursor := syncFolder(t, cfg, nil)
	assertItems(t, items, "local_folder:note.md")

	writeFile(t, root, "note.md", "", settled)
	items, _ = syncFolder(t, cfg, cursor)

	assertItems(t, items, "local_folder:note.md")
	if item := items["local_folder:note.md"]; len(item.Content) != 0 || !item.UpdateInPlace || item.IsDeleted {
		t.Fatalf("an emptied file should be an empty in-place update, got %+v", item)
	}
}

func TestFetchAllEmitsEveryFileWithoutDeletions(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "a.md", "a", time.Hour)
	writeFile(t, root, "b/c.md", "c", time.Hour)

	items, err := NewConnector().FetchAll(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}

	got := byExternalID(t, items)
	assertItems(t, got, "local_folder:a.md", "local_folder:b/c.md")
}

// errStreamInterrupted stands in for the timeout, crash or cancelled context a
// long sync has to survive.
var errStreamInterrupted = errors.New("stream interrupted")

// streamRecorder captures what a streaming sync emits and checkpoints. Setting
// stopAfter aborts the stream once that many items have been emitted.
type streamRecorder struct {
	items       []types.FetchedItem
	checkpoints []*types.SyncCursor
	ckptAtEmit  []int
	stopAfter   int
}

func (h *streamRecorder) Emit(_ context.Context, item types.FetchedItem) error {
	if h.stopAfter > 0 && len(h.items) >= h.stopAfter {
		return errStreamInterrupted
	}
	h.ckptAtEmit = append(h.ckptAtEmit, len(h.checkpoints))
	h.items = append(h.items, item)
	return nil
}

func (h *streamRecorder) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	h.checkpoints = append(h.checkpoints, cursor)
	return nil
}

func (h *streamRecorder) lastCheckpoint() *types.SyncCursor {
	if len(h.checkpoints) == 0 {
		return nil
	}
	return h.checkpoints[len(h.checkpoints)-1]
}

func TestStreamingSyncInterleavesEmitAndCheckpoint(t *testing.T) {
	root, cfg := newVault(t)
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		writeFile(t, root, name, "body of "+name, time.Hour)
	}

	h := &streamRecorder{}
	if _, err := NewConnector().FetchStream(context.Background(), cfg, nil, h); err != nil {
		t.Fatal(err)
	}

	assertItems(t, byExternalID(t, h.items),
		"local_folder:a.md", "local_folder:b.md", "local_folder:c.md")
	// Each file is checkpointed before the next is emitted, so the walk never
	// accumulates content: the Nth emit must see exactly N-1 checkpoints.
	for i, seen := range h.ckptAtEmit {
		if seen != i {
			t.Fatalf("emit %d saw %d checkpoints, want %d: items are being buffered", i, seen, i)
		}
	}
	if len(h.checkpoints) != 3 {
		t.Fatalf("checkpoints = %d, want one per file", len(h.checkpoints))
	}
}

func TestInterruptedStreamResumesFromLastCheckpoint(t *testing.T) {
	root, cfg := newVault(t)
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		writeFile(t, root, name, "body of "+name, time.Hour)
	}

	first := &streamRecorder{stopAfter: 2}
	_, err := NewConnector().FetchStream(context.Background(), cfg, nil, first)
	if !errors.Is(err, errStreamInterrupted) {
		t.Fatalf("interrupted stream error = %v, want the emit failure", err)
	}
	assertItems(t, byExternalID(t, first.items), "local_folder:a.md", "local_folder:b.md")
	resume := first.lastCheckpoint()
	if resume == nil {
		t.Fatal("an interrupted stream must leave a checkpoint to resume from")
	}

	second := &streamRecorder{}
	next, err := NewConnector().FetchStream(context.Background(), cfg, resume, second)
	if err != nil {
		t.Fatal(err)
	}

	// Files already checkpointed are not re-emitted; the one left over is.
	assertItems(t, byExternalID(t, second.items), "local_folder:c.md")
	if files := decodeCursor(next).Files; len(files) != 3 {
		t.Fatalf("cursor after resume = %v, want all three files", files)
	}
}

func TestInterruptedFullStreamKeepsDeletionBaseline(t *testing.T) {
	root, cfg := newVault(t)
	for _, name := range []string{"a.md", "b.md", "gone.md"} {
		writeFile(t, root, name, "body of "+name, time.Hour)
	}
	_, cursor := syncFolder(t, cfg, nil)
	if err := os.Remove(filepath.Join(root, "gone.md")); err != nil {
		t.Fatal(err)
	}

	first := &streamRecorder{stopAfter: 1}
	_, err := NewConnector().FetchFullStream(context.Background(), cfg, cursor, first)
	if !errors.Is(err, errStreamInterrupted) {
		t.Fatalf("interrupted full stream error = %v, want the emit failure", err)
	}
	mid := decodeCursor(first.lastCheckpoint())
	if !mid.FullSync || len(mid.FullBaseline) != 3 {
		t.Fatalf("an interrupted full sync must keep its deletion baseline: %+v", mid)
	}

	second := &streamRecorder{}
	next, err := NewConnector().FetchFullStream(context.Background(), cfg, first.lastCheckpoint(), second)
	if err != nil {
		t.Fatal(err)
	}

	// a.md was re-read before the interruption, b.md still had to be, and the
	// deletion is still reconciled against the pre-full-sync baseline.
	got := byExternalID(t, second.items)
	assertItems(t, got, "local_folder:b.md", "local_folder:gone.md")
	if !got["local_folder:gone.md"].IsDeleted {
		t.Fatalf("the deleted file was not reported: %+v", got)
	}
	state := decodeCursor(next)
	if state.FullSync || state.FullBaseline != nil {
		t.Fatalf("a finished full sync must clear its baseline: %+v", state)
	}
	if len(state.Files) != 2 {
		t.Fatalf("cursor files = %v, want a.md and b.md", state.Files)
	}
}

func TestCancelledContextStopsStreamAtLastCheckpoint(t *testing.T) {
	root, cfg := newVault(t)
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		writeFile(t, root, name, "body of "+name, time.Hour)
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &cancellingRecorder{streamRecorder: streamRecorder{}, cancelAfter: 1, cancel: cancel}
	_, err := NewConnector().FetchStream(ctx, cfg, nil, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream error = %v, want context.Canceled", err)
	}

	// Whatever was checkpointed stays valid, and the next run continues there.
	resume := h.lastCheckpoint()
	if files := decodeCursor(resume).Files; len(files) != 1 {
		t.Fatalf("checkpoint after cancel = %v, want only the finished file", files)
	}
	second := &streamRecorder{}
	if _, err := NewConnector().FetchStream(context.Background(), cfg, resume, second); err != nil {
		t.Fatal(err)
	}
	assertItems(t, byExternalID(t, second.items), "local_folder:b.md", "local_folder:c.md")
}

// cancellingRecorder cancels the sync's context part-way, the way an Asynq
// timeout does, instead of failing an Emit.
type cancellingRecorder struct {
	streamRecorder
	cancelAfter int
	cancel      context.CancelFunc
}

func (h *cancellingRecorder) Emit(ctx context.Context, item types.FetchedItem) error {
	if err := h.streamRecorder.Emit(ctx, item); err != nil {
		return err
	}
	if len(h.items) >= h.cancelAfter {
		h.cancel()
	}
	return nil
}

func TestUnchangedFilesNeitherEmitNorCheckpoint(t *testing.T) {
	root, cfg := newVault(t)
	for _, name := range []string{"a.md", "b.md", "c.md"} {
		writeFile(t, root, name, "body of "+name, time.Hour)
	}
	_, cursor := syncFolder(t, cfg, nil)

	// Nothing changed, so the cursor cannot advance and the sync must not
	// persist it: a quiet folder costs no writes, however many files it holds.
	quiet := &streamRecorder{}
	if _, err := NewConnector().FetchStream(context.Background(), cfg, cursor, quiet); err != nil {
		t.Fatal(err)
	}
	if len(quiet.items) != 0 || len(quiet.checkpoints) != 0 {
		t.Fatalf("no-op sync emitted %d items and wrote %d checkpoints, want none",
			len(quiet.items), len(quiet.checkpoints))
	}

	// A file touched without a content change does advance the cursor (its
	// recorded mtime), so it checkpoints once — without re-ingesting.
	writeFile(t, root, "b.md", "body of b.md", settled)
	touched := &streamRecorder{}
	if _, err := NewConnector().FetchStream(context.Background(), cfg, cursor, touched); err != nil {
		t.Fatal(err)
	}
	if len(touched.items) != 0 || len(touched.checkpoints) != 1 {
		t.Fatalf("touched file produced %d items and %d checkpoints, want 0 and 1",
			len(touched.items), len(touched.checkpoints))
	}
}

func TestFullStreamKeepsDeferredAndOversizedFilesInsteadOfDeletingThem(t *testing.T) {
	root, cfg := newVault(t)
	writeFile(t, root, "steady.md", "steady", time.Hour)
	writeFile(t, root, "quiet.md", "first draft", time.Hour)
	writeFile(t, root, "huge.md", "small for now", time.Hour)
	_, cursor := syncFolder(t, cfg, nil)

	// quiet.md is being typed right now, huge.md has grown past the size cap.
	// Both are still on disk, so neither may be reported as deleted.
	writeFile(t, root, "quiet.md", "still being edited", 0)
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	writeFile(t, root, "huge.md", strings.Repeat("x", 1024*1024+1), time.Hour)

	h := &streamRecorder{}
	next, err := NewConnector().FetchFullStream(context.Background(), cfg, cursor, h)
	if err != nil {
		t.Fatal(err)
	}

	got := byExternalID(t, h.items)
	for _, id := range []string{"local_folder:quiet.md", "local_folder:huge.md", "local_folder:steady.md"} {
		if item, ok := got[id]; ok && item.IsDeleted {
			t.Fatalf("%s is still on disk and must not be reported deleted: %+v", id, item)
		}
	}
	if _, emitted := got["local_folder:quiet.md"]; emitted {
		t.Fatalf("a file inside the quiet period should be deferred, not emitted: %+v", got)
	}
	if item := got["local_folder:huge.md"]; item.Metadata["error"] == "" {
		t.Fatalf("the oversized file should surface as a failed item: %+v", item)
	}

	// Both must stay in the cursor: a full sync rebuilds it, and a file it never
	// got to would otherwise be forgotten and could never be reconciled again.
	files := decodeCursor(next).Files
	for _, rel := range []string{"steady.md", "quiet.md", "huge.md"} {
		if _, known := files[rel]; !known {
			t.Fatalf("%s dropped out of the cursor after a full sync: %v", rel, files)
		}
	}

	// Proof that nothing was orphaned: deleting the deferred file is still
	// reconciled by the next ordinary sync.
	if err := os.Remove(filepath.Join(root, "quiet.md")); err != nil {
		t.Fatal(err)
	}
	items, _ := syncFolder(t, cfg, next)
	if item, ok := items["local_folder:quiet.md"]; !ok || !item.IsDeleted {
		t.Fatalf("deleting a previously deferred file must be reconciled, got %+v", items)
	}
}
