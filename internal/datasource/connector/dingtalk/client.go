package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	defaultTimeout = 30 * time.Second
	userAgent      = "WeKnora-DingTalk-Connector/1.0"
	tokenEndpoint  = "/gettoken"
)

type client struct {
	appKey     string
	appSecret  string
	baseURL    string
	httpClient *http.Client

	mu          sync.RWMutex
	accessToken string
	tokenExpiry time.Time

	logCredsOnce sync.Once
}

func newClient(cfg *Config) *client {
	return &client{
		appKey:     cfg.AppKey,
		appSecret:  cfg.AppSecret,
		baseURL:    cfg.GetBaseURL(),
		httpClient: datasource.NewConnectorHTTPClient(defaultTimeout),
	}
}

func (c *client) getAccessToken(ctx context.Context) (string, error) {
	c.mu.RLock()
	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		token := c.accessToken
		c.mu.RUnlock()
		return token, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.accessToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.accessToken, nil
	}

	c.logCredsOnce.Do(func() {
		logger.Infof(ctx, "[DingTalk] client configured app_key=%s base=%s",
			redactToken(c.appKey), c.baseURL)
	})

	token, expiresIn, err := c.fetchAccessToken(ctx)
	if err != nil {
		return "", err
	}

	c.accessToken = token
	c.tokenExpiry = time.Now().Add(time.Duration(expiresIn-300) * time.Second)
	return c.accessToken, nil
}

func (c *client) fetchAccessToken(ctx context.Context) (string, int, error) {
	path := fmt.Sprintf("%s?appkey=%s&appsecret=%s", tokenEndpoint, c.appKey, c.appSecret)
	reqURL := c.baseURL + path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	logger.Infof(ctx, "[DingTalk] fetching access_token")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("execute token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", 0, fmt.Errorf("read token response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("decode token response: %w", err)
	}

	if !tr.IsSuccess() {
		return "", 0, fmt.Errorf("%w: dingtalk auth failed errcode=%d errmsg=%s",
			datasource.ErrInvalidCredentials, tr.ErrCode, tr.ErrMsg)
	}

	return tr.AccessToken, tr.ExpiresIn, nil
}

func (c *client) doRequest(ctx context.Context, method, path string, result interface{}) error {
	const maxRetries = 3
	var lastErr error
	backoff := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

	for attempt := 0; attempt <= maxRetries; attempt++ {
		token, err := c.getAccessToken(ctx)
		if err != nil {
			return fmt.Errorf("get access token: %w", err)
		}

		separator := "?"
		if strings.Contains(path, "?") {
			separator = "&"
		}
		reqURL := c.baseURL + path + fmt.Sprintf("%saccess_token=%s", separator, token)

		req, err := http.NewRequestWithContext(ctx, method, reqURL, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")

		if attempt == 0 {
			logger.Infof(ctx, "[DingTalk] %s %s", method, path)
		} else {
			logger.Infof(ctx, "[DingTalk] %s %s (retry %d/%d)", method, path, attempt, maxRetries)
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("execute request: %w", err)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read response body: %w", readErr)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		bodyPreview := truncate(string(body), 500)
		logger.Infof(ctx, "[DingTalk] %s %s → status=%d bodyLen=%d body=%s",
			method, path, resp.StatusCode, len(body), bodyPreview)

		if resp.StatusCode == 429 {
			wait := parseRetryAfter(resp.Header.Get("Retry-After"), backoff[minInt(attempt, len(backoff)-1)])
			lastErr = fmt.Errorf("dingtalk rate limited: status=429 body=%s", bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, wait); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("dingtalk server error: status=%d body=%s", resp.StatusCode, bodyPreview)
			if attempt < maxRetries {
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return lastErr
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: status=%d body=%s", datasource.ErrInvalidCredentials, resp.StatusCode, bodyPreview)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiErr apiErrorBody
			_ = json.Unmarshal(body, &apiErr)
			if apiErr.ErrMsg != "" {
				return fmt.Errorf("dingtalk api error: status=%d errcode=%d errmsg=%s",
					resp.StatusCode, apiErr.ErrCode, apiErr.ErrMsg)
			}
			return fmt.Errorf("dingtalk api error: status=%d body=%s", resp.StatusCode, bodyPreview)
		}

		var errCheck struct {
			ErrCode int    `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if err := json.Unmarshal(body, &errCheck); err == nil && errCheck.ErrCode != 0 {
			c.mu.Lock()
			c.accessToken = ""
			c.tokenExpiry = time.Time{}
			c.mu.Unlock()

			if attempt < maxRetries {
				lastErr = fmt.Errorf("dingtalk api error in response: errcode=%d errmsg=%s",
					errCheck.ErrCode, errCheck.ErrMsg)
				if sErr := sleepCtx(ctx, backoff[attempt]); sErr != nil {
					return sErr
				}
				continue
			}
			return fmt.Errorf("dingtalk api error: errcode=%d errmsg=%s",
				errCheck.ErrCode, errCheck.ErrMsg)
		}

		if result != nil {
			if err := json.Unmarshal(body, result); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

func parseRetryAfter(header string, fallback time.Duration) time.Duration {
	if header == "" {
		return fallback
	}
	if secs, err := time.ParseDuration(header + "s"); err == nil {
		if secs <= 0 {
			return 100 * time.Millisecond
		}
		return secs
	}
	return fallback
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (c *client) Ping(ctx context.Context) error {
	_, err := c.getAccessToken(ctx)
	return err
}

func (c *client) ListSpaces(ctx context.Context) ([]space, error) {
	var all []space
	nextToken := ""
	for {
		path := "/v1.0/doc/spaces"
		if nextToken != "" {
			path += "?nextToken=" + nextToken
		}
		var resp spaceListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if !resp.HasMore {
			break
		}
		nextToken = resp.NextToken
	}
	return all, nil
}

func (c *client) ListSpaceDocs(ctx context.Context, spaceID string) ([]docSummary, error) {
	var all []docSummary
	nextToken := ""
	for {
		path := fmt.Sprintf("/v1.0/doc/spaces/%s/docs", spaceID)
		if nextToken != "" {
			separator := "?"
			if strings.Contains(path, "?") {
				separator = "&"
			}
			path += fmt.Sprintf("%snextToken=%s", separator, nextToken)
		}
		var resp docListResponse
		if err := c.doRequest(ctx, http.MethodGet, path, &resp); err != nil {
			return nil, err
		}
		all = append(all, resp.Items...)
		if !resp.HasMore {
			break
		}
		nextToken = resp.NextToken
	}
	return all, nil
}

func (c *client) GetDocDetail(ctx context.Context, spaceID, docID string) (*docDetailResponse, error) {
	path := fmt.Sprintf("/v1.0/doc/spaces/%s/docs/%s", spaceID, docID)
	var resp docDetailResponse
	if err := c.doRequest(ctx, http.MethodGet, path, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

var (
	reImgWithAlt = regexp.MustCompile(`<img[^>]*src="([^"]*)"[^>]*alt="([^"]*)"[^>]*>`)
	reImgNoAlt   = regexp.MustCompile(`<img[^>]*src="([^"]*)"[^>]*>`)
	reLink       = regexp.MustCompile(`<a[^>]*href="([^"]*)"[^>]*>([^<]*)</a>`)
	reListItem   = regexp.MustCompile(`<li[^>]*>`)
)

func htmlToMarkdown(html string) string {
	if html == "" {
		return ""
	}
	result := html

	result = strings.ReplaceAll(result, "<p>", "")
	result = strings.ReplaceAll(result, "</p>", "\n\n")
	result = strings.ReplaceAll(result, "<div>", "")
	result = strings.ReplaceAll(result, "</div>", "\n")
	result = strings.ReplaceAll(result, "<br>", "\n")
	result = strings.ReplaceAll(result, "<br/>", "\n")
	result = strings.ReplaceAll(result, "<br />", "\n")
	result = strings.ReplaceAll(result, "<hr>", "\n---\n")
	result = strings.ReplaceAll(result, "<hr/>", "\n---\n")
	result = strings.ReplaceAll(result, "<hr />", "\n---\n")

	for i := 1; i <= 6; i++ {
		prefix := strings.Repeat("#", i)
		open := fmt.Sprintf("<h%d>", i)
		close := fmt.Sprintf("</h%d>", i)
		result = strings.ReplaceAll(result, open, prefix+" ")
		result = strings.ReplaceAll(result, close, "\n\n")
	}

	result = strings.ReplaceAll(result, "<strong>", "**")
	result = strings.ReplaceAll(result, "</strong>", "**")
	result = strings.ReplaceAll(result, "<b>", "**")
	result = strings.ReplaceAll(result, "</b>", "**")
	result = strings.ReplaceAll(result, "<em>", "*")
	result = strings.ReplaceAll(result, "</em>", "*")
	result = strings.ReplaceAll(result, "<i>", "*")
	result = strings.ReplaceAll(result, "</i>", "*")
	result = strings.ReplaceAll(result, "<u>", "")
	result = strings.ReplaceAll(result, "</u>", "")
	result = strings.ReplaceAll(result, "<s>", "~~")
	result = strings.ReplaceAll(result, "</s>", "~~")
	result = strings.ReplaceAll(result, "<del>", "~~")
	result = strings.ReplaceAll(result, "</del>", "~~")
	result = strings.ReplaceAll(result, "<code>", "`")
	result = strings.ReplaceAll(result, "</code>", "`")

	result = reImgWithAlt.ReplaceAllString(result, "![$2]($1)")
	result = reImgNoAlt.ReplaceAllString(result, "![image]($1)")
	result = reLink.ReplaceAllString(result, "[$2]($1)")

	result = strings.ReplaceAll(result, "<ul>", "")
	result = strings.ReplaceAll(result, "</ul>", "\n")
	result = strings.ReplaceAll(result, "<ol>", "")
	result = strings.ReplaceAll(result, "</ol>", "\n")
	result = reListItem.ReplaceAllString(result, "- ")
	result = strings.ReplaceAll(result, "</li>", "\n")

	result = strings.ReplaceAll(result, "\n\n\n", "\n\n")
	result = strings.ReplaceAll(result, "\n\n\n", "\n\n")

	return strings.TrimSpace(result)
}
