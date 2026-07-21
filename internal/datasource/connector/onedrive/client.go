package onedrive

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
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
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	defaultGraphBase = "https://graph.microsoft.com/v1.0"
	maxJSONBody      = 4 << 20
	maxGraphAttempts = 4
	maxGraphPages    = 10000
)

type graphClient struct {
	baseURL      string
	httpClient   *http.Client
	accessToken  func(context.Context) (string, error)
	refreshToken func(context.Context) (string, error)
}

type graphError struct {
	StatusCode int
	Code       string
	Message    string
	RetryAfter time.Duration
}

func (e *graphError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("Microsoft Graph %s (%d): %s", e.Code, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Microsoft Graph returned HTTP %d", e.StatusCode)
}

func newGraphClient(
	baseURL string,
	httpClient *http.Client,
	token func(context.Context) (string, error),
	refresh func(context.Context) (string, error),
) *graphClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGraphBase
	}
	if httpClient == nil {
		httpClient = utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{Timeout: 2 * time.Minute, MaxRedirects: 5})
	}
	return &graphClient{baseURL: strings.TrimRight(baseURL, "/"), httpClient: httpClient, accessToken: token, refreshToken: refresh}
}

func (c *graphClient) getDrive(ctx context.Context) (*drive, error) {
	var result drive
	if err := c.getJSON(ctx, c.baseURL+"/me/drive", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *graphClient) getItem(ctx context.Context, driveID, itemID string) (*driveItem, error) {
	path := c.itemURL(driveID, itemID)
	var result driveItem
	if err := c.getJSON(ctx, path+"?$select=id,name,size,webUrl,lastModifiedDateTime,parentReference,file,folder", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *graphClient) listChildren(ctx context.Context, driveID, itemID string) ([]driveItem, error) {
	return c.listAll(ctx, c.itemURL(driveID, itemID)+"/children?$select=id,name,size,webUrl,lastModifiedDateTime,parentReference,file,folder")
}

func (c *graphClient) listAll(ctx context.Context, firstURL string) ([]driveItem, error) {
	items := make([]driveItem, 0)
	next := firstURL
	seen := make(map[string]struct{})
	pages := 0
	for next != "" {
		if pages >= maxGraphPages {
			return nil, fmt.Errorf("Microsoft Graph pagination exceeded %d pages", maxGraphPages)
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, fmt.Errorf("Microsoft Graph pagination cycle detected")
		}
		seen[next] = struct{}{}
		pages++
		var page itemCollection
		if err := c.getJSON(ctx, next, &page); err != nil {
			return nil, err
		}
		items = append(items, page.Value...)
		next = page.NextLink
	}
	return items, nil
}

func (c *graphClient) latestDelta(ctx context.Context, driveID string) (string, error) {
	endpoint := fmt.Sprintf("%s/drives/%s/root/delta?token=latest", c.baseURL, url.PathEscape(driveID))
	seen := make(map[string]struct{})
	pages := 0
	for endpoint != "" {
		if pages >= maxGraphPages {
			return "", fmt.Errorf("Microsoft Graph delta pagination exceeded %d pages", maxGraphPages)
		}
		if _, duplicate := seen[endpoint]; duplicate {
			return "", fmt.Errorf("Microsoft Graph delta pagination cycle detected")
		}
		seen[endpoint] = struct{}{}
		pages++
		var page itemCollection
		if err := c.getJSON(ctx, endpoint, &page); err != nil {
			return "", err
		}
		if page.DeltaLink != "" {
			return page.DeltaLink, nil
		}
		endpoint = page.NextLink
	}
	return "", fmt.Errorf("Microsoft Graph delta response did not include a delta link")
}

func (c *graphClient) delta(ctx context.Context, deltaURL string) ([]driveItem, string, error) {
	items := make([]driveItem, 0)
	next := deltaURL
	seen := make(map[string]struct{})
	pages := 0
	for next != "" {
		if pages >= maxGraphPages {
			return nil, "", fmt.Errorf("Microsoft Graph delta pagination exceeded %d pages", maxGraphPages)
		}
		if _, duplicate := seen[next]; duplicate {
			return nil, "", fmt.Errorf("Microsoft Graph delta pagination cycle detected")
		}
		seen[next] = struct{}{}
		pages++
		var page itemCollection
		if err := c.getJSON(ctx, next, &page); err != nil {
			return nil, "", err
		}
		items = append(items, page.Value...)
		if page.DeltaLink != "" {
			return items, page.DeltaLink, nil
		}
		next = page.NextLink
	}
	return nil, "", fmt.Errorf("Microsoft Graph delta response did not include a delta link")
}

func (c *graphClient) download(ctx context.Context, driveID, itemID string, maxBytes int64) ([]byte, error) {
	endpoint := c.itemURL(driveID, itemID) + "/content"
	resp, err := c.do(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeGraphError(resp)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file exceeds maximum size of %d bytes", maxBytes)
	}
	return data, nil
}

func (c *graphClient) getJSON(ctx context.Context, endpoint string, target interface{}) error {
	resp, err := c.do(ctx, endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeGraphError(resp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBody+1))
	if err != nil {
		return err
	}
	if len(body) > maxJSONBody {
		return fmt.Errorf("Microsoft Graph response exceeds %d bytes", maxJSONBody)
	}
	return json.Unmarshal(body, target)
}

func (c *graphClient) do(ctx context.Context, endpoint string) (*http.Response, error) {
	var lastErr error
	var forcedToken string
	refreshed401 := false
	for attempt := 0; attempt < maxGraphAttempts; attempt++ {
		token := forcedToken
		if token == "" {
			var err error
			token, err = c.accessToken(ctx)
			if err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, errors.New("invalid Microsoft Graph endpoint")
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")
		if requestID, requestErr := randomRequestID(); requestErr == nil {
			req.Header.Set("client-request-id", requestID)
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			// net/http errors include the full request URL, which may be an
			// opaque delta link containing a secret query token.
			lastErr = errors.New("Microsoft Graph transport error")
		} else if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			if refreshed401 || c.refreshToken == nil {
				return nil, datasource.ErrOAuthReauthorizationRequired
			}
			forcedToken, err = c.refreshToken(ctx)
			if err != nil {
				return nil, err
			}
			refreshed401 = true
			continue
		} else if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
			return resp, nil
		} else {
			graphErr := decodeGraphError(resp)
			resp.Body.Close()
			lastErr = graphErr
			if attempt == maxGraphAttempts-1 {
				break
			}
			delay := graphBackoff(attempt)
			if graphErr.RetryAfter > 0 {
				delay = graphErr.RetryAfter
			}
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			continue
		}
		if attempt < maxGraphAttempts-1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(graphBackoff(attempt)):
			}
		}
	}
	return nil, fmt.Errorf("Microsoft Graph request failed after retries: %w", lastErr)
}

func graphBackoff(attempt int) time.Duration {
	base := time.Duration(1<<attempt) * time.Second
	var sample [1]byte
	if _, err := cryptorand.Read(sample[:]); err != nil {
		return base
	}
	// Add up to 25% jitter to avoid synchronized retries across workers.
	return base + time.Duration(sample[0])*base/(4*255)
}

func (c *graphClient) itemURL(driveID, itemID string) string {
	base := fmt.Sprintf("%s/drives/%s", c.baseURL, url.PathEscape(driveID))
	if itemID == "root" {
		return base + "/root"
	}
	return base + "/items/" + url.PathEscape(itemID)
}

func decodeGraphError(resp *http.Response) *graphError {
	result := &graphError{StatusCode: resp.StatusCode}
	retryAfter := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		result.RetryAfter = time.Duration(seconds) * time.Second
	} else if retryAt, dateErr := http.ParseTime(retryAfter); dateErr == nil && time.Until(retryAt) > 0 {
		result.RetryAfter = time.Until(retryAt)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.NewDecoder(bytes.NewReader(body)).Decode(&envelope) == nil {
		result.Code = envelope.Error.Code
		result.Message = safeGraphMessage(envelope.Error.Message)
	}
	if len(result.Message) > 300 {
		result.Message = result.Message[:300]
	}
	return result
}

func safeGraphMessage(message string) string {
	message = strings.TrimSpace(message)
	lower := strings.ToLower(message)
	if strings.Contains(lower, "http://") || strings.Contains(lower, "https://") ||
		strings.Contains(lower, "token=") || strings.Contains(lower, "$deltatoken") {
		return "request rejected by Microsoft Graph"
	}
	return message
}

func isNotFound(err error) bool {
	var graphErr *graphError
	return errors.As(err, &graphErr) && graphErr.StatusCode == http.StatusNotFound
}

func isDeltaExpired(err error) bool {
	var graphErr *graphError
	if !errors.As(err, &graphErr) {
		return false
	}
	return graphErr.StatusCode == http.StatusGone || graphErr.Code == "resyncRequired" || graphErr.Code == "syncStateNotFound"
}

func randomRequestID() (string, error) {
	return datasourceRandomToken(16)
}
