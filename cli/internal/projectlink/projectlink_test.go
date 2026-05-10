package projectlink_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/projectlink"
)

func TestDiscover_FoundInCwd(t *testing.T) {
	dir := t.TempDir()
	mkLink(t, dir)
	got, found, err := projectlink.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !found {
		t.Fatalf("expected found=true")
	}
	want := filepath.Join(dir, ".weknora", "project.yaml")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDiscover_FoundInParent(t *testing.T) {
	root := t.TempDir()
	mkLink(t, root)
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got, found, err := projectlink.Discover(deep)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if !found {
		t.Fatal("expected found=true")
	}
	if filepath.Dir(filepath.Dir(got)) != root {
		t.Errorf("expected discovered file under %q, got %q", root, got)
	}
}

func TestDiscover_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, found, err := projectlink.Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if found {
		t.Fatal("expected found=false in empty tree")
	}
}

func TestLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".weknora", "project.yaml")
	in := &projectlink.Project{Context: "prod", KBID: "kb_abc", CreatedAt: time.Now().UTC().Round(time.Second)}
	if err := projectlink.Save(path, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := projectlink.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.Context != in.Context || out.KBID != in.KBID {
		t.Errorf("round trip: got %+v want %+v", out, in)
	}
}

func TestLoad_Corrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".weknora", "project.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not: yaml: : :"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := projectlink.Load(path)
	if err == nil {
		t.Fatal("expected error for corrupt yaml")
	}
}

func TestSave_CreatesParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".weknora", "project.yaml")
	if err := projectlink.Save(path, &projectlink.Project{KBID: "kb_x"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".weknora")); err != nil {
		t.Errorf("parent .weknora not created: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func mkLink(t *testing.T, dir string) {
	t.Helper()
	d := filepath.Join(dir, ".weknora")
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "project.yaml"), []byte("kb_id: kb_test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
