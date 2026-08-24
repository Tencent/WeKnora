package git_repo

import (
	"net/url"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// webhookRepoKey reduces a repository URL to a comparison key: lowercased
// host + path with the .git suffix and trailing slashes stripped. Webhook
// payload URLs legitimately differ from the configured repo_url in scheme
// (http vs https) and .git suffix (GitLab sends .../repo.git while users
// often configure .../repo), so exact normalizeRepoURL equality would miss
// valid matches. Absolute local paths (tests) are compared as-is.
func webhookRepoKey(raw string) (string, bool) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", false
	}
	if filepathIsAbs(v) {
		return path.Clean(v), true
	}
	// Cursor / ExternalID keys are stored as host/path without a scheme.
	if !strings.Contains(v, "://") {
		if !strings.Contains(v, ".") || !strings.Contains(v, "/") {
			return "", false
		}
		p := strings.TrimSuffix(strings.TrimSuffix(v, "/"), ".git")
		return strings.ToLower(p), true
	}
	u, err := url.Parse(v)
	if err != nil || u.Host == "" {
		return "", false
	}
	p := strings.TrimSuffix(u.Path, "/")
	p = strings.TrimSuffix(p, ".git")
	p = strings.TrimSuffix(p, "/")
	if p == "" {
		return "", false
	}
	// Host and path are folded: GitHub (and gitlab.com) treat owner/repo as
	// case-insensitive. A user-typed lowercase URL must still match the
	// canonical clone_url in the webhook payload.
	return strings.ToLower(u.Host) + strings.ToLower(p), true
}

// repoIdentity is the scheme-stable id used in ExternalIDs and clone-dir
// hashes so http/https / .git spelling changes do not fork the document set.
func repoIdentity(url string) string {
	if key, ok := webhookRepoKey(url); ok {
		return key
	}
	return strings.TrimSpace(url)
}

func repoCursorKey(url, branch string) string {
	return repoIdentity(url) + "\x00" + branch
}

// MatchPush reports whether a push event for repoURL on branch should trigger
// a sync of ds: at least one configured repo selection matches the repository
// URL, and the selection's branch filter (when set) includes the pushed
// branch. branch is the ref with any refs/heads/ prefix already stripped;
// selections with an empty branch follow the remote default and match any
// push (a no-op sync costs one fetch). Config parse failures never match —
// a broken data source should not be webhook-triggered.
func MatchPush(ds *types.DataSourceConfig, repoURL, branch string) bool {
	if ds == nil {
		return false
	}
	cfg, err := parseConfig(ds)
	if err != nil {
		return false
	}
	key, ok := webhookRepoKey(repoURL)
	if !ok {
		return false
	}
	for _, sel := range cfg.Repos {
		selKey, ok := webhookRepoKey(sel.RepoURL)
		if !ok || selKey != key {
			continue
		}
		if strings.TrimSpace(sel.Branch) == "" || strings.TrimSpace(sel.Branch) == branch {
			return true
		}
	}
	return false
}
