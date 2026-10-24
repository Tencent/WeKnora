package lingo

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
	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
)

const (
	lingoPageSize         = 100
	lingoMaxResponseBytes = 8 << 20
	lingoMaxAttempts      = 4
)

// lingoRetryBaseDelay is a variable so tests can shorten the backoff.
var lingoRetryBaseDelay = 500 * time.Millisecond

// tokenProvider is implemented by core.Client. Extracted so client tests can
// inject a static token without reaching the real token endpoint.
type tokenProvider interface {
	GetTenantAccessToken(ctx context.Context) (string, error)
}

// lingoAPI is the small surface the connector needs. Tests substitute a fake.
type lingoAPI interface {
	ListRepos(ctx context.Context) ([]Repository, error)
	ListEntities(ctx context.Context, repoID string, pageSize int) ([]Entity, error)
}

type httpAPI struct {
	baseURL    string
	tokens     tokenProvider
	httpClient *http.Client
}

func newHTTPAPI(cfg *core.Config) lingoAPI {
	return &httpAPI{
		baseURL:    cfg.GetBaseURL(),
		tokens:     core.NewClient(cfg),
		httpClient: datasource.NewConnectorHTTPClient(30 * time.Second),
	}
}

// ListRepos returns every Lingo repository visible to the app.
func (a *httpAPI) ListRepos(ctx context.Context) ([]Repository, error) {
	var resp repoListResponse
	if err := a.get(ctx, "/open-apis/lingo/v1/repos", nil, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("lingo repos: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return resp.Data.Items, nil
}

// ListEntities walks every page of Lingo entities for one repository. An empty
// or default repo ID queries the organisation-wide dictionary.
func (a *httpAPI) ListEntities(ctx context.Context, repoID string, pageSize int) ([]Entity, error) {
	if pageSize <= 0 || pageSize > lingoPageSize {
		pageSize = lingoPageSize
	}
	params := url.Values{}
	params.Set("page_size", strconv.Itoa(pageSize))
	if repoID != "" && repoID != defaultRepoID {
		params.Set("repo_id", repoID)
	}

	var entities []Entity
	seenTokens := make(map[string]struct{})
	pageToken := ""
	for page := 0; page < 5000; page++ {
		if pageToken == "" {
			params.Del("page_token")
		} else {
			params.Set("page_token", pageToken)
		}
		var resp entityListResponse
		if err := a.get(ctx, "/open-apis/lingo/v1/entities", params, &resp); err != nil {
			return nil, err
		}
		if resp.Code != 0 {
			return nil, fmt.Errorf("lingo entities repo=%s: code=%d msg=%s", repoID, resp.Code, resp.Msg)
		}
		entities = append(entities, resp.Data.Entities...)
		if !resp.Data.HasMore {
			return entities, nil
		}
		next := resp.Data.PageToken
		if next == "" {
			return nil, fmt.Errorf("lingo entities repo=%s: has_more without page_token", repoID)
		}
		if _, ok := seenTokens[next]; ok {
			return nil, fmt.Errorf("lingo entities repo=%s: repeated page_token", repoID)
		}
		seenTokens[next] = struct{}{}
		pageToken = next
	}
	return nil, fmt.Errorf("lingo entities repo=%s: exceeded page limit", repoID)
}

func (a *httpAPI) get(ctx context.Context, path string, query url.Values, out interface{}) error {
	endpoint := a.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var lastErr error
	for attempt := 0; attempt < lingoMaxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * lingoRetryBaseDelay):
			}
		}

		token, err := a.tokens.GetTenantAccessToken(ctx)
		if err != nil {
			return fmt.Errorf("lingo auth: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/json")

		resp, err := a.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("lingo request: %w", err)
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, lingoMaxResponseBytes+1))
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read lingo response: %w", readErr)
			continue
		}
		if len(body) > lingoMaxResponseBytes {
			return fmt.Errorf("lingo response exceeds %d bytes", lingoMaxResponseBytes)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("decode lingo response: %w", err)
			}
			return nil
		}

		lastErr = fmt.Errorf("lingo HTTP %d: %s", resp.StatusCode, truncate(string(body), 300))
		if !retryableStatus(resp.StatusCode) {
			return lastErr
		}
	}
	return lastErr
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.TrimSpace(value[:limit]) + "..."
}
