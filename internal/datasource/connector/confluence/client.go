package confluence

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

const maxJSONResponseBytes int64 = 20 << 20
const requestAttempts = 4
const maxErrorResponseRunes = 1000

type client struct {
	cfg  config
	http *http.Client
}

func newClient(cfg config) (*client, error) {
	if err := datasource.ValidateConnectorBaseURL(cfg.baseURL); err != nil {
		return nil, err
	}
	return &client{cfg: cfg, http: datasource.NewConnectorHTTPClient(60 * time.Second)}, nil
}

func (c *client) get(ctx context.Context, endpoint string, output interface{}) error {
	fullURL, err := c.resolveEndpoint(endpoint)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < requestAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return err
		}
		req.SetBasicAuth(c.cfg.username, c.cfg.secret)
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponseBytes+1))
			resp.Body.Close()
			if readErr != nil {
				return readErr
			}
			if int64(len(body)) > maxJSONResponseBytes {
				return fmt.Errorf("Confluence response exceeds %d MiB limit", maxJSONResponseBytes>>20)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if output == nil {
					return nil
				}
				return json.Unmarshal(body, output)
			}
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return fmt.Errorf("Confluence API %s: status %d body=%q", endpoint, resp.StatusCode, responseExcerpt(body))
			}
			if attempt == requestAttempts-1 {
				return fmt.Errorf("Confluence API %s: status %d after retries", endpoint, resp.StatusCode)
			}
			if err = waitRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
				return err
			}
			continue
		}
		if attempt == requestAttempts-1 {
			return fmt.Errorf("Confluence API %s: %w", endpoint, err)
		}
		if err = waitRetry(ctx, "", attempt); err != nil {
			return err
		}
	}
	return fmt.Errorf("Confluence API %s: retry exhausted", endpoint)
}
func responseExcerpt(body []byte) string {
	value := []rune(strings.TrimSpace(string(body)))
	if len(value) <= maxErrorResponseRunes {
		return string(value)
	}
	return string(value[:maxErrorResponseRunes]) + "..."
}

func waitRetry(ctx context.Context, retryAfter string, attempt int) error {
	delay := time.Duration(1<<attempt)*time.Second + time.Duration(time.Now().UnixNano()%250)*time.Millisecond
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
		delay = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(retryAfter); err == nil && time.Until(t) > 0 {
		delay = time.Until(t)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func (c *client) resolveEndpoint(endpoint string) (string, error) {
	base, err := url.Parse(c.cfg.baseURL)
	if err != nil {
		return "", fmt.Errorf("parse Confluence base URL: %w", err)
	}
	next, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Confluence pagination URL: %w", err)
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/")
	if next.IsAbs() {
		if next.Scheme != base.Scheme || next.Host != base.Host {
			return "", fmt.Errorf("Confluence pagination URL leaves configured origin")
		}
		if basePath != "" && next.EscapedPath() != basePath && !strings.HasPrefix(next.EscapedPath(), basePath+"/") {
			return "", fmt.Errorf("Confluence pagination URL leaves configured context path")
		}
		return next.String(), nil
	}
	path := next.EscapedPath()
	if path == "" {
		path = basePath
	} else if basePath != "" && path != basePath && !strings.HasPrefix(path, basePath+"/") {
		path = basePath + "/" + strings.TrimLeft(path, "/")
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: path, RawQuery: next.RawQuery}).String(), nil
}

func (c *client) ping(ctx context.Context) error {
	if c.cfg.cloud() {
		return c.get(ctx, "/api/v2/spaces?limit=1", nil)
	}
	return c.get(ctx, "/rest/api/space?limit=1", nil)
}

func (c *client) spaces(ctx context.Context) ([]space, error) {
	if c.cfg.cloud() {
		var all []space
		next := "/api/v2/spaces?limit=250"
		for next != "" {
			var result spaceList
			if err := c.get(ctx, next, &result); err != nil {
				return nil, err
			}
			all = append(all, result.Results...)
			next = result.Links.Next
		}
		return all, nil
	}
	var all []space
	next := "/rest/api/space?limit=100"
	for next != "" {
		var result serverSpaceList
		if err := c.get(ctx, next, &result); err != nil {
			return nil, err
		}
		for _, v := range result.Results {
			all = append(all, space{ID: v.ID.String(), Key: v.Key, Name: v.Name, Links: v.Links})
		}
		next = result.Links.Next
	}
	return all, nil
}
func serverPageSearchEndpoint(spaceKey string) string {
	cql := "space=" + cqlString(spaceKey) + " AND type=page AND status=current"
	return "/rest/api/content/search?cql=" + url.QueryEscape(cql) + "&expand=version,space&limit=100"
}

func cqlString(value string) string {
	escaped := strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value)
	return "\"" + escaped + "\""
}

func (c *client) pages(ctx context.Context, s space) ([]page, error) {
	if c.cfg.cloud() {
		var all []page
		next := "/api/v2/spaces/" + url.PathEscape(s.ID) + "/pages?status=current&limit=250"
		for next != "" {
			var result cloudPageList
			if err := c.get(ctx, next, &result); err != nil {
				return nil, err
			}
			for _, value := range result.Results {
				p := page{ID: value.ID, Title: value.Title, Status: value.Status, Links: value.Links}
				p.Space.Key, p.Space.Name = s.Key, s.Name
				p.Version.Number, p.Version.CreatedAt = value.Version.Number, value.Version.CreatedAt
				if p.Version.CreatedAt == "" {
					p.Version.CreatedAt = value.CreatedAt
				}
				all = append(all, p)
			}
			next = result.Links.Next
		}
		return all, nil
	}
	var all []page
	next := serverPageSearchEndpoint(s.Key)
	for next != "" {
		var result pageList
		if err := c.get(ctx, next, &result); err != nil {
			return nil, err
		}
		all = append(all, result.Results...)
		next = result.Links.Next
	}
	return all, nil
}
func (c *client) body(ctx context.Context, id string) (pageBody, error) {
	var result pageBody
	err := c.get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand=body.view,version,space", &result)
	return result, err
}
