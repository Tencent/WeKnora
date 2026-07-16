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
)

// -----------------------------------------------------------------------------
// Test doubles
// -----------------------------------------------------------------------------

// fakeSandboxSource is an in-memory stand-in for SessionBoundManager that
// records the calls the collector makes and returns pre-programmed
// responses. Concrete tests populate `entries` (one map per session) and
// `contents` (one map per absolute path).
type fakeSandboxSource struct {
	entries  map[string][]sandbox.RemoteDirEntry
	contents map[string][]byte

	listErr error
	readErr error

	listCalls int
	readCalls []string
}

func (f *fakeSandboxSource) ListSessionFiles(_ context.Context, sessionID, _ string) ([]sandbox.RemoteDirEntry, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
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

func (f *fakeFileService) GetFile(_ context.Context, _ string) (io.ReadCloser, error) {
	panic("GetFile should not be called by ArtifactCollector")
}

func (f *fakeFileService) GetFileURL(_ context.Context, _ string) (string, error) {
	panic("GetFileURL should not be called by ArtifactCollector")
}

func (f *fakeFileService) DeleteFile(_ context.Context, _ string) error { return nil }

func (f *fakeFileService) CopyFile(_ context.Context, _ string, _ uint64, _ string) (string, error) {
	panic("CopyFile should not be called by ArtifactCollector")
}

// -----------------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------------

func newTestCollector(src *fakeSandboxSource, store *fakeStore, fs *fakeFileService, max int64) *ArtifactCollector {
	return NewArtifactCollector(src, fs, store, ArtifactCollectorConfig{MaxFileBytes: max})
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

	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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

	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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

func TestArtifactCollector_ReattachesOnMtimeChange(t *testing.T) {
	ctx := context.Background()
	oldMod, _ := time.Parse(time.RFC3339, "2026-07-10T10:20:33Z")
	src := &fakeSandboxSource{
		entries: map[string][]sandbox.RemoteDirEntry{
			"sess-1": {
				// Same path as the known set, but a *newer* mtime — must be re-attached.
				{Name: "report.pptx", Path: "/workspace/output/report.pptx", Type: sandbox.RemoteEntryFile, Size: 4, ModTime: mustParseTime("2026-07-10T10:21:00Z")},
			},
		},
		contents: map[string][]byte{
			"/workspace/output/report.pptx": []byte("PPTX"),
		},
	}
	store := &fakeStore{prev: []types.MessageArtifact{
		{SourcePath: "/workspace/output/report.pptx", ModTime: oldMod},
	}}
	fs := &fakeFileService{}
	c := newTestCollector(src, store, fs, 1<<20)

	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Collect() len = %d, want 1 (mtime-change should re-attach)", len(got))
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

	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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

	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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
	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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
	got, err := c.Collect(ctx, "", 42, "/workspace/output")
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
	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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

	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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
	got, err := c.Collect(ctx, "sess-1", 42, "/workspace/output")
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
