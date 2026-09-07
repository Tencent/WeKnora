package handler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSnapshotID(t *testing.T) {
	t.Parallel()
	ok := []string{"weknora-snapshot-20260102-150405", "pre-restore-20260102-150405-2"}
	for _, id := range ok {
		if err := validateSnapshotID(id); err != nil {
			t.Fatalf("validateSnapshotID(%q) = %v, want nil", id, err)
		}
	}
	bad := []string{"", ".", "..", "a/b", `a\b`, "../x", "x/../y"}
	for _, id := range bad {
		if err := validateSnapshotID(id); err == nil {
			t.Fatalf("validateSnapshotID(%q) = nil, want error", id)
		}
	}
}

func TestSafeJoinRejectsTraversal(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if _, err := safeJoin(base, "../outside"); err == nil {
		t.Fatal("expected traversal to fail")
	}
	if _, err := safeJoin(base, "/etc/passwd"); err == nil {
		t.Fatal("expected absolute path to fail")
	}
	if _, err := safeJoin(base, `foo\bar`); err == nil {
		t.Fatal("expected backslash to fail")
	}
	got, err := safeJoin(base, "a/b.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "a", "b.txt")
	if got != want {
		t.Fatalf("safeJoin = %q, want %q", got, want)
	}
}

func TestParseVersionAndAtLeast(t *testing.T) {
	t.Parallel()
	if _, ok := parseVersion("0.8.0"); !ok {
		t.Fatal("expected 0.8.0 to parse")
	}
	if _, ok := parseVersion("v0.8.0-rc.1"); !ok {
		t.Fatal("expected prerelease suffix to parse leading X.Y.Z")
	}
	if _, ok := parseVersion("unknown"); ok {
		t.Fatal("unknown must not parse")
	}
	if !versionAtLeast("0.8.0", "0.7.9") {
		t.Fatal("0.8.0 should be >= 0.7.9")
	}
	if versionAtLeast("0.7.0", "0.8.0") {
		t.Fatal("0.7.0 should not be >= 0.8.0")
	}
	if !versionAtLeast("unknown", "unknown") {
		t.Fatal("unparseable vs unparseable should allow")
	}
	if !versionAtLeast("0.8.0-rc.1", "0.8.0") {
		t.Fatal("0.8.0-rc.1 should compare as 0.8.0")
	}
}

func TestUniqueSnapshotPath(t *testing.T) {
	dir := t.TempDir()
	id1, path1, err := uniqueSnapshotPath(dir, "snap")
	if err != nil {
		t.Fatal(err)
	}
	if id1 != "snap" {
		t.Fatalf("id = %q, want snap", id1)
	}
	if err := os.WriteFile(path1, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	id2, _, err := uniqueSnapshotPath(dir, "snap")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != "snap-2" {
		t.Fatalf("id = %q, want snap-2", id2)
	}
}

func TestIsReservedStorageRel(t *testing.T) {
	t.Parallel()
	if !isReservedStorageRel(".weknora-backups/foo.tar.gz") {
		t.Fatal("expected snapshots dir to be reserved")
	}
	if isReservedStorageRel("tenant/doc.pdf") {
		t.Fatal("regular relative path must not be reserved")
	}
}

func TestTarDirToSkipsReservedAndRestoreReplaces(t *testing.T) {
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "keep", "a.txt"), []byte("new-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, backupSnapshotsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(src, backupSnapshotsDirName, "secret.tar.gz")
	if err := os.WriteFile(secret, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}

	archive := filepath.Join(t.TempDir(), "files.tar")
	if err := tarDirTo(archive, src, func(_ string, rel string, _ os.FileInfo) bool {
		return isReservedStorageRel(rel)
	}); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	if err := os.WriteFile(filepath.Join(dest, "stale.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dest, backupSnapshotsDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	rollback := filepath.Join(dest, backupSnapshotsDirName, "rollback.tar.gz")
	if err := os.WriteFile(rollback, []byte("keep-me"), 0o600); err != nil {
		t.Fatal(err)
	}

	staging := filepath.Join(dest, backupRestoreNewDir)
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := untarFile(archive, staging, untarOptions{maxBytes: 1 << 20, skipReserved: true}); err != nil {
		t.Fatal(err)
	}
	if err := promoteRestoredFiles(dest, staging); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dest, "stale.txt")); !os.IsNotExist(err) {
		t.Fatal("stale file should have been replaced away")
	}
	got, err := os.ReadFile(filepath.Join(dest, "keep", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-a" {
		t.Fatalf("restored file = %q", got)
	}
	got, err = os.ReadFile(filepath.Join(dest, backupSnapshotsDirName, "rollback.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep-me" {
		t.Fatal("rollback snapshot must survive file replace")
	}
}

func TestUntarRejectsSymlinkAndTraversal(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "bad.tar")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	hdr := &tar.Header{Name: "../evil", Typeflag: tar.TypeReg, Size: 1, Mode: 0o644}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	dest := t.TempDir()
	if err := untarFile(archive, dest, untarOptions{maxBytes: 1 << 20}); err == nil {
		t.Fatal("expected traversal entry to fail")
	}
}

func TestCopyLimited(t *testing.T) {
	t.Parallel()
	remain := int64(4)
	var buf bytes.Buffer
	err := copyLimited(&buf, bytes.NewReader([]byte("hello")), &remain)
	if err != errExtractLimit {
		t.Fatalf("err = %v, want errExtractLimit", err)
	}
}

func TestGunzipLimited(t *testing.T) {
	var raw bytes.Buffer
	gw := gzip.NewWriter(&raw)
	if _, err := io.Copy(gw, bytes.NewReader(bytes.Repeat([]byte("A"), 64))); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	in := filepath.Join(t.TempDir(), "in.gz")
	out := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(in, raw.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := gunzipFile(in, out, 8); err != errExtractLimit {
		t.Fatalf("err = %v, want extract limit", err)
	}
}

func TestBackupDirDefaultUnderFilesVolume(t *testing.T) {
	t.Setenv("BACKUP_DIR", "")
	base := t.TempDir()
	t.Setenv("LOCAL_STORAGE_BASE_DIR", base)
	h := &BackupHandler{}
	dir, err := h.backupDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, backupSnapshotsDirName)
	if dir != want {
		t.Fatalf("backupDir = %q, want %q", dir, want)
	}
}

func TestUserFacingRestoreErrorHidesDumpStderr(t *testing.T) {
	t.Parallel()
	got := userFacingRestoreError(io.EOF)
	if got != "see server logs for details" {
		t.Fatalf("got %q", got)
	}
	got = userFacingRestoreError(errExtractLimit)
	if got != errExtractLimit.Error() {
		t.Fatalf("extract limit should surface, got %q", got)
	}
}
