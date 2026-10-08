package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFeishuRequestIntervalConfiguration(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  time.Duration
	}{
		{"", 750 * time.Millisecond},
		{"80", 750 * time.Millisecond},
		{"60", time.Second},
		{"0", 750 * time.Millisecond},
		{"-1", 750 * time.Millisecond},
		{"invalid", 750 * time.Millisecond},
	} {
		t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", tt.value)
		if got := feishuRequestInterval(); got != tt.want {
			t.Errorf("interval(%q)=%s, want %s", tt.value, got, tt.want)
		}
	}
	t.Setenv("FEISHU_RATE_LIMIT_RETRY_WAIT", "7s")
	if got := feishuRateLimitWait(http.Header{}, time.Now()); got != 7*time.Second {
		t.Fatalf("configured fallback = %s", got)
	}
}

func TestFeishuGatesShareAppAndAPIIndependentOfDocument(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "80")
	first := NewClient(&Config{AppID: t.Name(), BaseURL: "http://limits-test"})
	second := NewClient(&Config{AppID: t.Name(), BaseURL: "http://limits-test/"})
	poll := first.requestGate(http.MethodGet, "/open-apis/drive/v1/export_tasks/ticket-one?token=one")
	if poll != second.requestGate(http.MethodGet, "/open-apis/drive/v1/export_tasks/ticket-two?token=two") {
		t.Fatal("ticket and query must not create independent rate budgets")
	}
	if poll.interval != 750*time.Millisecond {
		t.Fatalf("default interval = %s", poll.interval)
	}
	create := first.requestGate(http.MethodPost, "/open-apis/drive/v1/export_tasks")
	download := first.requestGate(http.MethodGet, "/open-apis/drive/v1/export_tasks/file/one/download")
	if create == poll || create == download || poll == download {
		t.Fatal("export APIs must have separate budgets")
	}
	if download != second.requestGate(http.MethodGet, "/open-apis/drive/v1/export_tasks/file/two/download") {
		t.Fatal("download tokens must share one budget")
	}
	otherApp := NewClient(&Config{AppID: t.Name() + "-other", BaseURL: first.baseURL})
	otherHost := NewClient(&Config{AppID: t.Name(), BaseURL: "http://other-host"})
	if create == otherApp.requestGate(http.MethodPost, "/open-apis/drive/v1/export_tasks") ||
		create == otherHost.requestGate(http.MethodPost, "/open-apis/drive/v1/export_tasks") {
		t.Fatal("different applications and API hosts must not share a budget")
	}
	nodes := first.requestGate(http.MethodGet, "/open-apis/wiki/v2/spaces/one/nodes?parent_node_token=one")
	if nodes != second.requestGate(http.MethodGet, "/open-apis/wiki/v2/spaces/two/nodes?parent_node_token=two") ||
		nodes.interval != poll.interval {
		t.Fatal("wiki list paths must share a paced budget")
	}
}

func TestFeishuGatePacesConcurrentClients(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "3000") // 20ms for a bounded test.
	var mu sync.Mutex
	var arrivals []time.Time
	ts, cfg := retryTestServer("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		writeJSON(w, ApiResponse{Code: 0})
	})
	defer ts.Close()
	clients := []*Client{NewClient(cfg), NewClient(cfg)}
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := clients[i%2].DoRequest(context.Background(),
				http.MethodPost, "/open-apis/drive/v1/export_tasks", nil, nil)
			if err != nil {
				t.Errorf("request: %v", err)
			}
		}(i)
	}
	wg.Wait()
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) })
	if len(arrivals) != 6 {
		t.Fatalf("received %d requests", len(arrivals))
	}
	if elapsed := arrivals[5].Sub(arrivals[0]); elapsed < 90*time.Millisecond {
		t.Fatalf("clients burst through shared budget: elapsed=%s", elapsed)
	}
}

func TestFeishuLegacyRateLimitRetriesJSONAndDownload(t *testing.T) {
	for _, tt := range []struct {
		name     string
		download bool
		status   int
	}{
		{"json_400", false, http.StatusBadRequest},
		{"json_200", false, http.StatusOK},
		{"download_400", true, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var attempts int
			var firstAttempt time.Time
			ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				if attempts == 1 {
					firstAttempt = time.Now()
					w.Header().Set("x-ogw-ratelimit-reset", "0.05")
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, `{"code":99991400,"msg":"request trigger frequency limit"}`)
					return
				}
				if time.Since(firstAttempt) < 50*time.Millisecond {
					t.Error("request retried before Feishu reset time")
				}
				if tt.download {
					_, _ = io.WriteString(w, "file-content")
				} else {
					writeJSON(w, ApiResponse{Code: 0})
				}
			})
			defer ts.Close()
			c := NewClient(cfg)
			if tt.download {
				data, err := c.downloadRawBytes(context.Background(), "/target")
				if err != nil || string(data) != "file-content" {
					t.Fatalf("download=%q err=%v", data, err)
				}
			} else if err := c.DoRequest(context.Background(), http.MethodGet, "/target", nil, nil); err != nil {
				t.Fatal(err)
			}
			if attempts != 2 {
				t.Fatalf("attempts=%d, want 2", attempts)
			}
		})
	}
}

func TestFeishuCooldownBlocksOtherClientAndHonorsCancellation(t *testing.T) {
	var attempts atomic.Int32
	ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("x-ogw-ratelimit-reset", "0.2")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":99991400}`)
	})
	defer ts.Close()
	first := NewClient(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := first.DoRequest(ctx, http.MethodGet, "/target", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait should be cancellable: %v", err)
	}
	second := NewClient(cfg)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel2()
	if err := second.DoRequest(ctx2, http.MethodGet, "/target", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("other client must inherit cooldown: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("cooldown allowed extra requests: %d", got)
	}
}

func TestFeishuRateLimitExhaustionProtectsNextDocument(t *testing.T) {
	var attempts atomic.Int32
	ts, cfg := retryTestServer("/target", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		w.Header().Set("Retry-After", "0.05")
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer ts.Close()
	c := NewClient(cfg)
	if err := c.DoRequest(context.Background(), http.MethodGet, "/target", nil, nil); err == nil {
		t.Fatal("persistent rate limit must not report success")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := NewClient(cfg).DoRequest(ctx, http.MethodGet, "/target", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("next document must wait after exhausted retries: %v", err)
	}
	if got := attempts.Load(); got != 4 {
		t.Fatalf("attempts=%d, want bounded 4", got)
	}
}
