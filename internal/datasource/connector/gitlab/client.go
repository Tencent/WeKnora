package gitlab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// maxJSONResponseBytes bounds project, user, commit and paginated tree JSON.
	// Those listings are small; a larger body means a broken or hostile server.
	maxJSONResponseBytes int64 = 16 << 20
	// maxCompareResponseBytes bounds GET /repository/compare. That body carries
	// the full commit list and patch text. A straight compare across a long
	// unsynced range or a force-push routinely exceeds the metadata cap, and
	// failing it aborts the project sync before the cursor advances. Sync only
	// keeps paths and compare_timeout, but the response still has to be buffered.
	maxCompareResponseBytes int64 = 128 << 20
	// maxRawFileBytes bounds a single repository blob. GitLab's tree and
	// compare endpoints do not report blob sizes, so the download itself is the
	// only place a limit can be enforced. 512 MiB matches the Feishu connector,
	// the other connector that pulls arbitrary binary files.
	maxRawFileBytes int64 = 512 << 20
)

// base64FileBytes is the JSON cap for the file-detail fallback, whose body
// carries the raw blob base64-encoded (4/3 of its size) plus an envelope.
func base64FileBytes(rawLimit int64) int64 {
	return rawLimit/3*4 + (1 << 20)
}

// maxPaginationHops bounds a single paginated walk. GitLab reports the next
// page through the X-Next-Page header, so a broken instance or reverse proxy
// could keep advertising pages indefinitely.
const maxPaginationHops = 10000

type client struct {
	baseURL, token string
	http           *http.Client
	// jsonLimit, compareLimit and rawLimit are fields rather than direct uses of
	// the constants above so tests can lower them without materialising hundreds
	// of megabytes. newClient always sets the production values.
	jsonLimit    int64
	compareLimit int64
	rawLimit     int64
}

// readCapped reads a response body, refusing anything larger than limit instead
// of buffering it. Oversized payloads are reported as an error: a truncated
// body would be indexed as if it were the whole document.
func readCapped(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds maximum size (%d bytes)", limit)
	}
	return data, nil
}

type apiError struct {
	endpoint string
	status   int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("gitlab API %s: status %d", e.endpoint, e.status)
}

type project struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	Name              string `json:"name"`
	WebURL            string `json:"web_url"`
	DefaultBranch     string `json:"default_branch"`
	Namespace         struct {
		ID int64 `json:"id"`
	} `json:"namespace"`
}
type treeEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
}
type comparison struct {
	Diffs []struct {
		OldPath     string `json:"old_path"`
		NewPath     string `json:"new_path"`
		NewFile     bool   `json:"new_file"`
		DeletedFile bool   `json:"deleted_file"`
		RenamedFile bool   `json:"renamed_file"`
	} `json:"diffs"`
	CompareTimeout bool `json:"compare_timeout"`
	CompareSameRef bool `json:"compare_same_ref"`
}

func newClient(baseURL, token string) (*client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("GitLab platform configuration is missing")
	}
	if err := datasource.ValidateConnectorBaseURL(baseURL); err != nil {
		return nil, err
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	if !strings.HasSuffix(baseURL, "/api/v4") {
		baseURL += "/api/v4"
	}
	return &client{
		baseURL:      baseURL,
		token:        token,
		http:         datasource.NewConnectorHTTPClient(30 * time.Second),
		jsonLimit:    maxJSONResponseBytes,
		compareLimit: maxCompareResponseBytes,
		rawLimit:     maxRawFileBytes,
	}, nil
}

func (c *client) get(ctx context.Context, endpoint string, out interface{}) error {
	return c.getCapped(ctx, endpoint, out, c.jsonLimit)
}

// getCapped is get with an explicit response cap. Compare and the base64 file
// fallback pass their own limits because those bodies are larger than metadata.
func (c *client) getCapped(ctx context.Context, endpoint string, out interface{}, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{endpoint: endpoint, status: resp.StatusCode}
	}
	body, err := readCapped(resp.Body, limit)
	if err != nil {
		return fmt.Errorf("gitlab API %s: %w", endpoint, err)
	}
	return json.Unmarshal(body, out)
}

func (c *client) getRaw(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &apiError{endpoint: endpoint, status: resp.StatusCode}
	}
	body, err := readCapped(resp.Body, c.rawLimit)
	if err != nil {
		return nil, fmt.Errorf("gitlab raw file %s: %w", endpoint, err)
	}
	return body, nil
}

// projectPath encodes a GitLab project identifier for URL path segments.
// Numeric IDs are used verbatim. Namespace paths accept either "group/project"
// or a once-encoded "group%2Fproject" without double-encoding percent signs.
func projectPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	if _, err := strconv.ParseInt(id, 10, 64); err == nil {
		return id
	}
	decoded := id
	if strings.Contains(id, "%") {
		if unescaped, err := url.PathUnescape(id); err == nil {
			decoded = unescaped
		}
	}
	if strings.Contains(decoded, "/") {
		parts := strings.Split(decoded, "/")
		for i, part := range parts {
			parts[i] = url.PathEscape(part)
		}
		return strings.Join(parts, "%2F")
	}
	return url.PathEscape(decoded)
}

func (c *client) project(ctx context.Context, id string) (*project, error) {
	var p project
	err := c.get(ctx, "/projects/"+projectPath(id), &p)
	return &p, err
}

// trackPage records the page a paginated walk is about to request. A page that
// was already requested means X-Next-Page is not advancing, so the walk stops
// with an error instead of re-reading the same page; the hop cap bounds a
// server that keeps advertising fresh page numbers.
func trackPage(ctx context.Context, scope string, seen map[string]struct{}, page string) error {
	if _, dup := seen[page]; dup {
		return fmt.Errorf("gitlab %s pagination repeated page %q", scope, page)
	}
	if len(seen) >= maxPaginationHops {
		logger.Warnf(ctx, "[GitLab] %s pagination exceeded %d pages; aborting", scope, maxPaginationHops)
		return fmt.Errorf("gitlab %s pagination exceeded %d pages", scope, maxPaginationHops)
	}
	seen[page] = struct{}{}
	return nil
}

func (c *client) projects(ctx context.Context) ([]project, error) {
	q := url.Values{
		"membership": {"true"}, "per_page": {"100"}, "page": {"1"},
		"order_by": {"path_with_namespace"}, "sort": {"asc"},
	}
	var all []project
	seen := make(map[string]struct{})
	for {
		if err := trackPage(ctx, "project", seen, q.Get("page")); err != nil {
			return nil, err
		}
		var page []project
		nextPage, err := c.getPage(ctx, "/projects", q, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if nextPage == "" {
			return all, nil
		}
		q.Set("page", nextPage)
	}
}

// ping verifies that the supplied private token is accepted by this GitLab
// instance. It avoids project-list ordering parameters that older GitLab
// deployments may reject even when the token is valid.
func (c *client) ping(ctx context.Context) error {
	var user struct {
		ID int64 `json:"id"`
	}
	return c.get(ctx, "/user", &user)
}

func (c *client) commitSHA(ctx context.Context, id, ref string) (string, error) {
	var v struct {
		ID string `json:"id"`
	}
	err := c.get(ctx, "/projects/"+projectPath(id)+"/repository/commits/"+url.PathEscape(ref), &v)
	return v.ID, err
}

func (c *client) tree(ctx context.Context, id, ref, dir string) ([]treeEntry, error) {
	q := url.Values{"ref": {ref}, "per_page": {"100"}, "page": {"1"}}
	if dir != "" {
		q.Set("path", dir)
	}
	endpoint := "/projects/" + projectPath(id) + "/repository/tree"
	var all []treeEntry
	seen := make(map[string]struct{})
	for {
		if err := trackPage(ctx, "tree", seen, q.Get("page")); err != nil {
			return nil, err
		}
		var page []treeEntry
		nextPage, err := c.getPage(ctx, endpoint, q, &page)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if nextPage == "" {
			return all, nil
		}
		q.Set("page", nextPage)
	}
}

func (c *client) getPage(ctx context.Context, endpoint string, query url.Values, out interface{}) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("PRIVATE-TOKEN", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &apiError{endpoint: endpoint, status: resp.StatusCode}
	}
	body, err := readCapped(resp.Body, c.jsonLimit)
	if err != nil {
		return "", fmt.Errorf("gitlab API %s: %w", endpoint, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return "", err
	}
	return resp.Header.Get("X-Next-Page"), nil
}

func (c *client) raw(ctx context.Context, id, ref, file string) ([]byte, error) {
	q := url.Values{"ref": {ref}}
	encodedFile := gitlabFilePathEscape(file)
	rawEndpoint := "/projects/" + projectPath(id) + "/repository/files/" + encodedFile + "/raw?" + q.Encode()
	content, err := c.getRaw(ctx, rawEndpoint)
	if err == nil {
		return content, nil
	}
	var apiErr *apiError
	if !errors.As(err, &apiErr) || apiErr.status != http.StatusNotFound {
		return nil, fmt.Errorf("gitlab file %s: %w", file, err)
	}

	// Some GitLab deployments expose the file detail endpoint but return 404
	// for the otherwise standard /raw route. The detail response contains the
	// same content as base64 and provides a compatible fallback.
	var detail struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	detailEndpoint := "/projects/" + projectPath(id) + "/repository/files/" + encodedFile + "?" + q.Encode()
	if err := c.getCapped(ctx, detailEndpoint, &detail, base64FileBytes(c.rawLimit)); err != nil {
		return nil, fmt.Errorf("gitlab file %s: %w", file, err)
	}
	if detail.Encoding != "base64" {
		return nil, fmt.Errorf("gitlab file %s: unsupported encoding %q", file, detail.Encoding)
	}
	content, err = base64.StdEncoding.DecodeString(detail.Content)
	if err != nil {
		return nil, fmt.Errorf("gitlab file %s: decode base64: %w", file, err)
	}
	return content, nil
}

// gitlabFilePathEscape mirrors the company GitLab raw-file route: only ASCII
// letters, digits, hyphen and underscore remain literal. In particular dots,
// path separators and UTF-8 bytes must be percent-encoded.
func gitlabFilePathEscape(file string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	b.Grow(len(file) * 3)
	for _, c := range []byte(file) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

func (c *client) compare(ctx context.Context, id, from, to string) (*comparison, error) {
	// Sync compares snapshots, not changes since their merge base. The default
	// comparison misses reverted files when a branch is reset or force-pushed.
	q := url.Values{"from": {from}, "to": {to}, "straight": {"true"}}
	var v comparison
	err := c.getCapped(ctx, "/projects/"+projectPath(id)+"/repository/compare?"+q.Encode(), &v, c.compareLimit)
	return &v, err
}
