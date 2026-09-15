package meetingorchestration

import (
	"strings"
	"testing"
)

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

func TestEmbeddedMeetingPromptsContainContractGuidance(t *testing.T) {
	bundle := NewPromptBundle("")
	snapshot, err := bundle.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"meeting-item-candidate-v1.txt",
		"meeting-topic-cluster-matching-v1.txt",
		"meeting-work-item-matching-v1.txt",
		"meeting-work-item-update-v1.txt",
		"meeting-knowledge-selection-v1.txt",
		"meeting-topic-relation-v1.txt",
	} {
		if len(snapshot.Prompts[name]) < 500 {
			t.Fatalf("embedded prompt %q is missing its contract guidance", name)
		}
	}
	update := snapshot.Prompts["meeting-work-item-update-v1.txt"]
	for _, marker := range []string{"current_status", "todo_match_decision", "evidence_ids", "不得改写旧历史"} {
		if !strings.Contains(update, marker) {
			t.Fatalf("meeting update prompt missing %q", marker)
		}
	}
}
