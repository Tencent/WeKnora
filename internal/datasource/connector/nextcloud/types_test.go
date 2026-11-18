package nextcloud

import (
	"errors"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func allowTestServer(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,nextcloud.example.com")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

func TestConfig(t *testing.T) {
	allowTestServer(t)
	ds := &types.DataSourceConfig{Credentials: map[string]any{
		"base_url": " nextcloud.example.com/cloud/// ", "username": "alice",
		"app_password": "app-password-for-tests",
	}, Settings: map[string]any{"include_extensions": ".PDF, md", "exclude_extensions": []any{"md"}}}
	cfg, err := parseConfig(ds)
	if err != nil || cfg.baseURL != "https://nextcloud.example.com/cloud" {
		t.Fatalf("config = %+v, %v", cfg.baseURL, err)
	}
	if !cfg.includes("/说明.PDF") || cfg.includes("/notes.md") || cfg.includes("/archive.zip") {
		t.Fatal("extension filters must intersect supported formats, with exclusions winning")
	}
	for _, key := range []string{"base_url", "username", "app_password"} {
		previous := ds.Credentials[key]
		ds.Credentials[key] = ""
		if _, err := parseConfig(ds); !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("missing %s = %v", key, err)
		}
		ds.Credentials[key] = previous
	}
	for _, base := range []string{
		"http://192.0.2.1", "http://localhost", "ftp://nextcloud.example.com",
		"https://alice:app-password-for-tests@nextcloud.example.com", "https://nextcloud.example.com/?x=1",
		"https://nextcloud.example.com/#x", "https://nextcloud.example.com/../cloud",
		"https://nextcloud.example.com/remote.php/dav/files/alice",
	} {
		ds.Credentials["base_url"] = base
		if _, err := parseConfig(ds); !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("unsafe base URL accepted: %v", err)
		}
	}
}

func TestPathsAndRoots(t *testing.T) {
	roots, err := collapseRoots([]string{"/docs/a", "/docs", "/documents", "/docs"})
	if err != nil || !slices.Equal(roots, []string{"/docs", "/documents"}) {
		t.Fatalf("roots = %v, %v", roots, err)
	}
	for _, p := range []string{"relative", "/../x", "/a/./b", "/a\\b", "/a\x00b"} {
		if _, err := normalizePath(p); err == nil {
			t.Fatalf("unsafe path accepted: %q", p)
		}
	}
	if _, err := collapseRoots(nil); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("empty selection = %v", err)
	}
	if got, _ := normalizePath("//产品//100% 文档/"); got != "/产品/100% 文档" {
		t.Fatalf("normalized path = %q", got)
	}
}
