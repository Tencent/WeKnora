package config

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestFindPromptTemplateByIDStaysWithinManagedSurface(t *testing.T) {
	protocols := []PromptTemplate{{ID: "protocol", Content: "agent protocol"}}
	rewrites := []PromptTemplate{{ID: "rewrite", Content: "rewrite protocol"}}

	if got := FindPromptTemplateByID(protocols, "protocol"); got == nil || got.Content != "agent protocol" {
		t.Fatalf("managed protocol lookup failed: %#v", got)
	}
	if got := FindPromptTemplateByID(protocols, rewrites[0].ID); got != nil {
		t.Fatalf("cross-surface prompt lookup succeeded: %#v", got)
	}
}

func TestLoadPromptTemplatesLoadsResponseModeCatalog(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve test file path")
	}
	configDir := filepath.Join(filepath.Dir(currentFile), "..", "..", "config")
	templates, err := loadPromptTemplates(configDir)
	if err != nil {
		t.Fatal(err)
	}
	if templates == nil {
		t.Fatal("prompt templates were not loaded")
	}
	for _, id := range []string{"greeting", "knowledge_base_unavailable", "retrieval_sources_unavailable"} {
		if FindPromptTemplateByID(templates.ResponseModePrompts, id) == nil {
			t.Fatalf("managed response-mode prompt %q was not loaded", id)
		}
	}
}
