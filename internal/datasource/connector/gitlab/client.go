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
)

type client struct {
	baseURL, token string
	http           *http.Client
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
	return &client{baseURL: baseURL, token: token, http: datasource.NewConnectorHTTPClient(30 * time.Second)}, nil
}
func (c *client) get(ctx context.Context, endpoint string, out interface{}) error {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &apiError{endpoint: endpoint, status: resp.StatusCode}
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
	return io.ReadAll(resp.Body)
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
func (c *client) projects(ctx context.Context) ([]project, error) {
	var p []project
	err := c.get(ctx, "/projects?membership=true&per_page=100&order_by=path_with_namespace&sort=asc", &p)
	return p, err
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
	for {
		var page []treeEntry
		nextPage, err := c.getTreePage(ctx, endpoint, q, &page)
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

func (c *client) getTreePage(ctx context.Context, endpoint string, query url.Values, out interface{}) (string, error) {
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", &apiError{endpoint: endpoint, status: resp.StatusCode}
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
		return nil, fmt.Errorf("gitlab raw file: %w", err)
	}

	// Some GitLab deployments expose the file detail endpoint but return 404
	// for the otherwise standard /raw route. The detail response contains the
	// same content as base64 and provides a compatible fallback.
	var detail struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	detailEndpoint := "/projects/" + projectPath(id) + "/repository/files/" + encodedFile + "?" + q.Encode()
	if err := c.get(ctx, detailEndpoint, &detail); err != nil {
		return nil, fmt.Errorf("gitlab file content: %w", err)
	}
	if detail.Encoding != "base64" {
		return nil, fmt.Errorf("gitlab file content: unsupported encoding %q", detail.Encoding)
	}
	content, err = base64.StdEncoding.DecodeString(detail.Content)
	if err != nil {
		return nil, fmt.Errorf("gitlab file content: decode base64: %w", err)
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
	q := url.Values{"from": {from}, "to": {to}}
	var v comparison
	err := c.get(ctx, "/projects/"+projectPath(id)+"/repository/compare?"+q.Encode(), &v)
	return &v, err
}
