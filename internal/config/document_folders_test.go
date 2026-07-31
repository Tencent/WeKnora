package config

import "testing"

func TestDocumentFoldersEnabledDefaultsFailClosed(t *testing.T) {
	if DocumentFoldersEnabled(nil) {
		t.Fatal("nil config must keep document folders disabled")
	}
	if DocumentFoldersEnabled(&Config{}) {
		t.Fatal("missing knowledge_base config must keep document folders disabled")
	}

	cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{DocumentFoldersEnabled: true}}
	if !DocumentFoldersEnabled(cfg) {
		t.Fatal("explicitly enabled document folders should be available")
	}
}
