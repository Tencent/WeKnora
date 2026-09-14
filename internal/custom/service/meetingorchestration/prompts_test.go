package meetingorchestration

import "testing"

func TestNewPromptBundleEmbedsCompletePromptAndSchemaPackage(t *testing.T) {
	bundle := NewPromptBundle("")
	snapshot, err := bundle.Load()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.External {
		t.Fatal("empty prompt directory should use the embedded package")
	}
	if len(snapshot.Prompts) != 6 || len(snapshot.Schemas) != 6 {
		t.Fatalf("embedded package is incomplete: prompts=%d schemas=%d", len(snapshot.Prompts), len(snapshot.Schemas))
	}
	if snapshot.Version == "" {
		t.Fatal("embedded package has no content fingerprint")
	}
}
