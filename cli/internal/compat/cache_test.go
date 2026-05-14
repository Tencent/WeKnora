package compat_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/compat"
)

func TestCache_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	want := &compat.Info{ServerVersion: "1.2.3", ProbedAt: time.Now().UTC().Truncate(time.Second)}
	if err := compat.SaveCache(want); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	got, fresh, err := compat.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if !fresh {
		t.Error("just-saved cache should be fresh")
	}
	if got == nil || got.ServerVersion != want.ServerVersion {
		t.Errorf("ServerVersion = %v, want %q", got, want.ServerVersion)
	}

	// File should live at $XDG_CACHE_HOME/weknora/server-info.yaml
	p := filepath.Join(dir, "weknora", "server-info.yaml")
	if _, err := filepath.Abs(p); err != nil {
		t.Errorf("path resolution: %v", err)
	}
}

func TestCache_Stale(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", dir)

	old := &compat.Info{ServerVersion: "1.2.3", ProbedAt: time.Now().Add(-25 * time.Hour)}
	if err := compat.SaveCache(old); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	_, fresh, err := compat.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if fresh {
		t.Error("25h old cache should be stale")
	}
}

func TestCache_Missing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	info, fresh, err := compat.LoadCache()
	if err != nil {
		t.Fatalf("LoadCache should not error on missing: %v", err)
	}
	if fresh {
		t.Error("missing cache must not be fresh")
	}
	if info != nil {
		t.Errorf("missing cache info should be nil, got %v", info)
	}
}
