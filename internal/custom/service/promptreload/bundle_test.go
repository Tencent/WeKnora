package promptreload

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleUsesFallbackAndReloadsAtomically(t *testing.T) {
	dir := t.TempDir()
	fallback := map[string]string{"a.txt": "fallback-a", "b.txt": "fallback-b"}
	bundle := New(dir, fallback)

	snapshot, err := bundle.Load()
	if err == nil || snapshot.External {
		// A configured directory without files is an invalid external bundle;
		// fallback remains active but the error must be observable.
		t.Fatalf("expected fallback with observable missing-file error, snapshot=%#v err=%v", snapshot, err)
	}
	if snapshot.Prompts["a.txt"] != "fallback-a" {
		t.Fatalf("fallback prompt not retained: %#v", snapshot.Prompts)
	}

	writePromptFile(t, dir, "a.txt", "external-a")
	writePromptFile(t, dir, "b.txt", "external-b")
	snapshot, err = bundle.Load()
	if err != nil || !snapshot.External || snapshot.Prompts["a.txt"] != "external-a" {
		t.Fatalf("external bundle not activated: snapshot=%#v err=%v", snapshot, err)
	}
	firstVersion := snapshot.Version

	writePromptFile(t, dir, "a.txt", "external-a-2")
	// The retained snapshot is the task boundary; an in-flight caller keeps
	// using its copy until it explicitly asks the bundle for a new snapshot.
	if snapshot.Prompts["a.txt"] != "external-a" {
		t.Fatalf("task snapshot changed unexpectedly: %#v", snapshot.Prompts)
	}
	snapshot, err = bundle.Load()
	if err != nil || snapshot.Version == firstVersion || snapshot.Prompts["a.txt"] != "external-a-2" {
		t.Fatalf("complete bundle change not reloaded: snapshot=%#v err=%v", snapshot, err)
	}

	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("   "), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err = bundle.Load()
	if err == nil || snapshot.Prompts["a.txt"] != "external-a-2" || snapshot.Prompts["b.txt"] != "external-b" {
		t.Fatalf("invalid partial bundle should retain previous snapshot: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestBundleWithoutDirectoryIsDeterministic(t *testing.T) {
	bundle := New("", map[string]string{"a.txt": "  fallback-a  "})
	one, err := bundle.Load()
	if err != nil {
		t.Fatal(err)
	}
	two, err := bundle.Load()
	if err != nil || one.Version != two.Version || one.Prompts["a.txt"] != "fallback-a" {
		t.Fatalf("fallback bundle is not stable: one=%#v two=%#v err=%v", one, two, err)
	}
}

func TestBundleValidatorRejectsNewVersionWithoutSwitching(t *testing.T) {
	dir := t.TempDir()
	bundle := New(dir, map[string]string{"a.txt": "fallback-a"})
	bundle.Validate = func(prompts map[string]string) error {
		if prompts["a.txt"] == "bad" {
			return os.ErrInvalid
		}
		return nil
	}
	writePromptFile(t, dir, "a.txt", "good")
	snapshot, err := bundle.Load()
	if err != nil || snapshot.Prompts["a.txt"] != "good" {
		t.Fatalf("valid version was not activated: snapshot=%#v err=%v", snapshot, err)
	}
	writePromptFile(t, dir, "a.txt", "bad")
	snapshot, err = bundle.Load()
	if err == nil || snapshot.Prompts["a.txt"] != "good" {
		t.Fatalf("validator failure should retain previous version: snapshot=%#v err=%v", snapshot, err)
	}
}

func TestBundleLoadsPromptsAndSchemasAsOneVersion(t *testing.T) {
	dir := t.TempDir()
	bundle := NewWithSchemas(dir, map[string]string{"a.txt": "fallback-a"}, map[string]string{"a.schema.json": `{"type":"object"}`})
	bundle.ValidateSchemas = func(schemas map[string]string) error {
		if schemas["a.schema.json"] == "" {
			return os.ErrInvalid
		}
		return nil
	}
	writePromptFile(t, dir, "a.txt", "external-a")
	writePromptFile(t, dir, "a.schema.json", `{"type":"object"}`)
	snapshot, err := bundle.Load()
	if err != nil || !snapshot.External || snapshot.Schemas["a.schema.json"] == "" {
		t.Fatalf("complete prompt/schema package was not activated: snapshot=%#v err=%v", snapshot, err)
	}
	firstVersion := snapshot.Version
	if err := os.WriteFile(filepath.Join(dir, "a.schema.json"), []byte(`{"type":`), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle.ValidateSchemas = func(schemas map[string]string) error {
		if schemas["a.schema.json"] == `{"type":` {
			return os.ErrInvalid
		}
		return nil
	}
	retained, err := bundle.Load()
	if err == nil || retained.Version != firstVersion || retained.Schemas["a.schema.json"] != `{"type":"object"}` {
		t.Fatalf("invalid schema package should retain previous snapshot: snapshot=%#v err=%v", retained, err)
	}
}

func writePromptFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
