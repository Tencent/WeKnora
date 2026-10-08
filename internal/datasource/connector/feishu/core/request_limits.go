package core

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultFeishuRequestsPerMinute = 80
	defaultFeishuRateLimitWait     = time.Minute
)

// Export APIs each allow 100 requests/minute. Reserve headroom and pace requests
// without bursts. Wiki listing has its own budget; other APIs only share a
// reactive cooldown. The registry spans clients, data sources and task retries
// in this process. Different App IDs and API hosts have independent budgets.
var feishuRequestGates sync.Map

type feishuRequestGateKey struct {
	baseURL string
	appID   string
	api     string
}

type feishuRequestGate struct {
	mu           sync.Mutex
	interval     time.Duration
	nextRequest  time.Time
	blockedUntil time.Time
}

func feishuRequestInterval() time.Duration {
	rpm, err := strconv.Atoi(strings.TrimSpace(os.Getenv("FEISHU_API_REQUESTS_PER_MINUTE")))
	if err != nil || rpm <= 0 || int64(rpm) > int64(time.Minute) {
		rpm = defaultFeishuRequestsPerMinute
	}
	return time.Minute / time.Duration(rpm)
}

func feishuRequestAPI(method, path string) (string, bool) {
	path, _, _ = strings.Cut(path, "?")
	path = strings.TrimRight(path, "/")
	if method == http.MethodPost && path == "/open-apis/drive/v1/export_tasks" {
		return "export-create", true
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/open-apis/drive/v1/export_tasks/") {
		if strings.HasPrefix(path, "/open-apis/drive/v1/export_tasks/file/") && strings.HasSuffix(path, "/download") {
			return "export-download", true
		}
		return "export-status", true
	}
	if method == http.MethodGet &&
		strings.HasPrefix(path, "/open-apis/wiki/v2/spaces/") && strings.HasSuffix(path, "/nodes") {
		return "wiki-node-list", true
	}
	return "other", false
}

func (c *Client) requestGate(method, path string) *feishuRequestGate {
	api, paced := feishuRequestAPI(method, path)
	key := feishuRequestGateKey{strings.TrimRight(c.baseURL, "/"), c.appID, api}
	if existing, ok := feishuRequestGates.Load(key); ok {
		return existing.(*feishuRequestGate)
	}
	gate := &feishuRequestGate{}
	if paced {
		gate.interval = feishuRequestInterval()
	}
	actual, _ := feishuRequestGates.LoadOrStore(key, gate)
	return actual.(*feishuRequestGate)
}

func (g *feishuRequestGate) wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		now := time.Now()
		readyAt := g.nextRequest
		if g.blockedUntil.After(readyAt) {
			readyAt = g.blockedUntil
		}
		wait := readyAt.Sub(now)
		if wait <= 0 {
			g.nextRequest = now.Add(g.interval)
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		if err := sleepCtx(ctx, wait); err != nil {
			return err
		}
	}
}

// Even an exhausted retry delays the next document, rather than letting a long
// sync continue hammering an API which has already rejected this application.
func (g *feishuRequestGate) cooldown(wait time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	until := time.Now().Add(wait)
	if until.After(g.blockedUntil) {
		g.blockedUntil = until
	}
}

func isFeishuRateLimited(status int, body []byte) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if status != http.StatusBadRequest && status != http.StatusOK {
		return false
	}
	var response struct {
		Code int `json:"code"`
	}
	return json.Unmarshal(body, &response) == nil && response.Code == 99991400
}

func feishuRateLimitWait(headers http.Header, now time.Time) time.Duration {
	var wait time.Duration
	valid := false
	for _, name := range []string{"x-ogw-ratelimit-reset", "Retry-After"} {
		value := strings.TrimSpace(headers.Get(name))
		if value == "" {
			continue
		}
		seconds, err := strconv.ParseFloat(value, 64)
		if err != nil && name == "Retry-After" {
			if date, dateErr := http.ParseTime(value); dateErr == nil {
				seconds, err = date.Sub(now).Seconds(), nil
			}
		}
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) ||
			seconds >= float64(math.MaxInt64)/float64(time.Second) {
			continue
		}
		valid = true
		candidate := 100 * time.Millisecond
		if seconds > 0 {
			candidate = time.Duration(seconds * float64(time.Second))
		}
		if candidate > wait {
			wait = candidate
		}
	}
	if valid {
		return wait
	}
	configured, err := time.ParseDuration(strings.TrimSpace(os.Getenv("FEISHU_RATE_LIMIT_RETRY_WAIT")))
	if err == nil && configured > 0 {
		return configured
	}
	return defaultFeishuRateLimitWait
}
