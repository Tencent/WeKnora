// Package nextcloud synchronizes files from a Nextcloud account over WebDAV.
// Resource IDs are absolute, decoded paths relative to the account's files root.
package nextcloud

import (
	"crypto/sha256"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

type config struct {
	baseURL, username, password string
	include, exclude            map[string]bool
}

func parseConfig(ds *types.DataSourceConfig) (config, error) {
	if ds == nil {
		return config{}, fmt.Errorf("%w: Nextcloud configuration is required", datasource.ErrInvalidConfig)
	}
	base, _ := ds.Credentials["base_url"].(string)
	username, _ := ds.Credentials["username"].(string)
	password, _ := ds.Credentials["app_password"].(string)
	username = strings.TrimSpace(username)
	if username == "" || username == "." || username == ".." || strings.ContainsAny(username, "/\\:") ||
		strings.ContainsFunc(username, unicode.IsControl) {
		return config{}, fmt.Errorf("%w: a valid username is required", datasource.ErrInvalidConfig)
	}
	if strings.TrimSpace(password) == "" || strings.ContainsFunc(password, unicode.IsControl) {
		return config{}, fmt.Errorf("%w: app_password is required", datasource.ErrInvalidConfig)
	}
	base, err := normalizeBaseURL(base)
	if err != nil {
		return config{}, err
	}
	include, err := extensions(ds.Settings["include_extensions"])
	if err != nil {
		return config{}, err
	}
	exclude, err := extensions(ds.Settings["exclude_extensions"])
	if err != nil {
		return config{}, err
	}
	return config{baseURL: base, username: username, password: password, include: include, exclude: exclude}, nil
}

func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: base_url is required", datasource.ErrInvalidConfig)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery ||
		u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("%w: base_url must be an HTTP(S) installation URL without credentials, query or fragment",
			datasource.ErrInvalidConfig)
	}
	deployment, err := normalizePath("/" + strings.Trim(u.Path, "/"))
	if err != nil || strings.Contains(deployment, "/remote.php") {
		return "", fmt.Errorf("%w: base_url must point to the installation, without a WebDAV endpoint",
			datasource.ErrInvalidConfig)
	}
	u.Path, u.RawPath = strings.TrimRight(deployment, "/"), ""
	u.Host = strings.ToLower(u.Host)
	base := u.String()
	if err := datasource.ValidateConnectorBaseURL(base); err != nil {
		return "", fmt.Errorf("%w: %v", datasource.ErrInvalidConfig, err)
	}
	return base, nil
}

func extensions(value any) (map[string]bool, error) {
	var values []string
	switch v := value.(type) {
	case nil:
	case string:
		values = strings.FieldsFunc(v, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	case []string:
		values = v
	case []any:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%w: extension filters must contain strings", datasource.ErrInvalidConfig)
			}
			values = append(values, s)
		}
	default:
		return nil, fmt.Errorf("%w: extension filters must be strings or arrays", datasource.ErrInvalidConfig)
	}
	out := make(map[string]bool, len(values))
	for _, v := range values {
		ext := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(v), "."))
		if ext == "" || strings.ContainsAny(ext, "/\\.*?,") || strings.ContainsFunc(ext, func(r rune) bool {
			return unicode.IsSpace(r) || unicode.IsControl(r)
		}) {
			return nil, fmt.Errorf("%w: invalid extension filter", datasource.ErrInvalidConfig)
		}
		out["."+ext] = true
	}
	return out, nil
}

func (c config) includes(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	return utils.IsSupportedImportExtension(ext) && !c.exclude[ext] && (len(c.include) == 0 || c.include[ext])
}

// scope binds cursors and external IDs to the deployment and account, without
// storing credentials in the cursor. Rotating an app password keeps the scope.
func (c config) scope() string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(c.baseURL+"\x00"+c.username)))
}

func normalizePath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || strings.ContainsFunc(p, unicode.IsControl) {
		return "", fmt.Errorf("%w: Nextcloud paths must be absolute and contain no control characters or backslashes",
			datasource.ErrInvalidConfig)
	}
	var segments []string
	for _, segment := range strings.Split(p, "/") {
		if segment == "." || segment == ".." {
			return "", fmt.Errorf("%w: Nextcloud paths must not contain dot segments", datasource.ErrInvalidConfig)
		}
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return "/" + strings.Join(segments, "/"), nil
}

func collapseRoots(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: select at least one Nextcloud folder or file", datasource.ErrInvalidConfig)
	}
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		p, err := normalizePath(id)
		if err != nil {
			return nil, err
		}
		selected[p] = true
	}
	var roots []string
	for p := range selected {
		covered := false
		for parent := p; parent != "/"; {
			parent = path.Dir(parent)
			if selected[parent] {
				covered = true
				break
			}
		}
		if !covered {
			roots = append(roots, p)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

type entry struct {
	Path, FileID, ETag string
	ModifiedAt         time.Time
	Size               int64
	IsDir              bool
}

const (
	reasonPermissionDenied = "nextcloud_permission_denied"
	reasonNotFound         = "nextcloud_not_found"
	reasonFileTooLarge     = "nextcloud_file_too_large"
	reasonEmptyFile        = "nextcloud_empty_file"
	reasonSourceChanged    = "nextcloud_source_changed"
	reasonInvalidResponse  = "nextcloud_invalid_response"
	reasonSSRFBlocked      = "nextcloud_ssrf_blocked"
	reasonFetchFailed      = "nextcloud_fetch_failed"
)

var reasonText = map[string]string{
	reasonPermissionDenied: "Permission to access the Nextcloud resource was denied",
	reasonNotFound:         "The Nextcloud resource was not found",
	reasonFileTooLarge:     "The Nextcloud file exceeds the configured size limit",
	reasonEmptyFile:        "The Nextcloud file is empty",
	reasonSourceChanged:    "The Nextcloud file changed while it was being fetched",
	reasonInvalidResponse:  "Nextcloud returned an unexpected response",
	reasonSSRFBlocked:      "The Nextcloud request was blocked by the SSRF policy",
	reasonFetchFailed:      "Failed to fetch the file from Nextcloud",
}
