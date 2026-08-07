package utils

import (
	"path/filepath"
	"testing"
)

func TestDiskFreeBytesExistingDir(t *testing.T) {
	free, err := DiskFreeBytes(t.TempDir())
	if err != nil {
		t.Fatalf("DiskFreeBytes on temp dir: %v", err)
	}
	if free == 0 {
		t.Fatal("expected non-zero free bytes on temp dir volume")
	}
}

func TestDiskFreeBytesWalksUpToExistingAncestor(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "does", "not", "exist", "yet")
	free, err := DiskFreeBytes(deep)
	if err != nil {
		t.Fatalf("DiskFreeBytes should walk up to an existing ancestor: %v", err)
	}
	if free == 0 {
		t.Fatal("expected non-zero free bytes after walk-up")
	}
}

func TestDiskFreeBytesEmptyPath(t *testing.T) {
	// Empty path probes the working directory's volume; must not error.
	if _, err := DiskFreeBytes("   "); err != nil {
		t.Fatalf("DiskFreeBytes with blank path: %v", err)
	}
}

func TestLocalStorageFreeBytes(t *testing.T) {
	t.Setenv("LOCAL_STORAGE_BASE_DIR", t.TempDir())
	free, err := LocalStorageFreeBytes()
	if err != nil {
		t.Fatalf("LocalStorageFreeBytes: %v", err)
	}
	if free <= 0 {
		t.Fatal("expected positive free bytes")
	}
}

func TestLocalStorageBaseDirDefault(t *testing.T) {
	t.Setenv("LOCAL_STORAGE_BASE_DIR", "")
	if got := LocalStorageBaseDir(); got != "/data/files" {
		t.Fatalf("expected container default /data/files, got %q", got)
	}
}
