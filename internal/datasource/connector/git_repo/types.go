package git_repo

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// repoSelection selects a single git repository (or a subdirectory of it) to
// sync. RepoURL is the git remote URL (https/http for production; absolute
// local paths are accepted for tests and local checkouts). Branch is optional —
// empty means "follow the remote default branch". Paths optionally restricts
// the sync to the listed repository-relative directories.
type repoSelection struct {
	RepoURL string   `json:"repo_url"`
	Branch  string   `json:"branch"`
	Paths   []string `json:"paths"`
}

type config struct {
	Repos []repoSelection `json:"repos"`
}

// parseConfig validates and normalizes the git_repo connector settings. The
// shape mirrors the GitLab connector's settings-driven configuration: secret
// auth lives in credentials (access_token), the selection lives in settings.
func parseConfig(ds *types.DataSourceConfig) (*config, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	raw, ok := ds.Settings["repos"]
	if !ok {
		return nil, fmt.Errorf("%w: settings.repos is required", datasource.ErrInvalidConfig)
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: settings.repos must be an array", datasource.ErrInvalidConfig)
	}
	out := &config{Repos: make([]repoSelection, 0, len(items))}
	seen := map[string]bool{}
	for _, rawRepo := range items {
		m, ok := rawRepo.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: invalid repo selection", datasource.ErrInvalidConfig)
		}
		rawURL, _ := m["repo_url"].(string)
		normalized, err := normalizeRepoURL(rawURL)
		if err != nil {
			return nil, err
		}
		if seen[normalized] {
			return nil, fmt.Errorf("%w: repo_url must be unique (duplicate: %s)", datasource.ErrInvalidConfig, rawURL)
		}
		seen[normalized] = true
		branch, _ := m["branch"].(string)
		r := repoSelection{RepoURL: normalized, Branch: strings.TrimSpace(branch)}
		if rawPaths, exists := m["paths"]; exists {
			values, ok := rawPaths.([]interface{})
			if !ok {
				return nil, fmt.Errorf("%w: paths must be an array", datasource.ErrInvalidConfig)
			}
			for _, value := range values {
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("%w: path must be a string", datasource.ErrInvalidConfig)
				}
				normalizedPath, err := normalizePath(s)
				if err != nil {
					return nil, err
				}
				r.Paths = append(r.Paths, normalizedPath)
			}
		}
		r.Paths = collapsePaths(r.Paths)
		out.Repos = append(out.Repos, r)
	}
	if len(out.Repos) == 0 {
		return nil, fmt.Errorf("%w: at least one repo is required", datasource.ErrInvalidConfig)
	}
	return out, nil
}

// normalizeRepoURL validates a repo URL and normalizes it so the same
// repository cannot be configured twice under equivalent spellings. Only
// http/https remotes are accepted for real syncs (no embedded userinfo —
// credentials go in the access_token credential field); an absolute local path
// is allowed so tests can clone from a throwaway bare repo and local checkouts
// can be synced directly.
func normalizeRepoURL(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", fmt.Errorf("%w: repo_url is required", datasource.ErrInvalidConfig)
	}
	if filepathIsAbs(v) {
		return path.Clean(v), nil
	}
	u, err := url.Parse(v)
	if err != nil {
		return "", fmt.Errorf("%w: invalid repo_url %q", datasource.ErrInvalidConfig, raw)
	}
	switch u.Scheme {
	case "http", "https":
	case "git+http", "git+https":
		u.Scheme = strings.TrimPrefix(u.Scheme, "git+")
	default:
		return "", fmt.Errorf("%w: repo_url scheme %q not supported (use http/https)", datasource.ErrInvalidConfig, u.Scheme)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: repo_url must not embed credentials; use the access_token credential", datasource.ErrInvalidConfig)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: repo_url must include a host", datasource.ErrInvalidConfig)
	}
	u.Fragment = ""
	u.RawQuery = ""
	return u.String(), nil
}

// filepathIsAbs reports whether v is an absolute filesystem path, without
// importing path/filepath in this small package (kept dependency-light).
func filepathIsAbs(v string) bool {
	return strings.HasPrefix(v, "/") || strings.HasPrefix(v, "\\") ||
		(len(v) >= 2 && v[1] == ':')
}

func normalizePath(value string) (string, error) {
	v := strings.Trim(strings.TrimSpace(value), "/")
	if v == "" {
		return "", nil
	}
	if strings.Contains(v, "\\") {
		return "", fmt.Errorf("%w: path must use forward slashes", datasource.ErrInvalidConfig)
	}
	if path.Clean(v) != v || v == "." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") {
		return "", fmt.Errorf("%w: invalid repository path", datasource.ErrInvalidConfig)
	}
	return v, nil
}

func collapsePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	for _, p := range paths {
		if p == "" {
			return nil
		}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if len(out) > 0 && (p == out[len(out)-1] || strings.HasPrefix(p, out[len(out)-1]+"/")) {
			continue
		}
		out = append(out, p)
	}
	return out
}
