package git_repo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveUnderRootAcceptsRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveUnderRoot(root, "a.md")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "a.md" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveUnderRootRejectsEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := resolveUnderRoot(root, "../outside.md"); err == nil {
		t.Fatal("expected escape error")
	}
}

func TestResolveUnderRootRejectsEscapingSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("leaked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "leak.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveUnderRoot(root, "leak.md"); err == nil {
		t.Fatal("escaping symlink must be rejected")
	}
}

func TestResolveUnderRootAllowsInternalSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "real.md"), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real.md"), filepath.Join(root, "link.md")); err != nil {
		t.Fatal(err)
	}
	got, err := resolveUnderRoot(root, "link.md")
	if err != nil {
		t.Fatalf("in-worktree symlink rejected: %v", err)
	}
	data, err := os.ReadFile(got)
	if err != nil || string(data) != "ok" {
		t.Fatalf("read linked file: %v %q", err, data)
	}
}

func TestReadFileRejectsOversized(t *testing.T) {
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	root := t.TempDir()
	big := strings.Repeat("x", 2*1024*1024)
	if err := os.WriteFile(filepath.Join(root, "big.md"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readFile(root, "big.md"); !errors.Is(err, errFileTooLarge) {
		t.Fatalf("oversized file err=%v, want errFileTooLarge", err)
	}
}

func TestRemoveCloneStorageDeletesOnlyDataSourceDir(t *testing.T) {
	t.Setenv(localStorageBaseEnv, t.TempDir())
	dir := CloneStorageDir(7, "ds-clean")
	if dir == "" {
		t.Fatal("expected clone dir")
	}
	if err := os.MkdirAll(filepath.Join(dir, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := RemoveCloneStorage(7, "ds-clean"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("clone dir still present: %v", err)
	}
	if got := CloneStorageDir(7, "../escape"); got != "" {
		t.Fatalf("path-like dsID must be rejected, got %q", got)
	}
}
