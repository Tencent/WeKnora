// Package seafile implements the WeKnora data source connector for Seafile
// Server (community edition 10.0.1, api2). A data source selects nodes of a
// single library; the connector scans the selected scope one directory level
// at a time, downloads files through the two-step link + fileserver flow and
// reconciles deletions against a per-file fingerprint cursor.
package seafile

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// config is the parsed credential set. The selection lives entirely in
// resource_ids, so Settings carry no Seafile-specific keys and are ignored;
// a stale key left over from another connector type does no harm.
type config struct {
	baseURL string
	token   string
}

func parseConfig(ds *types.DataSourceConfig) (config, error) {
	if ds == nil {
		return config{}, fmt.Errorf("%w: seafile config is nil", datasource.ErrInvalidConfig)
	}
	rawBase, _ := ds.Credentials["base_url"].(string)
	rawToken, _ := ds.Credentials["api_token"].(string)

	token := strings.TrimSpace(rawToken)
	if token == "" || strings.ContainsFunc(token, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) {
		return config{}, fmt.Errorf("%w: api_token is required and must not contain whitespace",
			datasource.ErrInvalidConfig)
	}

	baseURL, err := normalizeBaseURL(rawBase)
	if err != nil {
		return config{}, err
	}
	return config{baseURL: baseURL, token: token}, nil
}

// normalizeBaseURL trims the URL, defaults the scheme to https, drops the
// trailing slash and keeps any deployment path prefix.
func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: base_url is required", datasource.ErrInvalidConfig)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", fmt.Errorf("%w: base_url must be an http(s) URL without credentials, query or fragment",
			datasource.ErrInvalidConfig)
	}
	base := strings.TrimRight(u.String(), "/")
	if err := datasource.ValidateConnectorBaseURL(base); err != nil {
		return "", fmt.Errorf("%w: %v", datasource.ErrInvalidConfig, err)
	}
	return base, nil
}

// Resource IDs are "<repo_id>:<absolute path>"; the library root is
// "<repo_id>:/". The split happens at the first colon so paths may contain
// colons. IDs are never URL-decoded.

var repoIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func parseResourceID(id string) (repoID, path string, err error) {
	repoID, rawPath, ok := strings.Cut(id, ":")
	if !ok || !repoIDPattern.MatchString(repoID) {
		return "", "", fmt.Errorf("%w: seafile resource id must be <repo_id>:<absolute path>",
			datasource.ErrInvalidConfig)
	}
	path, err = normalizePath(rawPath)
	if err != nil {
		return "", "", err
	}
	return repoID, path, nil
}

func encodeResourceID(repoID, path string) string {
	return repoID + ":" + path
}

// normalizePath collapses repeated slashes, strips the trailing slash (except
// for the root) and rejects dot segments, backslashes and control characters.
func normalizePath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsFunc(p, unicode.IsControl) {
		return "", fmt.Errorf("%w: seafile path must be absolute without backslashes or control characters",
			datasource.ErrInvalidConfig)
	}
	segments := make([]string, 0, 8)
	for _, segment := range strings.Split(p, "/") {
		switch segment {
		case "":
			continue
		case ".", "..":
			return "", fmt.Errorf("%w: seafile path must not contain dot segments", datasource.ErrInvalidConfig)
		}
		segments = append(segments, segment)
	}
	return "/" + strings.Join(segments, "/"), nil
}

// collapseRoots dedupes and sorts the selection, requires a single library
// and drops every path whose ancestor directory is also selected. "/a" does
// not cover "/ab".
func collapseRoots(ids []string) (repoID string, roots []string, err error) {
	if len(ids) == 0 {
		return "", nil, fmt.Errorf("%w: seafile requires at least one selected resource", datasource.ErrInvalidConfig)
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		repo, p, err := parseResourceID(id)
		if err != nil {
			return "", nil, err
		}
		if repoID != "" && repo != repoID {
			return "", nil, fmt.Errorf("%w: seafile selections must belong to one library", datasource.ErrInvalidConfig)
		}
		repoID = repo
		selected[p] = true
	}
	sorted := make([]string, 0, len(selected))
	for p := range selected {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		if !hasSelectedAncestor(p, selected) {
			roots = append(roots, p)
		}
	}
	return repoID, roots, nil
}

func hasSelectedAncestor(p string, selected map[string]bool) bool {
	for p != "/" {
		p = parentPath(p)
		if selected[p] {
			return true
		}
	}
	return false
}

// parentPath returns the parent of a normalized absolute path ("/" for
// top-level entries).
func parentPath(p string) string {
	if i := strings.LastIndex(p, "/"); i > 0 {
		return p[:i]
	}
	return "/"
}

// ancestorIDs lists the resource IDs a lazy picker must expand to reveal id:
// "<repo>:/a/b/c" yields "<repo>:/", "<repo>:/a", "<repo>:/a/b". Unparsable
// IDs and the root yield nothing.
func ancestorIDs(id string) []string {
	repoID, p, err := parseResourceID(id)
	if err != nil || p == "/" {
		return nil
	}
	out := []string{encodeResourceID(repoID, "/")}
	for i := 1; i < len(p); i++ {
		if p[i] == '/' {
			out = append(out, encodeResourceID(repoID, p[:i]))
		}
	}
	return out
}

// repoInfo is one row of GET /api2/repos/. seahub 10.0.1 exposes no reliable
// "virtual" marker, so it is deliberately not decoded.
type repoInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Encrypted  bool   `json:"encrypted"`
	Permission string `json:"permission"`
}

// dirent is one entry of GET /api2/repos/{id}/dir/. Only file entries carry
// a size key.
type dirent struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Mtime      int64  `json:"mtime"`
	Size       *int64 `json:"size,omitempty"`
	Permission string `json:"permission"`
}

// Error reason codes surfaced on error items; the frontend localises them via
// datasource.syncError.<code>.
const (
	reasonPermissionDenied = "seafile_permission_denied"
	reasonNotFound         = "seafile_not_found"
	reasonFileTooLarge     = "seafile_file_too_large"
	reasonEmptyFile        = "seafile_empty_file"
	reasonSourceChanged    = "seafile_source_changed"
	reasonInvalidResponse  = "seafile_invalid_response"
	reasonSSRFBlocked      = "seafile_ssrf_blocked"
	reasonFetchFailed      = "seafile_fetch_failed"
)

var reasonText = map[string]string{
	reasonPermissionDenied: "Permission to access the Seafile resource was denied",
	reasonNotFound:         "The Seafile resource was not found",
	reasonFileTooLarge:     "The Seafile file exceeds the configured size limit",
	reasonEmptyFile:        "The Seafile file is empty",
	reasonSourceChanged:    "The Seafile file changed while it was being fetched",
	reasonInvalidResponse:  "Seafile returned an unexpected response",
	reasonSSRFBlocked:      "The Seafile download URL was blocked by the SSRF policy",
	reasonFetchFailed:      "Failed to fetch the file from Seafile",
}

// errorReason returns the English fallback text for a reason code.
func errorReason(code string) string {
	if text, ok := reasonText[code]; ok {
		return text
	}
	return reasonText[reasonFetchFailed]
}
