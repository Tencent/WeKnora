package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	urlpath "path"
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
	defaultPageSize             = 50
)

type apiError struct {
	endpoint string
	status   int
	excerpt  string
}

func (e *apiError) Error() string {
	if e.excerpt == "" {
		return fmt.Sprintf("jira API %s: status %d", e.endpoint, e.status)
	}
	return fmt.Sprintf("jira API %s: status %d body=%q", e.endpoint, e.status, e.excerpt)
}

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
				return fmt.Errorf("jira response exceeds %d MiB limit", maxJSONResponseBytes>>20)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if output == nil {
					return nil
				}
				return json.Unmarshal(body, output)
			}
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return &apiError{
					endpoint: endpoint,
					status:   resp.StatusCode,
					excerpt:  responseExcerpt(body),
				}
			}
			if attempt == requestAttempts-1 {
				return &apiError{endpoint: endpoint, status: resp.StatusCode, excerpt: responseExcerpt(body)}
			}
			if err = waitRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
				return err
			}
			continue
		}
		if attempt == requestAttempts-1 {
			return fmt.Errorf("jira API %s: %w", endpoint, err)
		}
		if err = waitRetry(ctx, "", attempt); err != nil {
			return err
		}
	}
	return fmt.Errorf("jira API %s: retry exhausted", endpoint)
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
		return "", fmt.Errorf("parse Jira base URL: %w", err)
	}
	next, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse Jira endpoint URL: %w", err)
	}
	basePath := strings.TrimRight(urlpath.Clean(base.Path), "/")
	if basePath == "." {
		basePath = ""
	}
	if next.IsAbs() {
		if next.Scheme != base.Scheme || next.Host != base.Host {
			return "", fmt.Errorf("jira pagination URL leaves configured origin")
		}
		cleaned, err := joinContextPath(basePath, next.Path, false)
		if err != nil {
			return "", err
		}
		next.Path = cleaned
		next.RawPath = ""
		return next.String(), nil
	}
	cleaned, err := joinContextPath(basePath, next.Path, true)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: base.Scheme, Host: base.Host, Path: cleaned, RawQuery: next.RawQuery}).String(), nil
}

func joinContextPath(basePath, rawPath string, prependContext bool) (string, error) {
	joined := rawPath
	if joined == "" {
		joined = basePath
	} else if prependContext && basePath != "" && joined != basePath && !strings.HasPrefix(joined, basePath+"/") {
		joined = basePath + "/" + strings.TrimLeft(joined, "/")
	}
	cleaned := urlpath.Clean(joined)
	if cleaned == "." {
		cleaned = "/"
	}
	if basePath != "" && cleaned != basePath && !strings.HasPrefix(cleaned, basePath+"/") {
		return "", fmt.Errorf("jira URL leaves configured context path")
	}
	return cleaned, nil
}

// ping verifies credentials against Jira API.
func (c *client) ping(ctx context.Context) error {
	return c.get(ctx, "/rest/api/2/myself", nil)
}

// projects retrieves visible Jira projects.
func (c *client) projects(ctx context.Context) ([]project, error) {
	var list []project
	if err := c.get(ctx, "/rest/api/2/project", &list); err != nil {
		return nil, err
	}
	return list, nil
}

// searchIssues queries Jira issues matching JQL.
func (c *client) searchIssues(ctx context.Context, jql, nextPageToken string, maxResults int) (*searchResponse, error) {
	if maxResults <= 0 {
		maxResults = defaultPageSize
	}
	body := map[string]interface{}{
		"jql":        jql,
		"maxResults": maxResults,
		"fields":     []string{"summary", "issuetype", "status", "priority", "resolution", "assignee", "reporter", "created", "updated", "components", "labels", "fixVersions", "description", "comment", "attachment"},
	}
	if nextPageToken != "" {
		body["nextPageToken"] = nextPageToken
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	endpoint := "/rest/api/3/search/jql"
	var resp searchResponse
	if err := c.requestJSON(ctx, http.MethodPost, endpoint, payload, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *client) requestJSON(ctx context.Context, method, endpoint string, payload []byte, output interface{}) error {
	fullURL, err := c.resolveEndpoint(endpoint)
	if err != nil { return err }
	for attempt := 0; attempt < requestAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, fullURL, bytes.NewReader(payload))
		if err != nil { return err }
		req.SetBasicAuth(c.cfg.username, c.cfg.secret)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxJSONResponseBytes+1)); _ = resp.Body.Close()
			if readErr != nil { return readErr }
			if int64(len(data)) > maxJSONResponseBytes { return fmt.Errorf("jira response exceeds %d MiB limit", maxJSONResponseBytes>>20) }
			if resp.StatusCode >= 200 && resp.StatusCode < 300 { return json.Unmarshal(data, output) }
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 { return &apiError{endpoint: endpoint, status: resp.StatusCode, excerpt: responseExcerpt(data)} }
			if attempt == requestAttempts-1 { return &apiError{endpoint: endpoint, status: resp.StatusCode, excerpt: responseExcerpt(data)} }
			if err = waitRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil { return err }
			continue
		}
		if attempt == requestAttempts-1 { return fmt.Errorf("jira API %s: %w", endpoint, err) }
		if err = waitRetry(ctx, "", attempt); err != nil { return err }
	}
	return fmt.Errorf("jira API %s: retry exhausted", endpoint)
}

// enrichCommentsIfNeeded fetches additional comments if the issue has more comments than returned in search.
func (c *client) enrichCommentsIfNeeded(ctx context.Context, iss *issue) error {
	if iss == nil || iss.Fields.Comment == nil {
		return nil
	}
	total := iss.Fields.Comment.Total
	curCount := len(iss.Fields.Comment.Comments)
	if curCount >= total {
		return nil
	}

	startAt := curCount
	for startAt < total {
		query := url.Values{}
		query.Set("startAt", strconv.Itoa(startAt))
		query.Set("maxResults", "100")
		query.Set("expand", "renderedBody")

		endpoint := fmt.Sprintf("/rest/api/3/issue/%s/comment?%s", url.PathEscape(iss.Key), query.Encode())
		var page struct {
			Comments []struct {
				ID           string `json:"id"`
				Author       *struct{ DisplayName string `json:"displayName"` } `json:"author,omitempty"`
				Created      string `json:"created"`
				Updated      string `json:"updated"`
				Body         interface{} `json:"body"`
				RenderedBody string `json:"renderedBody"`
			} `json:"comments"`
		}
		if err := c.get(ctx, endpoint, &page); err != nil {
			return err
		}
		if len(page.Comments) == 0 {
			break
		}
		for _, cm := range page.Comments {
			iss.Fields.Comment.Comments = append(iss.Fields.Comment.Comments, comment{
				ID:      cm.ID,
				Author:  cm.Author,
				Created: cm.Created,
				Updated: cm.Updated,
				Body:    cm.Body,
			})
			if iss.RenderedFields == nil {
				iss.RenderedFields = &renderedFields{}
			}
			if iss.RenderedFields.Comment == nil {
				iss.RenderedFields.Comment = &renderedCommentList{}
			}
			iss.RenderedFields.Comment.Comments = append(iss.RenderedFields.Comment.Comments, renderedComment{
				ID:   cm.ID,
				Body: cm.RenderedBody,
			})
		}
		startAt += len(page.Comments)
	}
	return nil
}

func (c *client) resourceURL(projectKey string) string {
	return fmt.Sprintf("%s/browse/%s", strings.TrimRight(c.cfg.baseURL, "/"), projectKey)
}

func (c *client) issueURL(issueKey string) string {
	return fmt.Sprintf("%s/browse/%s", strings.TrimRight(c.cfg.baseURL, "/"), issueKey)
}

// downloadAttachment downloads file attachment content with retry and size limits.
func (c *client) downloadAttachment(ctx context.Context, contentURL string) ([]byte, error) {
	fullURL, err := c.resolveEndpoint(contentURL)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < requestAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return nil, err
		}
		req.SetBasicAuth(c.cfg.username, c.cfg.secret)
		resp, err := c.http.Do(req)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, readErr
				}
				if int64(len(data)) > maxAttachmentBytes {
					return nil, fmt.Errorf("attachment exceeds %d MiB limit", maxAttachmentBytes>>20)
				}
				return data, nil
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return nil, fmt.Errorf("download attachment failed with status %d", resp.StatusCode)
			}
			if attempt == requestAttempts-1 {
				return nil, fmt.Errorf("download attachment failed with status %d", resp.StatusCode)
			}
			if err = waitRetry(ctx, resp.Header.Get("Retry-After"), attempt); err != nil {
				return nil, err
			}
			continue
		}
		if attempt == requestAttempts-1 {
			return nil, fmt.Errorf("download attachment: %w", err)
		}
		if err = waitRetry(ctx, "", attempt); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("download attachment: retry exhausted")
}
