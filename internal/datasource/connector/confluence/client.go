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

const (
	maxJSONResponseBytes  int64 = 20 << 20
	requestAttempts             = 4
	maxErrorResponseRunes       = 1000
	maxRetryDelay               = 60 * time.Second
)

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
			_ = resp.Body.Close()
			if readErr != nil {
				return readErr
			}
			if int64(len(body)) > maxJSONResponseBytes {
				return fmt.Errorf("confluence response exceeds %d MiB limit", maxJSONResponseBytes>>20)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if output == nil {
					return nil
				}
				return json.Unmarshal(body, output)
			}
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return fmt.Errorf(
					"confluence API %s: status %d body=%q",
					endpoint, resp.StatusCode, responseExcerpt(body),
				)
			}
			if attempt == requestAttempts-1 {
				return fmt.Errorf("confluence API %s: status %d after retries", endpoint, resp.StatusCode)
			}
			if err = waitRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
				return err
			}
			continue
		}
		if attempt == requestAttempts-1 {
			return fmt.Errorf("confluence API %s: %w", endpoint, err)
		}
		if err = waitRetry(ctx, "", attempt); err != nil {
			return err
		}
	}
	return fmt.Errorf("confluence API %s: retry exhausted", endpoint)
}

func responseExcerpt(body []byte) string {
	value := []rune(strings.TrimSpace(string(body)))
	if len(value) <= maxErrorResponseRunes {
		return string(value)
	}
	return string(value[:maxErrorResponseRunes]) + "..."
}

func waitRetry(ctx context.Context, retryAfter string, attempt int) error {
	backoff := time.Duration(1<<attempt)*time.Second + time.Duration(time.Now().UnixNano()%250)*time.Millisecond
	delay := capRetryDelay(backoff)
	if secs, err := strconv.Atoi(retryAfter); err == nil {
		delay = capRetryDelay(time.Duration(secs) * time.Second)
	} else if t, err := http.ParseTime(retryAfter); err == nil {
		delay = capRetryDelay(time.Until(t))
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

func capRetryDelay(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 100 * time.Millisecond
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
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
			return "", fmt.Errorf("confluence pagination URL leaves configured origin")
		}
		if basePath != "" && next.EscapedPath() != basePath && !strings.HasPrefix(next.EscapedPath(), basePath+"/") {
			return "", fmt.Errorf("confluence pagination URL leaves configured context path")
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

func (c *client) resourceURL(link string) string {
	if strings.TrimSpace(link) == "" {
		return c.cfg.baseURL
	}
	resolved, err := c.resolveEndpoint(link)
	if err != nil {
		return c.cfg.baseURL
	}
	return resolved
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

func serverSpacePagesEndpoint(spaceKey string) string {
	return "/rest/api/space/" + url.PathEscape(spaceKey) + "/content/page?expand=version,space&limit=100"
}

func (c *client) pages(ctx context.Context, s space) ([]page, error) {
	if c.cfg.cloud() {
		var all []page
		next := "/api/v2/spaces/" + url.PathEscape(s.ID) + "/pages?status=current&depth=all&limit=250"
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
	next := serverSpacePagesEndpoint(s.Key)
	for next != "" {
		var result serverSpacePageList
		if err := c.get(ctx, next, &result); err != nil {
			return nil, err
		}
		all = append(all, result.Page.Results...)
		next = result.Page.Links.Next
		if next == "" {
			next = result.Links.Next
		}
	}
	return all, nil
}

func (c *client) body(ctx context.Context, id string) (pageBody, error) {
	if c.cfg.cloud() {
		var result pageBody
		err := c.get(ctx, "/api/v2/pages/"+url.PathEscape(id)+"?body-format=view", &result)
		return result, err
	}
	var result pageBody
	err := c.get(ctx, "/rest/api/content/"+url.PathEscape(id)+"?expand=body.view,version,space", &result)
	return result, err
}
