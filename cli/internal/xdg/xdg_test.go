package xdg_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/xdg"
)

func TestPath_HonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := xdg.Path("XDG_CONFIG_HOME", ".config", "config.yaml")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(dir, "weknora", "config.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPath_FallsBackToHome(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "")
	got, err := xdg.Path("XDG_CACHE_HOME", ".cache", "server-info.yaml")
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	if !strings.Contains(got, ".cache") || !strings.Contains(got, "weknora") {
		t.Errorf("expected ~/.cache/weknora prefix, got %q", got)
	}
}

func TestWriteAtomicYAML_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "file.yaml")
	type doc struct {
		Name string `yaml:"name"`
	}
	if err := xdg.WriteAtomicYAML(p, &doc{Name: "alice"}); err != nil {
		t.Fatalf("WriteAtomicYAML: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(p)
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("mode = %v, want 0600", mode)
		}
	}
}
