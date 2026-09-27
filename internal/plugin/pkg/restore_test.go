package pkg

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// An extracted package is put back as it was: files changed or removed
// since are written again, and files planted there are removed.
func TestRestore(t *testing.T) {
	p, err := Open(zipOf(t, map[string]string{
		"plugin.yaml":             kitManifest,
		"config/tenant.yaml":      tenantSchema,
		"skills/triage/SKILL.md":  "---\nname: triage\n---\nTriage issues.",
		"skills/triage/notes.txt": "x",
	}))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	changed, err := Restore(p, dir)
	if err != nil || len(changed) != 4 {
		t.Fatalf("first restore wrote %v, %v", changed, err)
	}
	if changed, err := Restore(p, dir); err != nil || len(changed) != 0 {
		t.Fatalf("an intact package changed %v, %v", changed, err)
	}

	// A temp cleaner took the manifest, someone rewrote a file and planted
	// others, a directory included.
	_ = os.Remove(filepath.Join(dir, "plugin.yaml"))
	_ = os.WriteFile(filepath.Join(dir, "skills/triage/notes.txt"), []byte("evil"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "sitecustomize.py"), []byte("import os"), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "vendor/evil"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "vendor/evil/__init__.py"), []byte(""), 0o644)
	changed, err = Restore(p, dir)
	want := []string{"plugin.yaml", "sitecustomize.py", "skills/triage/notes.txt", "vendor"}
	if err != nil || !slices.Equal(changed, want) {
		t.Fatalf("restore changed %v, %v; want %v", changed, err, want)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "skills/triage/notes.txt")); string(b) != "x" {
		t.Fatalf("notes.txt = %q", b)
	}
	for _, gone := range []string{"sitecustomize.py", "vendor"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Fatalf("%s survived: %v", gone, err)
		}
	}
}
