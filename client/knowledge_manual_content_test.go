package client

import (
	"encoding/json"
	"testing"
)

func TestKnowledgeManualContent(t *testing.T) {
	k := &Knowledge{Metadata: json.RawMessage("{\"content\":\"# hello\",\"format\":\"markdown\"}")}
	got, err := k.ManualContent()
	if err != nil || got != "# hello" {
		t.Fatalf("ManualContent() = %q, %v", got, err)
	}

	empty, err := (&Knowledge{}).ManualContent()
	if err != nil || empty != "" {
		t.Fatalf("empty ManualContent() = %q, %v", empty, err)
	}

	if _, err := (&Knowledge{Metadata: json.RawMessage("{")}).ManualContent(); err == nil {
		t.Fatal("expected invalid metadata to fail")
	}
}

func TestUpdateManualKnowledgeRequestCarriesStatus(t *testing.T) {
	body, err := json.Marshal(UpdateManualKnowledgeRequest{
		Title: "t", Content: "c", Status: "publish",
	})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["status"] != "publish" || fields["title"] != "t" || fields["content"] != "c" {
		t.Fatalf("payload = %s", body)
	}
}
