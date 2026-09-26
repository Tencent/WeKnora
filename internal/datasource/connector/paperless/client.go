package paperless

import (
	"context"
	"encoding/json"
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
	baseURL string
	token   string
	http    *http.Client
}

func newClient(baseURL, token string) *client {
	return &client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    datasource.NewConnectorHTTPClient(30 * time.Second),
	}
}

func (c *client) ping(ctx context.Context) error {
	_, err := c.listDocuments(ctx, url.Values{"page_size": {"1"}})
	return err
}

func (c *client) listCorrespondents(ctx context.Context) ([]correspondent, error) {
	return listPages[correspondent](ctx, c, "/api/correspondents/")
}

func (c *client) listDocumentTypes(ctx context.Context) ([]documentType, error) {
	return listPages[documentType](ctx, c, "/api/document_types/")
}

func (c *client) listCustomFields(ctx context.Context) ([]customField, error) {
	return listPages[customField](ctx, c, "/api/custom_fields/")
}

func (c *client) listDocuments(ctx context.Context, values url.Values) ([]document, error) {
	if values == nil {
		values = url.Values{}
	}
	if values.Get("page_size") == "" {
		values.Set("page_size", "100")
	}
	return listPagesWithValues[document](ctx, c, "/api/documents/", values)
}

func listPages[T any](ctx context.Context, c *client, initialPath string) ([]T, error) {
	return listPagesWithValues[T](ctx, c, initialPath, url.Values{"page_size": {"100"}})
}

func listPagesWithValues[T any](ctx context.Context, c *client, initialPath string, initialValues url.Values) ([]T, error) {
	var out []T
	path := initialPath
	values := initialValues
	for {
		var page listResponse[T]
		next, err := c.get(ctx, path, values, &page)
		if err != nil {
			return nil, err
		}
		out = append(out, page.Results...)
		if next == "" {
			return out, nil
		}
		path = next
		values = nil
	}
}

func (c *client) document(ctx context.Context, id int) (*document, error) {
	var doc document
	_, err := c.get(ctx, "/api/documents/"+strconv.Itoa(id)+"/", nil, &doc)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func (c *client) downloadDocument(ctx context.Context, id int) ([]byte, string, error) {
	path := "/api/documents/" + strconv.Itoa(id) + "/download/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("paperless: GET %s failed with status %d", path, resp.StatusCode)
	}
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return content, resp.Header.Get("Content-Type"), nil
}

func (c *client) get(ctx context.Context, path string, values url.Values, out interface{}) (string, error) {
	endpoint := path
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = c.baseURL + path
	}
	if values != nil && len(values) > 0 {
		endpoint += "?" + values.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("paperless: GET %s failed with status %d", path, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return "", err
	}
	return nextURL(out), nil
}

func nextURL(out interface{}) string {
	switch page := out.(type) {
	case *listResponse[document]:
		return strings.TrimSpace(page.Next)
	case *listResponse[correspondent]:
		return strings.TrimSpace(page.Next)
	case *listResponse[documentType]:
		return strings.TrimSpace(page.Next)
	case *listResponse[customField]:
		return strings.TrimSpace(page.Next)
	default:
		return ""
	}
}
