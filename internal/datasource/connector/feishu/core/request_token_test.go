package core

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Cached-token requests first check Err when entering gate.wait. Observe that
// boundary so the test expires the token only after every caller authenticated.
type feishuGateWaitContext struct {
	context.Context
	once    sync.Once
	entered chan<- struct{}
}

func (c *feishuGateWaitContext) Err() error {
	c.once.Do(func() { c.entered <- struct{}{} })
	return c.Context.Err()
}

func TestFeishuTokenRefreshPreservesPacingAndCooldown(t *testing.T) {
	for _, mode := range []string{"json", "download"} {
		t.Run(mode, func(t *testing.T) {
			resetFeishuRequestGates(t)
			t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "600")
			t.Setenv("FEISHU_RATE_LIMIT_MAX_WAIT", "5m")
			const count = 4
			const interval = 100 * time.Millisecond
			var mu sync.Mutex
			var arrivals []time.Time
			var earliestRequest time.Time
			var auths atomic.Int32
			var gate *feishuRequestGate
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
					auths.Add(1)
					// Slow enough for every caller to pass the gate in the broken
					// implementation and queue behind the refreshing caller's tokenMu.
					time.Sleep(time.Duration(count+1) * interval)
					mu.Lock()
					earliestRequest = time.Now().Add(3 * interval)
					mu.Unlock()
					// A concurrent data source can extend this shared cooldown while
					// our token is refreshing. All callers must honor the new delay.
					gate.cooldown(3 * interval)
					writeJSON(w, TokenResponse{TenantAccessToken: "fresh-token", Expire: 7200})
					return
				}
				if r.Header.Get("Authorization") != "Bearer fresh-token" {
					t.Error("request sent the token that expired while waiting")
				}
				mu.Lock()
				arrivals = append(arrivals, time.Now())
				mu.Unlock()
				_, _ = io.WriteString(w, `{"code":0}`)
			}))
			defer server.Close()
			c := NewClient(&Config{AppID: t.Name(), BaseURL: server.URL})
			c.tokenCache = "cached-token"
			c.tokenExpAt = time.Now().Add(time.Hour)
			method, path := http.MethodPost, "/open-apis/drive/v1/export_tasks"
			if mode == "download" {
				method, path = http.MethodGet, "/open-apis/drive/v1/export_tasks/file/export-token/download"
			}
			gate = c.requestGate(method, path)
			gate.cooldown(500 * time.Millisecond)
			entered := make(chan struct{}, count)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			for range count {
				wg.Add(1)
				go func() {
					defer wg.Done()
					observed := &feishuGateWaitContext{Context: ctx, entered: entered}
					var err error
					if mode == "download" {
						_, err = c.downloadRawBytes(observed, path)
					} else {
						err = c.DoRequest(observed, method, path, nil, nil)
					}
					if err != nil {
						t.Errorf("request: %v", err)
					}
				}()
			}
			for range count {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("requests failed to reach the gate")
				}
			}
			c.tokenMu.Lock()
			c.tokenExpAt = time.Now().Add(-time.Second)
			c.tokenMu.Unlock()
			wg.Wait()
			mu.Lock()
			defer mu.Unlock()
			if len(arrivals) != count || auths.Load() != 1 {
				t.Fatalf("requests/refreshes=%d/%d, want %d/1", len(arrivals), auths.Load(), count)
			}
			sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) })
			if arrivals[0].Before(earliestRequest) {
				t.Errorf("request ignored the cooldown imposed during token refresh")
			}
			for i := 1; i < len(arrivals); i++ {
				if gap := arrivals[i].Sub(arrivals[i-1]); gap < interval/2 {
					t.Errorf("requests burst after token refresh: gap=%s, interval=%s", gap, interval)
				}
			}
		})
	}
}
