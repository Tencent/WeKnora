package gitlab

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseConfigCollapsesDirectories(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{Settings: map[string]interface{}{
		"projects": []interface{}{map[string]interface{}{
			"project_id": "team/docs", "ref": "main",
			"paths": []interface{}{"docs/guide", "docs", "docs"},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects) != 1 || len(cfg.Projects[0].Paths) != 1 || cfg.Projects[0].Paths[0] != "docs" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}

func TestParseConfigRootMeansWholeProject(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{Settings: map[string]interface{}{
		"projects": []interface{}{map[string]interface{}{"project_id": "42", "paths": []interface{}{"/"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Projects[0].Paths) != 0 {
		t.Fatalf("root path must collapse to whole project: %#v", cfg.Projects[0].Paths)
	}
}

func TestNormalizePathRejectsTraversal(t *testing.T) {
	if _, err := normalizePath("docs/../secrets"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestKnowledgeRelativePathPreservesRepositoryTreeBelowProjectAndBranch(t *testing.T) {
	got := knowledgeRelativePath("knowledge", "feature/login", "docs/guide/install.md")
	if got != "knowledge-feature-login/docs/guide/install.md" {
		t.Fatalf("knowledge relative path = %q", got)
	}
}
