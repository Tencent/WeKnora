package git_repo

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseConfigValid(t *testing.T) {
	ds := &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"repos": []interface{}{
				map[string]interface{}{
					"repo_url": "https://github.com/org/blog.git",
					"branch":   "main",
					"paths":    []interface{}{"docs/", "README.md"},
				},
			},
		},
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("want 1 repo, got %d", len(cfg.Repos))
	}
	r := cfg.Repos[0]
	if r.RepoURL != "https://github.com/org/blog" {
		t.Fatalf("repo_url = %q", r.RepoURL)
	}
	if r.Branch != "main" {
		t.Fatalf("branch = %q", r.Branch)
	}
	if len(r.Paths) != 2 || r.Paths[0] != "README.md" || r.Paths[1] != "docs" {
		t.Fatalf("paths = %+v (collapsePaths sorts them), want [README.md docs]", r.Paths)
	}
}

func TestParseConfigErrors(t *testing.T) {
	settingsWith := func(repos interface{}) map[string]interface{} {
		return map[string]interface{}{"repos": repos}
	}
	cases := []struct {
		name string
		ds   *types.DataSourceConfig
	}{
		{"nil settings", &types.DataSourceConfig{Settings: map[string]interface{}{}}},
		{"repos not array", &types.DataSourceConfig{Settings: map[string]interface{}{"repos": "x"}}},
		{"empty repos", &types.DataSourceConfig{Settings: settingsWith([]interface{}{})}},
		{"missing repo_url", &types.DataSourceConfig{
			Settings: settingsWith([]interface{}{map[string]interface{}{"branch": "main"}}),
		}},
		{"duplicate repo_url", &types.DataSourceConfig{
			Settings: settingsWith([]interface{}{
				map[string]interface{}{"repo_url": "https://github.com/org/a.git"},
				map[string]interface{}{"repo_url": "https://github.com/org/a.git"},
			}),
		}},
		{"file scheme", &types.DataSourceConfig{
			Settings: settingsWith([]interface{}{map[string]interface{}{"repo_url": "file:///etc/passwd"}}),
		}},
		{"ssh scheme", &types.DataSourceConfig{
			Settings: settingsWith([]interface{}{map[string]interface{}{"repo_url": "git@github.com:org/blog.git"}}),
		}},
		{"embedded userinfo", &types.DataSourceConfig{
			Settings: settingsWith([]interface{}{map[string]interface{}{
				"repo_url": "https://user:pass@github.com/org/blog.git",
			}}),
		}},
		{"path traversal", &types.DataSourceConfig{
			Settings: settingsWith([]interface{}{map[string]interface{}{
				"repo_url": "https://github.com/org/blog.git",
				"paths":    []interface{}{"../outside"},
			}}),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseConfig(tc.ds); err == nil {
				t.Fatalf("parseConfig should fail for %q", tc.name)
			}
		})
	}
}

func TestParseConfigRejectsLocalAbsolutePath(t *testing.T) {
	allowLocalRepoURL = false
	ds := &types.DataSourceConfig{Settings: map[string]interface{}{
		"repos": []interface{}{map[string]interface{}{"repo_url": "/tmp/repos/blog.git"}},
	}}
	if _, err := parseConfig(ds); err == nil {
		t.Fatal("local absolute path must be rejected in production")
	}
}

func TestParseConfigAllowsLocalAbsolutePathWhenEnabled(t *testing.T) {
	allowLocalRepoURL = true
	t.Cleanup(func() { allowLocalRepoURL = false })
	ds := &types.DataSourceConfig{Settings: map[string]interface{}{
		"repos": []interface{}{map[string]interface{}{"repo_url": "/tmp/repos/blog.git"}},
	}}
	cfg, err := parseConfig(ds)
	if err != nil {
		t.Fatalf("local absolute path should be allowed when enabled: %v", err)
	}
	if cfg.Repos[0].RepoURL != "/tmp/repos/blog.git" {
		t.Fatalf("repo_url = %q", cfg.Repos[0].RepoURL)
	}
}

func TestParseConfigNormalizesGitHTTPS(t *testing.T) {
	ds := &types.DataSourceConfig{Settings: map[string]interface{}{
		"repos": []interface{}{map[string]interface{}{"repo_url": "git+https://github.com/org/blog.git"}},
	}}
	cfg, err := parseConfig(ds)
	if err != nil {
		t.Fatalf("git+https should be accepted: %v", err)
	}
	if cfg.Repos[0].RepoURL != "https://github.com/org/blog" {
		t.Fatalf("normalized repo_url = %q", cfg.Repos[0].RepoURL)
	}
}

func TestParseConfigCollapsesNestedPaths(t *testing.T) {
	ds := &types.DataSourceConfig{Settings: map[string]interface{}{
		"repos": []interface{}{map[string]interface{}{
			"repo_url": "https://github.com/org/blog.git",
			"paths":    []interface{}{"docs", "docs/guides"},
		}},
	}}
	cfg, err := parseConfig(ds)
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	// "docs/guides" is under "docs" and is collapsed away.
	if len(cfg.Repos[0].Paths) != 1 || cfg.Repos[0].Paths[0] != "docs" {
		t.Fatalf("collapsed paths = %+v, want [docs]", cfg.Repos[0].Paths)
	}
}

func TestValidateMissingReposFails(t *testing.T) {
	if _, err := parseConfig(nil); err == nil {
		t.Fatal("nil config should fail")
	}
}
