package outline

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"golang.org/x/time/rate"
)

const maxResponse = 32 << 20

var errTooLarge = errors.New("outline_document_too_large")
var limiters sync.Map

type apiError struct {
	Code   string
	Status int
}

func (e *apiError) Error() string { return e.Code }

func fatal(err error) bool {
	var api *apiError
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &api) && api.Status == http.StatusUnauthorized)
}

type client struct {
	base    string
	key     string
	http    *http.Client
	limiter *rate.Limiter
	metrics map[string]int64
}

func newClient(config *types.DataSourceConfig) (*client, error) {
	if config == nil {
		return nil, datasource.ErrInvalidConfig
	}
	base, _ := config.Credentials["base_url"].(string)
	key, _ := config.Credentials["api_key"].(string)
	base, key = strings.TrimSpace(base), strings.TrimSpace(key)
	if base == "" || key == "" {
		return nil, fmt.Errorf("%w: base_url and api_key are required", datasource.ErrInvalidConfig)
	}
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("%w: base_url must be an instance root URL", datasource.ErrInvalidConfig)
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = ""
	base = u.String()
	if err := datasource.ValidateConnectorBaseURL(base); err != nil {
		return nil, err
	}
	if u.Scheme == "http" {
		dnsCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupIPAddr(dnsCtx, u.Hostname())
		if err != nil || len(ips) == 0 || !utils.IsSSRFWhitelisted(u.Hostname()) {
			return nil, fmt.Errorf("%w: HTTP requires a whitelisted private host", datasource.ErrInvalidConfig)
		}
		for _, ip := range ips {
			if !ip.IP.IsPrivate() && !ip.IP.IsLoopback() {
				return nil, fmt.Errorf("%w: public HTTP is not allowed", datasource.ErrInvalidConfig)
			}
		}
	}
	h := datasource.NewConnectorHTTPClient(30 * time.Second)
	// RPC endpoints do not need redirects. Rejecting all also avoids POST-to-GET conversion.
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("outline_redirect_rejected") }
	digest := sha256.Sum256([]byte(base + "\x00" + key))
	limiter, _ := limiters.LoadOrStore(digest, rate.NewLimiter(rate.Every(time.Second), 1))
	return &client{base: base, key: key, http: h, limiter: limiter.(*rate.Limiter), metrics: map[string]int64{}}, nil
}

type envelope struct {
	Data       json.RawMessage `json:"data"`
	OK         *bool           `json:"ok"`
	Pagination struct {
		Total    *int    `json:"total"`
		NextPath *string `json:"nextPath"`
	} `json:"pagination"`
}

func wait(ctx context.Context, delay time.Duration) error {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(min(seconds, 7200)) * time.Second
	}
	if until, err := http.ParseTime(value); err == nil {
		return max(time.Duration(0), time.Until(until))
	}
	return time.Duration(2<<attempt) * time.Second
}

func (c *client) call(ctx context.Context, method string, body map[string]interface{}) (*envelope, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 4; attempt++ {
		start := time.Now()
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		c.metrics["rate_limit_wait_ms"] += time.Since(start).Milliseconds()
		c.metrics["api_requests"]++
		if attempt > 0 {
			c.metrics["retry_count"]++
		}
		if err := datasource.ValidateConnectorBaseURL(c.base); err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/"+method, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-API-Version", "2")
		resp, err := c.http.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == 3 {
				return nil, errors.New("outline_request_failed")
			}
			if err := wait(ctx, retryDelay("", attempt)+time.Duration(rand.IntN(250))*time.Millisecond); err != nil {
				return nil, err
			}
			continue
		}
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		resp.Body.Close()
		if len(data) > maxResponse {
			return nil, errTooLarge
		}
		if resp.StatusCode == 429 || resp.StatusCode >= 500 || readErr != nil {
			if attempt < 3 {
				delay := retryDelay(resp.Header.Get("Retry-After"), attempt)
				if resp.StatusCode == 429 {
					c.metrics["rate_limit_wait_ms"] += delay.Milliseconds()
				}
				if err := wait(ctx, delay); err != nil {
					return nil, err
				}
				continue
			}
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			code := "outline_request_failed"
			switch resp.StatusCode {
			case 401:
				code = "outline_auth_failed"
			case 403:
				code = "outline_permission_denied"
			case 429:
				code = "outline_rate_limited"
			}
			return nil, &apiError{Code: code, Status: resp.StatusCode}
		}
		if readErr != nil {
			return nil, errors.New("outline_response_invalid")
		}
		var result envelope
		if json.Unmarshal(data, &result) != nil || len(result.Data) == 0 ||
			string(result.Data) == "null" || (result.OK != nil && !*result.OK) {
			return nil, errors.New("outline_response_invalid")
		}
		return &result, nil
	}
	return nil, errors.New("outline_request_failed")
}

// walk uses only numeric pagination from nextPath; credentials never follow its URL.
func (c *client) walk(ctx context.Context, method string, body map[string]interface{}, page func([]json.RawMessage) error) error {
	offset, limit := 0, 25
	seen := map[string]bool{}
	for {
		body["offset"], body["limit"] = offset, limit
		result, err := c.call(ctx, method, body)
		if errors.Is(err, errTooLarge) && limit > 1 {
			limit = max(1, limit/2)
			continue
		}
		if err != nil {
			return err
		}
		var rows []json.RawMessage
		if json.Unmarshal(result.Data, &rows) != nil {
			return errors.New("outline_response_invalid")
		}
		fresh := make([]json.RawMessage, 0, len(rows))
		for _, raw := range rows {
			var id struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(raw, &id) != nil || id.ID == "" {
				return errors.New("outline_response_invalid")
			}
			if !seen[id.ID] {
				seen[id.ID] = true
				fresh = append(fresh, raw)
			}
		}
		if len(rows) > 0 && len(fresh) == 0 {
			return errors.New("outline_scan_incomplete")
		}
		if err := page(fresh); err != nil {
			return err
		}
		next := offset + len(rows)
		total := result.Pagination.Total
		if total != nil && (next > *total || (len(rows) == 0 && next < *total)) {
			return errors.New("outline_scan_incomplete")
		}
		if path := result.Pagination.NextPath; path != nil && *path != "" {
			u, err := url.Parse(*path)
			if err != nil || u.Path != "/api/"+method || (u.Host != "" && u.Scheme+"://"+u.Host != c.base) {
				return errors.New("outline_scan_incomplete")
			}
			n, err := strconv.Atoi(u.Query().Get("offset"))
			if err != nil || n <= offset || len(rows) == 0 || n != next {
				return errors.New("outline_scan_incomplete")
			}
			offset = n
			continue
		}
		if total != nil {
			if next == *total {
				return nil
			}
		} else if len(rows) < limit || (result.Pagination.NextPath != nil && *result.Pagination.NextPath == "") {
			return nil
		}
		if next <= offset {
			return errors.New("outline_scan_incomplete")
		}
		offset = next
	}
}
