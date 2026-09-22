package service

import (
	"context"
	stderrors "errors"
	"io"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// -----------------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------------

// fakeSandboxSource is an in-memory stand-in for SessionBoundManager that
// records the calls the collector makes and returns pre-programmed
// responses. Concrete tests populate `entries` (one map per session) and
// `contents` (one map per absolute path).
type fakeSandboxSource struct {
	entries      map[string][]sandbox.RemoteDirEntry
	entriesByDir map[string][]sandbox.RemoteDirEntry
	contents     map[string][]byte

	listErr error
	readErr error

	listCalls  int
	listedDirs []string
	readCalls  []string
}

func (f *fakeSandboxSource) ListSessionFiles(
	_ context.Context, sessionID, dir string,
) ([]sandbox.RemoteDirEntry, error) {
	f.listCalls++
	f.listedDirs = append(f.listedDirs, dir)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.entriesByDir != nil {
		return f.entriesByDir[dir], nil
	}
	return f.entries[sessionID], nil
}

func (f *fakeSandboxSource) ReadSessionFile(_ context.Context, _ string, path string) ([]byte, error) {
	f.readCalls = append(f.readCalls, path)
	if f.readErr != nil {
		return nil, f.readErr
	}
	if data, ok := f.contents[path]; ok {
		return data, nil
	}
	return nil, stderrors.New("fake source: not found: " + path)
}

// fakeStore lets a test declare which (path, mtime) tuples the collector
// should treat as already recorded.
type fakeStore struct {
	prev []types.MessageArtifact
	err  error
}

func (s *fakeStore) KnownArtifacts(_ context.Context, _ string) ([]types.MessageArtifact, error) {
	return s.prev, s.err
}

func (s *fakeStore) RecordRestoredMtime(_ context.Context, _, sourcePath string, mod time.Time, hash string) error {
	updated, changed := types.MessageArtifacts(s.prev).WithRestoredMtime(sourcePath, mod, hash)
	if changed {
		s.prev = updated
	}
	return nil
}

// fakeFileService captures uploads and returns a deterministic provider URL.
// Only SaveBytes is exercised by the collector; the other methods are
// implemented to satisfy interfaces.FileService but panic on use so a
// regression that starts calling them doesn't fail silently.
type fakeFileService struct {
	saved     map[string][]byte
	saveErr   error
	seq       int
	tenantIDs []uint64
}

func (f *fakeFileService) CheckConnectivity(_ context.Context) error { return nil }

func (f *fakeFileService) SaveFile(_ context.Context, _ *multipart.FileHeader, _ uint64, _ string) (string, error) {
	panic("SaveFile should not be called by ArtifactCollector")
}

func (f *fakeFileService) SaveBytes(_ context.Context, data []byte, tenantID uint64, fileName string, _ bool) (string, error) {
	if f.saveErr != nil {
		return "", f.saveErr
	}
	if f.saved == nil {
		f.saved = map[string][]byte{}
	}
	f.seq++
	key := "fake://tenant-" + fileName
	f.saved[key] = append([]byte(nil), data...)
	f.tenantIDs = append(f.tenantIDs, tenantID)
	return key, nil
}

func (f *fakeFileService) GetFile(_ context.Context, filePath string) (io.ReadCloser, error) {
	if data, ok := f.saved[filePath]; ok {
		return io.NopCloser(strings.NewReader(string(data))), nil
	}
	return nil, stderrors.New("fake file service: not found: " + filePath)
}

func (f *fakeFileService) GetFileURL(_ context.Context, _ string) (string, error) {
	panic("GetFileURL should not be called by ArtifactCollector")
}

func (f *fakeFileService) DeleteFile(_ context.Context, _ string) error { return nil }

func (f *fakeFileService) CopyFile(_ context.Context, _ string, _ uint64, _ string) (string, error) {
	panic("CopyFile should not be called by ArtifactCollector")
}

// resourceRefFileService is a fileService whose SaveBytes returns a valid
// resource:// reference, so the collector's binding path is exercised.
type resourceRefFileService struct {
	fakeFileService
	handle string
}

func (f *resourceRefFileService) SaveBytes(_ context.Context, data []byte, tenantID uint64, fileName string, _ bool) (string, error) {
	if f.saved == nil {
		f.saved = map[string][]byte{}
	}
	f.seq++
	ref := types.BuildResourcePath(f.handle)
	f.saved[ref] = append([]byte(nil), data...)
	f.tenantIDs = append(f.tenantIDs, tenantID)
	return ref, nil
}

// bindCall records one Bind invocation for assertions.
type bindCall struct {
	ref       string
	ownerType string
	ownerID   string
	relation  string
}

// fakeCatalog captures Bind calls; every other ResourceCatalog method is a
// no-op stub because the collector only ever calls Bind.
type fakeCatalog struct {
	binds   []bindCall
	bindErr error
	// Release scripting, used by the deletion-guard tests. releaseRemaining
	// maps a reference to the binding count left after the release; a missing
	// entry answers -1 ("not a catalog handle").
	releaseRemaining map[string]int64
	releaseErr       error
	releases         []string
}

func (c *fakeCatalog) Register(context.Context, uint64, string, interfaces.ResourceRegistration) (string, error) {
	return "", nil
}

func (c *fakeCatalog) Resolve(context.Context, string) (*types.StoredResource, error) {
	return nil, nil
}

func (c *fakeCatalog) ResolvePath(_ context.Context, v string) (string, *types.StoredResource, error) {
	return v, nil, nil
}

func (c *fakeCatalog) Bind(_ context.Context, ref, ownerType, ownerID, relation string) error {
	c.binds = append(c.binds, bindCall{ref, ownerType, ownerID, relation})
	return c.bindErr
}
func (c *fakeCatalog) MarkDeleted(context.Context, string) error { return nil }

func (c *fakeCatalog) Release(_ context.Context, ref, ownerType, ownerID string) (int64, error) {
	c.releases = append(c.releases, ref+"|"+ownerType+"|"+ownerID)
	if c.releaseErr != nil {
		return -1, c.releaseErr
	}
	if remaining, ok := c.releaseRemaining[ref]; ok {
		return remaining, nil
	}
	return -1, nil
}

func (c *fakeCatalog) CreateAccessGrant(context.Context, string, time.Duration) (string, error) {
	return "", nil
}

func (c *fakeCatalog) ResolveAccessGrant(context.Context, string) (*types.StoredResource, error) {
	return nil, nil
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func newTestCollector(src *fakeSandboxSource, store *fakeStore, fs *fakeFileService, max int64) *ArtifactCollector {
	// catalog is nil here: these tests use a fakeFileService that returns raw
	// "fake://" paths, so no resource binding is attempted. Binding behaviour
	// is covered separately in TestArtifactCollector_BindsResourceToMessage.
	return NewArtifactCollector(src, fs, store, nil, ArtifactCollectorConfig{MaxFileBytes: max})
}

// hostArtifactManager is a host-typed sandbox.Manager that reads artifacts
// from a fake filesystem. Host sessions never write a pin, so tests use this
// as HostSessionResolver's manager.
type hostArtifactManager struct {
	artifactFallbackManager
}

func (m *hostArtifactManager) GetType() sandbox.SandboxType { return sandbox.SandboxTypeHost }

func newHostCollector(t *testing.T, hostSource *fakeSandboxSource) *ArtifactCollector {
	t.Helper()
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	collector := NewArtifactCollector(
		&fakeSandboxSource{},
		&fakeFileService{},
		&fakeStore{},
		nil,
		ArtifactCollectorConfig{MaxFileBytes: 1 << 20},
	)
	collector.resolver = stubSandboxResolver{}
	collector.pinner = pinner
	collector.host = NewHostSessionResolver(pinner, &hostArtifactManager{
		artifactFallbackManager: artifactFallbackManager{source: hostSource},
	})
	return collector
}

func TestArtifactCollector_CollectsNewFiles(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
				{Name: "summary.txt", Path: "/workspace/output/summary.txt", Type: sandbox.RemoteEntryFile, Size: 3, ModTime: mustParseTime("2026-07-10T10:20:34Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
			"/workspace/output/summary.txt": []byte("hey"),
		},
	}
	store := &fakeStore{}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Collect() len = %d, want 2 (%+v)", len(got), got)
	}
	// Files must be uploaded with the tenant ID passed by the caller.
	for _, tID := range fs.tenantIDs {
		if tID != 42 {
			t.Fatalf("SaveBytes tenant = %d, want 42", tID)
		}
	}
	// Both files must have populated URL + FileType + FileSize.
	for _, art := range got {
		if art.URL == "" {
			t.Fatalf("artifact URL empty: %+v", art)
		}
		if art.FileType == "" {
			t.Fatalf("artifact FileType empty: %+v", art)
		}
		if art.FileSize == 0 {
			t.Fatalf("artifact FileSize zero: %+v", art)
		}
	}
}

func TestArtifactCollector_NotifyFiresBeforeUpload(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{
					Name: "a.html", Path: "/workspace/output/a.html",
					Type: sandbox.RemoteEntryFile, Size: 2,
					ModTime: mustParseTime("2026-07-10T10:20:33Z"),
				},
				{
					Name: "b.csv", Path: "/workspace/output/b.csv",
					Type: sandbox.RemoteEntryFile, Size: 2,
					ModTime: mustParseTime("2026-07-10T10:20:34Z"),
				},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/a.html": []byte("ok"),
			"/workspace/output/b.csv":  []byte("x,"),
		},
	}
	fs := &fakeFileService{}
	c := newTestCollector(src, &fakeStore{}, fs, 1<<20)

	var notified int
	got, err := c.CollectWithNotify(ctx, "sess-1", "msg-1", 42, "/workspace/output", func(n int) {
		if len(fs.saved) != 0 {
			t.Fatalf("notify ran after SaveBytes: saved=%v", fs.saved)
		}
		if len(src.readCalls) == 0 {
			t.Fatalf("notify should run after hash reads")
		}
		notified = n
	})
	if err != nil {
		t.Fatalf("CollectWithNotify() error = %v", err)
	}
	if notified != 2 {
		t.Fatalf("notify count = %d, want 2", notified)
	}
	if len(got) != 2 {
		t.Fatalf("CollectWithNotify() len = %d, want 2", len(got))
	}
}

func TestArtifactCollector_NotifySkipsHashMatchedRestores(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:21:00Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{
			SourcePath:  "/workspace/output/report.pptx",
			ModTime:     oldMod,
			FileSize:    4,
			ContentHash: artifactContentHash([]byte("PPTX")),
		},
	}}
	c := newTestCollector(src, store, &fakeFileService{}, 1<<20)

	notified := 0
	got, err := c.CollectWithNotify(ctx, "sess-1", "msg-1", 42, "/workspace/output", func(n int) {
		notified = n
	})
	if err != nil {
		t.Fatalf("CollectWithNotify() error = %v", err)
	}
	if notified != 0 {
		t.Fatalf("notify count = %d, want 0 (restored files must not look pending)", notified)
	}
	if len(got) != 0 {
		t.Fatalf("CollectWithNotify() len = %d, want 0", len(got))
	}
}

func TestArtifactCollector_SkipsAlreadyKnown(t *testing.T) {
	ctx := context.Background()
	mod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{SourcePath: "/workspace/output/report.pptx", ModTime: mod},
	}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0 (dedupe should have kicked in)", len(got))
	}
	if len(fs.saved) != 0 {
		t.Fatalf("SaveBytes should not have been called; saved=%v", fs.saved)
	}
	if len(src.readCalls) != 0 {
		t.Fatalf("ReadSessionFile should not have been called; readCalls=%v", src.readCalls)
	}
}

func TestArtifactCollector_SkipsRestoredFileSamePathSameSize(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				// git checkout rewrites the file with a fresh mtime but the
				// same bytes. That must not attach a duplicate to the next
				// assistant message.
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:21:00Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{
			SourcePath:  "/workspace/output/report.pptx",
			ModTime:     oldMod,
			FileSize:    4,
			ContentHash: artifactContentHash([]byte("PPTX")),
		},
	}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0 (same bytes after restore must not re-attach)", len(got))
	}
	if len(fs.saved) != 0 {
		t.Fatalf("SaveBytes should not have been called; saved=%v", fs.saved)
	}
}

func TestArtifactCollector_SkipsRestoredFileUsingStoredBlob(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:21:00Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	fs := &fakeFileService{saved: map[string][]byte{"fake://prev": []byte("PPTX")}}
	store := &fakeStore{prev: []types.MessageArtifact{
		{SourcePath: "/workspace/output/report.pptx", ModTime: oldMod, FileSize: 4, URL: "fake://prev"},
	}}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0 (same stored blob after restore must not re-attach)", len(got))
	}
}

func TestArtifactCollector_PersistsRestoredMtimeSoNextTurnDoesNotReread(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	newMod := mustParseTime("2026-07-10T10:21:00Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"fork-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: newMod},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{
			SourcePath:  "/workspace/output/report.pptx",
			ModTime:     oldMod,
			FileSize:    4,
			ContentHash: artifactContentHash([]byte("PPTX")),
		},
	}}
	c := newTestCollector(src, store, &fakeFileService{}, 1<<20)

	got, err := c.Collect(ctx, "fork-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("first Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("first Collect() len = %d, want 0", len(got))
	}
	if len(src.readCalls) != 1 {
		t.Fatalf("first Collect() reads = %d, want 1 (hash restore)", len(src.readCalls))
	}
	if !store.prev[0].ModTime.Equal(newMod) {
		t.Fatalf("restored mtime not written back: got %v want %v", store.prev[0].ModTime, newMod)
	}

	src.readCalls = nil
	got, err = c.Collect(ctx, "fork-1", "msg-2", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("second Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("second Collect() len = %d, want 0", len(got))
	}
	if len(src.readCalls) != 0 {
		t.Fatalf("second Collect() should hit path+mtime and skip the read; readCalls=%v", src.readCalls)
	}
}

func TestArtifactCollector_ReattachesOnMtimeChange(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				// Same path, newer mtime *and* a different size — the skill
				// actually rewrote the file this turn.
				{
					Name:    "report.pptx",
					Path:    "/workspace/output/report.pptx",
					Type:    sandbox.RemoteEntryFile,
					Size:    8,
					ModTime: mustParseTime("2026-07-10T10:21:00Z"),
				},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX-NEW"),
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{SourcePath: "/workspace/output/report.pptx", ModTime: oldMod, FileSize: 4},
	}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (rewritten file should re-attach)", len(got))
	}
	if len(fs.saved) != 1 {
		t.Fatalf("SaveBytes calls = %d, want 1", len(fs.saved))
	}
}

func TestArtifactCollector_ReattachesSameSizeDifferentContent(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	oldBytes := []byte("PPTX")
	newBytes := []byte("PPT!")
	if len(oldBytes) != len(newBytes) {
		t.Fatal("fixture must keep size identical so a size-key skip would hide the rewrite")
	}
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: int64(len(newBytes)), ModTime: mustParseTime("2026-07-10T10:21:00Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": newBytes,
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{SourcePath: "/workspace/output/report.pptx", ModTime: oldMod, FileSize: int64(len(oldBytes)), ContentHash: artifactContentHash(oldBytes)},
	}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (same-size rewrite must still attach)", len(got))
	}
	if len(fs.saved) != 1 {
		t.Fatalf("SaveBytes calls = %d, want 1", len(fs.saved))
	}
}

func TestArtifactCollector_SkipsOversize(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "huge.bin", Path: "/workspace/output/huge.bin", Type: sandbox.RemoteEntryFile, Size: 1024, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
				{Name: "ok.txt", Path: "/workspace/output/ok.txt", Type: sandbox.RemoteEntryFile, Size: 3, ModTime: mustParseTime("2026-07-10T10:20:34Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/huge.bin": bytesN(2048),
			"/workspace/output/ok.txt":   []byte("hey"),
		},
	}
	fs := &fakeFileService{}
	c := newTestCollector(src, &fakeStore{}, fs, 100)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (oversize file must be skipped)", len(got))
	}
	if got[0].FileName != "ok.txt" {
		t.Fatalf("Collect() kept the wrong file: %+v", got[0])
	}
}

func TestArtifactCollector_SkipsOversizeAfterRead(t *testing.T) {
	// Envd can report a stale size while the file grows; the collector must
	// enforce the cap against the actual bytes too.
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "lying.bin", Path: "/workspace/output/lying.bin", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/lying.bin": bytesN(2048),
		},
	}
	fs := &fakeFileService{}
	c := newTestCollector(src, &fakeStore{}, fs, 100)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0", len(got))
	}
}

func TestArtifactCollector_EmptyWhenNoEntries(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{}
	c := newTestCollector(src, &fakeStore{}, &fakeFileService{}, 1<<20)
	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0", len(got))
	}
}

func TestArtifactCollector_EmptyWhenNoSessionID(t *testing.T) {
	// The collector treats empty session ID as "nothing to do" — the design
	// spec forbids sandbox lookups for chat-only sessions.
	ctx := context.Background()
	src := &fakeSandboxSource{}
	c := newTestCollector(src, &fakeStore{}, &fakeFileService{}, 1<<20)
	got, err := c.Collect(ctx, "", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0", len(got))
	}
	if src.listCalls != 0 {
		t.Fatalf("expected 0 list calls for empty session id, got %d", src.listCalls)
	}
}

func TestArtifactCollector_ListErrorDegrades(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{listErr: stderrors.New("envd timeout")}
	c := newTestCollector(src, &fakeStore{}, &fakeFileService{}, 1<<20)
	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v (should degrade gracefully)", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0", len(got))
	}
}

func TestArtifactCollector_UploadFailureIsPerFile(t *testing.T) {
	// A single failed upload must NOT abort the whole batch — the other
	// files should still be persisted, matching the best-effort contract.
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "a.txt", Path: "/workspace/output/a.txt", Type: sandbox.RemoteEntryFile, Size: 1, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
				{Name: "b.txt", Path: "/workspace/output/b.txt", Type: sandbox.RemoteEntryFile, Size: 1, ModTime: mustParseTime("2026-07-10T10:20:34Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/a.txt": []byte("a"),
			"/workspace/output/b.txt": []byte("b"),
		},
	}
	// FileService fails on every call — both files should be skipped without
	// propagating an error.
	fs := &fakeFileService{saveErr: stderrors.New("s3 dead")}
	c := newTestCollector(src, &fakeStore{}, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v (want best-effort)", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0 (all uploads failed)", len(got))
	}
}

func TestArtifactCollector_FiltersDirectories(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				// The production ListSessionFiles never yields dirs, but the
				// collector must defensively skip anything with Type=="dir"
				// so alternate SandboxArtifactSource impls stay safe.
				{Name: "sub", Path: "/workspace/output/sub", Type: sandbox.RemoteEntryDir, Size: 0, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
				{Name: "a.txt", Path: "/workspace/output/a.txt", Type: sandbox.RemoteEntryFile, Size: 1, ModTime: mustParseTime("2026-07-10T10:20:34Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/a.txt": []byte("a"),
		},
	}
	c := newTestCollector(src, &fakeStore{}, &fakeFileService{}, 1<<20)
	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (dir must be skipped)", len(got))
	}
	if !strings.HasSuffix(got[0].SourcePath, "a.txt") {
		t.Fatalf("wrong file kept: %+v", got[0])
	}
}

func TestArtifactCollector_BindsResourceToMessage(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	// 22-char handle so BuildResourcePath yields a valid resource:// ref.
	fs := &resourceRefFileService{handle: "abcdefghijklmnopqrstuv"}
	cat := &fakeCatalog{}
	c := NewArtifactCollector(src, fs, &fakeStore{}, cat, ArtifactCollectorConfig{MaxFileBytes: 1 << 20})

	got, err := c.Collect(ctx, "sess-1", "msg-42", 7, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1", len(got))
	}
	if len(cat.binds) != 1 {
		t.Fatalf("Bind calls = %d, want 1 (%+v)", len(cat.binds), cat.binds)
	}
	b := cat.binds[0]
	if b.ownerType != "message" || b.ownerID != "msg-42" || b.relation != "artifact" {
		t.Fatalf("unexpected binding: %+v", b)
	}
	if _, ok := types.ParseResourcePath(b.ref); !ok {
		t.Fatalf("binding ref is not a resource reference: %q", b.ref)
	}
}

// Host sessions have no pin, so the collector must resolve them explicitly or
// every generated file renders as a broken sandbox: link.
func TestCollectResolvesHostSessionWithoutPin(t *testing.T) {
	ctx := context.Background()
	hostSource := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-host": {
				{
					Name:    "report.pptx",
					Path:    "/Users/dev/AppData/s1/output/report.pptx",
					Type:    sandbox.RemoteEntryFile,
					Size:    4,
					ModTime: mustParseTime("2026-07-10T10:20:33Z"),
				},
			},
		},
		contents: map[string][]byte{
			"/Users/dev/AppData/s1/output/report.pptx": []byte("PPTX"),
		},
	}
	c := newHostCollector(t, hostSource)

	got, err := c.Collect(ctx, "sess-host", "msg-1", 42, "/Users/dev/AppData/s1/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (host session without pin must still drain OutputDir)", len(got))
	}
	if got[0].FileName != "report.pptx" {
		t.Fatalf("Collect() file = %q, want report.pptx", got[0].FileName)
	}
}

// The collector reads and uploads every file it lists. Pointing it at the
// project root would copy the user's whole repository into object storage.
func TestCollectOnHostOnlyScansOutputDir(t *testing.T) {
	ctx := context.Background()
	workspaceRoot := "/Users/dev/proj"
	outputDir := "/Users/dev/AppData/s1/output"
	hostSource := &fakeSandboxSource{
		entriesByDir: map[string][]sandbox.RemoteDirEntry{
			workspaceRoot: {
				{
					Name:    "main.go",
					Path:    workspaceRoot + "/src/main.go",
					Type:    sandbox.RemoteEntryFile,
					Size:    12,
					ModTime: mustParseTime("2026-07-10T10:20:33Z"),
				},
			},
			outputDir: {
				{
					Name:    "report.pptx",
					Path:    outputDir + "/report.pptx",
					Type:    sandbox.RemoteEntryFile,
					Size:    4,
					ModTime: mustParseTime("2026-07-10T10:20:34Z"),
				},
			},
		},
		contents: map[string][]byte{
			workspaceRoot + "/src/main.go": []byte("package main"),
			outputDir + "/report.pptx":     []byte("PPTX"),
		},
	}
	c := newHostCollector(t, hostSource)

	got, err := c.Collect(ctx, "sess-host", "msg-1", 42, outputDir)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (only OutputDir files)", len(got))
	}
	if got[0].FileName != "report.pptx" {
		t.Fatalf("Collect() file = %q, want report.pptx", got[0].FileName)
	}
	if len(hostSource.listedDirs) == 0 {
		t.Fatal("ListSessionFiles was not called")
	}
	for _, dir := range hostSource.listedDirs {
		if dir != outputDir {
			t.Fatalf("ListSessionFiles dir = %q, want only %q", dir, outputDir)
		}
		if dir == workspaceRoot {
			t.Fatal("ListSessionFiles must never scan the workspace root")
		}
	}
}

// Remote deployments must be untouched: no host resolver, pin still required.
func TestCollectWithoutHostResolverStillRequiresPin(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{
					Name:    "report.pptx",
					Path:    "/workspace/output/report.pptx",
					Type:    sandbox.RemoteEntryFile,
					Size:    4,
					ModTime: mustParseTime("2026-07-10T10:20:33Z"),
				},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	c := newTestCollector(src, &fakeStore{}, &fakeFileService{}, 1<<20)
	c.resolver = stubSandboxResolver{}
	c.pinner = NewSessionSandboxPinner(newPinTestDB(t))

	got, err := c.Collect(ctx, "sess-1", "msg-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0 (unpinned remote session has no sandbox to drain)", len(got))
	}
	if src.listCalls != 0 {
		t.Fatalf("ListSessionFiles calls = %d, want 0", src.listCalls)
	}
}

func TestArtifactCollector_BindFailureDoesNotDropArtifact(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{Name: "a.txt", Path: "/workspace/output/a.txt", Type: sandbox.RemoteEntryFile, Size: 1, ModTime: mustParseTime("2026-07-10T10:20:33Z")},
			},
		},
		contents: map[string][]byte{"/workspace/output/a.txt": []byte("a")},
	}
	fs := &resourceRefFileService{handle: "abcdefghijklmnopqrstuv"}
	cat := &fakeCatalog{bindErr: stderrors.New("db down")}
	c := NewArtifactCollector(src, fs, &fakeStore{}, cat, ArtifactCollectorConfig{MaxFileBytes: 1 << 20})

	got, err := c.Collect(ctx, "sess-1", "msg-1", 7, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (bind failure must not drop the artifact)", len(got))
	}
}

// mustParseTime parses an RFC3339 timestamp for test data. It fails the
// program on error because test fixtures should never contain malformed
// timestamps.
func mustParseTime(raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		panic("mustParseTime: " + err.Error())
	}
	return t
}

// bytesN returns a byte slice of length n filled with 'x'. Kept local to
// avoid pulling in a dependency; matches "generate N bytes" test helpers
// elsewhere in the codebase.
func bytesN(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return b
}

// A deleted file whose sandbox copy was never touched must stay gone: its
// mtime has not moved, so nothing about the sandbox says it was regenerated.
func TestArtifactCollector_DeletedFileDoesNotComeBackUntouched(t *testing.T) {
	ctx := context.Background()
	mod := mustParseTime("2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{
					Name: "report.pptx", Path: "/workspace/output/report.pptx",
					Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mod,
				},
			},
		},
		contents: map[string][]byte{"/workspace/output/report.pptx": []byte("PPTX")},
	}
	deletedAt := mustParseTime("2026-07-11T00:00:00Z")
	store := &fakeStore{prev: []types.MessageArtifact{{
		SourcePath:  "/workspace/output/report.pptx",
		ModTime:     mod,
		FileSize:    4,
		ContentHash: artifactContentHash([]byte("PPTX")),
		DeletedAt:   &deletedAt,
	}}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-2", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0: an untouched sandbox file must not resurrect it", len(got))
	}
	if len(fs.saved) != 0 {
		t.Fatalf("SaveBytes should not have been called; saved=%v", fs.saved)
	}
}

// But a turn that actually rewrites the file — new mtime — regenerates it, and
// the user gets it back even when the bytes come out identical. The tombstone
// must not feed the same-content restore short circuit: doing so would make a
// deleted file impossible to reproduce byte-for-byte ever again.
func TestArtifactCollector_DeletedFileReturnsWhenRegenerated(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{
					Name: "report.pptx", Path: "/workspace/output/report.pptx",
					Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-11T09:00:00Z"),
				},
			},
		},
		contents: map[string][]byte{"/workspace/output/report.pptx": []byte("PPTX")},
	}
	deletedAt := mustParseTime("2026-07-11T00:00:00Z")
	store := &fakeStore{prev: []types.MessageArtifact{{
		SourcePath: "/workspace/output/report.pptx",
		ModTime:    mustParseTime("2026-07-10T10:20:33Z"),
		FileSize:   4,
		// Identical content to what the sandbox now holds.
		ContentHash: artifactContentHash([]byte("PPTX")),
		DeletedAt:   &deletedAt,
	}}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-2", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1: a regenerated file is a new artifact, not a restore", len(got))
	}
	if got[0].Deleted() {
		t.Fatal("the re-collected artifact must be live, not a tombstone")
	}
	if len(fs.saved) != 1 {
		t.Fatalf("the regenerated bytes must be uploaded; saved=%v", fs.saved)
	}
}

// A live artifact's same-content restore still short-circuits — the tombstone
// change must not weaken the fork-checkout path it was built for.
func TestArtifactCollector_LiveRestoreStillSkipsAlongsideATombstone(t *testing.T) {
	ctx := context.Background()
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				{
					Name: "report.pptx", Path: "/workspace/output/report.pptx",
					Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-11T09:00:00Z"),
				},
			},
		},
		contents: map[string][]byte{"/workspace/output/report.pptx": []byte("PPTX")},
	}
	deletedAt := mustParseTime("2026-07-11T00:00:00Z")
	store := &fakeStore{prev: []types.MessageArtifact{
		// An older version of the same file that the user deleted...
		{
			SourcePath: "/workspace/output/report.pptx", ModTime: mustParseTime("2026-07-09T10:00:00Z"),
			FileSize: 4, ContentHash: artifactContentHash([]byte("OLD!")), DeletedAt: &deletedAt,
		},
		// ...and a live one whose bytes match what the sandbox holds now.
		{
			SourcePath: "/workspace/output/report.pptx", ModTime: mustParseTime("2026-07-10T10:20:33Z"),
			FileSize: 4, ContentHash: artifactContentHash([]byte("PPTX")),
		},
	}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", "msg-2", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Collect() len = %d, want 0: the live version still matches, so this is a restore", len(got))
	}
}

type layoutArtifactSource struct {
	fakeSandboxSource
	layout sandbox.WorkspaceLayout
	err    error
}

func (s *layoutArtifactSource) SessionWorkspaceLayout(context.Context, string) (sandbox.WorkspaceLayout, error) {
	if s.err != nil {
		return sandbox.WorkspaceLayout{}, s.err
	}
	return s.layout, nil
}

func TestCollectTargetSkipsNilCollector(t *testing.T) {
	var c *ArtifactCollector
	dir, skip := c.CollectTarget(context.Background(), "s1")
	if !skip || dir != "" {
		t.Fatalf("CollectTarget() = %q, skip=%v, want skip with empty dir", dir, skip)
	}
}

func TestCollectTargetSkipsNilSource(t *testing.T) {
	c := NewArtifactCollector(nil, &fakeFileService{}, &fakeStore{}, nil, ArtifactCollectorConfig{})
	dir, skip := c.CollectTarget(context.Background(), "s1")
	if !skip || dir != "" {
		t.Fatalf("CollectTarget() = %q, skip=%v, want skip when there is no artifact source", dir, skip)
	}
}

func TestCollectTargetUsesRemoteOutputDir(t *testing.T) {
	remote := sandbox.RemoteWorkspaceLayout()
	c := NewArtifactCollector(
		&layoutArtifactSource{layout: remote},
		&fakeFileService{},
		&fakeStore{},
		nil,
		ArtifactCollectorConfig{},
	)
	dir, skip := c.CollectTarget(context.Background(), "s1")
	if skip {
		t.Fatal("CollectTarget() skip = true, want remote collection")
	}
	if dir != remote.Normalized().OutputDir {
		t.Fatalf("CollectTarget() dir = %q, want %q", dir, remote.Normalized().OutputDir)
	}
}

func TestCollectTargetSkipsHostLayoutWithoutOutputTree(t *testing.T) {
	root := "/Users/dev/My Project"
	c := NewArtifactCollector(&layoutArtifactSource{layout: sandbox.WorkspaceLayout{
		Origin:     sandbox.WorkspaceOriginHost,
		Root:       root,
		WriteRoots: []string{root},
		ReadRoots:  []string{root},
	}}, &fakeFileService{}, &fakeStore{}, nil, ArtifactCollectorConfig{})
	dir, skip := c.CollectTarget(context.Background(), "s1")
	if !skip || dir != "" {
		t.Fatalf("CollectTarget() = %q, skip=%v, want skip so host Root is not uploaded", dir, skip)
	}
}

func TestCollectTargetSkipsWhenLayoutLookupFails(t *testing.T) {
	c := NewArtifactCollector(
		&layoutArtifactSource{err: stderrors.New("unavailable")},
		&fakeFileService{},
		&fakeStore{},
		nil,
		ArtifactCollectorConfig{},
	)
	dir, skip := c.CollectTarget(context.Background(), "s1")
	if !skip || dir != "" {
		t.Fatalf("CollectTarget() = %q, skip=%v, want skip on layout error", dir, skip)
	}
}

func TestCollectTargetKeepsRemoteDefaultWhenSourceHasNoLayout(t *testing.T) {
	c := NewArtifactCollector(&fakeSandboxSource{}, &fakeFileService{}, &fakeStore{}, nil, ArtifactCollectorConfig{})
	dir, skip := c.CollectTarget(context.Background(), "s1")
	if skip || dir != "" {
		t.Fatalf("CollectTarget() = %q, skip=%v, want empty dir and skip=false for the remote default", dir, skip)
	}
}
