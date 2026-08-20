package git_repo

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func matchPushConfig(t *testing.T, repos ...map[string]interface{}) *types.DataSourceConfig {
	t.Helper()
	items := make([]interface{}, 0, len(repos))
	for _, r := range repos {
		items = append(items, r)
	}
	return &types.DataSourceConfig{Settings: map[string]interface{}{"repos": items}}
}

func TestMatchPushURLVariants(t *testing.T) {
	// The configured repo_url must pass SSRF validation (normalizeRepoURL), so
	// the fixture uses a real public host. Payload URLs only go through the
	// scheme/host/path comparison key and can carry any form — the SSRF guard
	// applies to the configured clone target, not to webhook event payloads.
	ds := matchPushConfig(t, map[string]interface{}{"repo_url": "https://github.com/org/blog.git"})
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"exact", "https://github.com/org/blog.git", true},
		{"scheme variant", "http://github.com/org/blog.git", true},
		{"no .git suffix", "https://github.com/org/blog", true},
		{"trailing slash", "https://github.com/org/blog.git/", true},
		{"other project", "https://github.com/org/other.git", false},
		{"other host", "https://gitlab.com/org/blog.git", false},
		{"garbage", "not a url", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := MatchPush(ds, tc.payload, "main"); got != tc.want {
			t.Errorf("%s: MatchPush(%q) = %v, want %v", tc.name, tc.payload, got, tc.want)
		}
	}

	// Host comparison is case-insensitive in both configured and payload URLs.
	mixed := matchPushConfig(t, map[string]interface{}{"repo_url": "https://GitHub.com/org/blog.git"})
	if !MatchPush(mixed, "https://github.com/org/blog.git", "main") {
		t.Error("host case-insensitive match failed")
	}
}

func TestMatchPushBranchFilter(t *testing.T) {
	ds := matchPushConfig(t, map[string]interface{}{
		"repo_url": "https://github.com/org/blog.git", "branch": "release/1.0",
	})
	if !MatchPush(ds, "https://github.com/org/blog.git", "release/1.0") {
		t.Fatal("configured branch should match")
	}
	if MatchPush(ds, "https://github.com/org/blog.git", "main") {
		t.Fatal("other branch should not match a branch-filtered selection")
	}

	// Empty branch = follow remote default: matches any push (sync no-ops if
	// the default branch itself did not move).
	follow := matchPushConfig(t, map[string]interface{}{"repo_url": "https://github.com/org/blog.git"})
	if !MatchPush(follow, "https://github.com/org/blog.git", "any-branch") {
		t.Fatal("empty selection branch should match any push")
	}
}

func TestMatchPushInvalidConfig(t *testing.T) {
	bad := &types.DataSourceConfig{Settings: map[string]interface{}{"repos": "not-an-array"}}
	if MatchPush(bad, "https://github.com/org/blog.git", "main") {
		t.Fatal("invalid config must never match")
	}
	if MatchPush(nil, "https://github.com/org/blog.git", "main") {
		t.Fatal("nil config must never match")
	}
}
