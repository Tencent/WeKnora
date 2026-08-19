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
	ds := matchPushConfig(t, map[string]interface{}{"repo_url": "http://192.168.0.200/lurj/knowledge.git"})
	cases := []struct {
		name    string
		payload string
		want    bool
	}{
		{"exact", "http://192.168.0.200/lurj/knowledge.git", true},
		{"https variant", "https://192.168.0.200/lurj/knowledge.git", true},
		{"no .git suffix", "http://192.168.0.200/lurj/knowledge", true},
		{"trailing slash", "http://192.168.0.200/lurj/knowledge.git/", true},
		{"other project", "http://192.168.0.200/lurj/other.git", false},
		{"other host", "http://192.168.0.201/lurj/knowledge.git", false},
		{"garbage", "not a url", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := MatchPush(ds, tc.payload, "main"); got != tc.want {
			t.Errorf("%s: MatchPush(%q) = %v, want %v", tc.name, tc.payload, got, tc.want)
		}
	}

	// Host comparison is case-insensitive in both configured and payload URLs.
	mixed := matchPushConfig(t, map[string]interface{}{"repo_url": "http://GitLab.Example.COM/lurj/knowledge.git"})
	if !MatchPush(mixed, "http://gitlab.example.com/lurj/knowledge.git", "main") {
		t.Error("host case-insensitive match failed")
	}
}

func TestMatchPushBranchFilter(t *testing.T) {
	ds := matchPushConfig(t, map[string]interface{}{"repo_url": "http://x/repo.git", "branch": "release/1.0"})
	if !MatchPush(ds, "http://x/repo.git", "release/1.0") {
		t.Fatal("configured branch should match")
	}
	if MatchPush(ds, "http://x/repo.git", "main") {
		t.Fatal("other branch should not match a branch-filtered selection")
	}

	// Empty branch = follow remote default: matches any push (sync no-ops if
	// the default branch itself did not move).
	follow := matchPushConfig(t, map[string]interface{}{"repo_url": "http://x/repo.git"})
	if !MatchPush(follow, "http://x/repo.git", "any-branch") {
		t.Fatal("empty selection branch should match any push")
	}
}

func TestMatchPushInvalidConfig(t *testing.T) {
	bad := &types.DataSourceConfig{Settings: map[string]interface{}{"repos": "not-an-array"}}
	if MatchPush(bad, "http://x/repo.git", "main") {
		t.Fatal("invalid config must never match")
	}
	if MatchPush(nil, "http://x/repo.git", "main") {
		t.Fatal("nil config must never match")
	}
}
