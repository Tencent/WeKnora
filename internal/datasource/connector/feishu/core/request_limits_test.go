package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	resetFeishuRequestGates(t)
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
	ts, cfg := retryTestServer(t, "/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, _ *http.Request) {
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
			var attempts atomic.Int32
			var firstAttempt atomic.Int64
			ts, cfg := retryTestServer(t, "/target", func(w http.ResponseWriter, _ *http.Request) {
				if attempts.Add(1) == 1 {
					firstAttempt.Store(time.Now().UnixNano())
					w.Header().Set("x-ogw-ratelimit-reset", "0.05")
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, `{"code":99991400,"msg":"request trigger frequency limit"}`)
					return
				}
				if time.Since(time.Unix(0, firstAttempt.Load())) < 50*time.Millisecond {
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
			if attempts.Load() != 2 {
				t.Fatalf("attempts=%d, want 2", attempts.Load())
			}
		})
	}
}

func TestFeishuCooldownBlocksOtherClientAndHonorsCancellation(t *testing.T) {
	var attempts atomic.Int32
	ts, cfg := retryTestServer(t, "/target", func(w http.ResponseWriter, _ *http.Request) {
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
	ts, cfg := retryTestServer(t, "/target", func(w http.ResponseWriter, _ *http.Request) {
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

// Gate tests are serial (no t.Parallel): clear before and after each test so
// even a reused httptest port cannot inherit pacing or cooldown state.
func resetFeishuRequestGates(t *testing.T) {
	t.Helper()
	feishuRequestGates.Clear()
	t.Cleanup(feishuRequestGates.Clear)
}

func TestFeishuRequestGateReset(t *testing.T) {
	config := &Config{AppID: t.Name(), BaseURL: "http://reused-test-host"}
	path := "/open-apis/drive/v1/export_tasks"
	old := NewClient(config).requestGate(http.MethodPost, path)
	old.cooldown(time.Minute)
	t.Run("reset", func(t *testing.T) {
		resetFeishuRequestGates(t)
		fresh := NewClient(config).requestGate(http.MethodPost, path)
		if fresh == old || !fresh.blockedUntil.IsZero() || !fresh.nextRequest.IsZero() {
			t.Fatal("reused host/app/API inherited gate state")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		if err := fresh.wait(ctx); err != nil {
			t.Fatalf("fresh gate still blocked: %v", err)
		}
	})
	key := feishuRequestGateKey{config.BaseURL, config.AppID, "export-create"}
	if _, exists := feishuRequestGates.Load(key); exists {
		t.Fatal("test cleanup left a gate in the registry")
	}
}

func TestFeishuCooldownConfigurationAndBounds(t *testing.T) {
	t.Setenv("FEISHU_RATE_LIMIT_MAX_WAIT", "25ms")
	t.Setenv("FEISHU_RATE_LIMIT_RETRY_WAIT", "1h")
	if got := feishuRateLimitWait(http.Header{}, time.Now()); got != 25*time.Millisecond {
		t.Fatalf("fallback escaped cap: %s", got)
	}
	headers := http.Header{"Retry-After": {"86400"}}
	if got := feishuRateLimitWait(headers, time.Now()); got != 25*time.Millisecond {
		t.Fatalf("header escaped configured cap: %s", got)
	}
	headers.Set("Retry-After", time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
	if got := feishuRateLimitWait(headers, time.Now()); got != 25*time.Millisecond {
		t.Fatalf("HTTP date escaped configured cap: %s", got)
	}
	gate := &feishuRequestGate{}
	gate.cooldown(time.Hour)
	// Check the remaining cooldown, excluding any time the test was descheduled
	// before cooldown() ran. Scheduler latency must not look like an excessive cap.
	if remaining := time.Until(gate.blockedUntil); remaining > 25*time.Millisecond {
		t.Fatalf("shared gate escaped cap: %s", remaining)
	}
	for _, invalid := range []string{"", "0s", "-1s", "bad"} {
		t.Setenv("FEISHU_RATE_LIMIT_MAX_WAIT", invalid)
		if got := feishuRateLimitWait(headers, time.Now()); got != 5*time.Minute {
			t.Fatalf("invalid cap %q: got %s", invalid, got)
		}
	}
}

func BenchmarkFeishuRateLimitSuccess(b *testing.B) {
	body := append([]byte(`{"code":0,"data":"`), bytes.Repeat([]byte("x"), 16<<20)...)
	body = append(body, []byte(`"}`)...)
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		if isFeishuRateLimited(http.StatusOK, body) {
			b.Fatal("successful response classified as a rate limit")
		}
	}
}

func TestFeishuReactiveCooldownsArePerAPI(t *testing.T) {
	resetFeishuRequestGates(t)
	c := NewClient(&Config{AppID: t.Name(), BaseURL: "http://limits-test"})
	pairs := [][2]string{
		{"/open-apis/drive/v1/files/one/download", "/open-apis/drive/v1/files/two/download?x=y"},
		{"/open-apis/drive/v1/medias/one/download", "/open-apis/drive/v1/medias/two/download"},
		{"/open-apis/docx/v1/documents/one/blocks", "/open-apis/docx/v1/documents/two/blocks?page_token=x"},
		{"/open-apis/docx/v1/documents/one/raw_content", "/open-apis/docx/v1/documents/two/raw_content"},
		{"/open-apis/sheets/v2/spreadsheets/one/values/A1", "/open-apis/sheets/v2/spreadsheets/two/values/B2"},
		{"/open-apis/bitable/v1/apps/one/tables/t1/fields", "/open-apis/bitable/v1/apps/two/tables/t2/fields"},
		{
			"/open-apis/bitable/v1/apps/one/tables/t1/records/search",
			"/open-apis/bitable/v1/apps/two/tables/t2/records/search",
		},
		{"/open-apis/drive/explorer/v2/folder/one/meta", "/open-apis/drive/explorer/v2/folder/two/meta"},
		{"/open-apis/wiki/v2/spaces/get_node?token=one", "/open-apis/wiki/v2/spaces/get_node?token=two"},
		{"/open-apis/wiki/v2/spaces?page_token=one", "/open-apis/wiki/v2/spaces?page_token=two"},
	}
	seen := make(map[*feishuRequestGate]bool)
	for _, pair := range pairs {
		gate := c.requestGate(http.MethodGet, pair[0])
		if gate != c.requestGate(http.MethodGet, pair[1]) || gate.interval != 0 {
			t.Fatalf("reactive gate must normalize tokens and query: %v", pair)
		}
		if seen[gate] {
			t.Fatalf("unrelated APIs share a cooldown: %v", pair)
		}
		seen[gate] = true
		if gate == c.requestGate(http.MethodPost, pair[0]) {
			t.Fatalf("HTTP methods share a cooldown: %v", pair)
		}
	}
	media := c.requestGate(http.MethodGet, pairs[1][0])
	media.cooldown(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := c.requestGate(http.MethodGet, pairs[2][0]).wait(ctx); err != nil {
		t.Fatalf("media cooldown blocked docx: %v", err)
	}
	if err := media.wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("media did not retain its own cooldown: %v", err)
	}
}

func TestFeishuRateLimitDetection(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
		want   bool
	}{
		{http.StatusOK, `{"code":0,"data":{"text":"99991400"}}`, false},
		{http.StatusOK, `{"code":99991400}`, true},
		{http.StatusBadRequest, `{"code":99991400}`, true},
		{http.StatusOK, `{"code":0}`, false},
		{http.StatusOK, `invalid 99991400`, false},
		{http.StatusForbidden, `{"code":99991400}`, false},
		{http.StatusTooManyRequests, ``, true},
	} {
		if got := isFeishuRateLimited(tt.status, []byte(tt.body)); got != tt.want {
			t.Errorf("rate limited(%d, %s) = %v, want %v", tt.status, tt.body, got, tt.want)
		}
	}
}

// Run HTTP handlers in process so synctest can advance time without real sockets.
type feishuHandlerTransport struct {
	http.Handler
}

func (tr feishuHandlerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	recorder := httptest.NewRecorder()
	tr.ServeHTTP(recorder, r)
	return recorder.Result(), nil
}

func TestFeishuExportPollingRetainsTicketAcrossCooldown(t *testing.T) {
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "80")
	t.Setenv("FEISHU_RATE_LIMIT_RETRY_WAIT", "70s")
	t.Setenv("FEISHU_RATE_LIMIT_MAX_WAIT", "5m")
	t.Setenv("FEISHU_EXPORT_MAX_POLLS", "2")
	t.Setenv("FEISHU_EXPORT_POLL_INTERVAL", "2s")
	synctest.Test(t, func(t *testing.T) {
		resetFeishuRequestGates(t)
		var creates, polls, downloads int
		mux := http.NewServeMux()
		mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, _ *http.Request) {
			creates++
			_, _ = io.WriteString(w, `{"code":0,"data":{"ticket":"same-ticket"}}`)
		})
		mux.HandleFunc("/open-apis/drive/v1/export_tasks/same-ticket", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("token") != "document" {
				t.Error("poll did not retain the document token")
			}
			polls++
			switch polls {
			case 1:
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"code":99991400}`)
			case 2:
				_, _ = io.WriteString(w, `{"code":0,"data":{"result":{"job_status":2}}}`)
			default:
				_, _ = io.WriteString(w,
					`{"code":0,"data":{"result":{"job_status":0,"file_token":"file","file_name":"doc.pdf"}}}`)
			}
		})
		mux.HandleFunc("/open-apis/drive/v1/export_tasks/file/file/download",
			func(w http.ResponseWriter, _ *http.Request) {
				downloads++
				_, _ = io.WriteString(w, "export-content")
			})
		cfg := &Config{AppID: t.Name(), BaseURL: "http://feishu-export.test"}
		c := NewClient(cfg)
		c.httpClient.Transport = feishuHandlerTransport{mux}
		c.tokenCache, c.tokenExpAt = "cached-token", time.Now().Add(time.Hour)
		// Both shared and in-poll cooldowns exceed the previous 60-second deadline.
		started := time.Now()
		other := NewClient(cfg)
		other.requestGate(http.MethodGet, "/open-apis/drive/v1/export_tasks/other-ticket").cooldown(70 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		data, name, err := c.ExportAndDownload(ctx, "document", "docx")
		if err != nil || name != "doc.pdf" || string(data) != "export-content" {
			t.Fatalf("export across cooldown: data=%q name=%q err=%v", data, name, err)
		}
		if creates != 1 || polls != 3 || downloads != 1 {
			t.Fatalf("create/poll/download=%d/%d/%d, want 1/3/1", creates, polls, downloads)
		}
		if elapsed := time.Since(started); elapsed < 140*time.Second {
			t.Fatalf("export skipped the shared or in-poll cooldown: %s", elapsed)
		}
	})
}

func TestFeishuExportPollingIsBoundedAndCancellable(t *testing.T) {
	for _, cancelWhileWaiting := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelWhileWaiting), func(t *testing.T) {
			t.Setenv("FEISHU_EXPORT_MAX_POLLS", "3")
			t.Setenv("FEISHU_EXPORT_POLL_INTERVAL", "1ms")
			t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "60000")
			var creates, polls atomic.Int32
			createHandler := func(w http.ResponseWriter, _ *http.Request) {
				creates.Add(1)
				_, _ = io.WriteString(w, `{"code":0,"data":{"ticket":"pending"}}`)
			}
			ts, cfg := retryTestServer(t, "/open-apis/drive/v1/export_tasks", createHandler)
			defer ts.Close()
			ts.Config.Handler.(*http.ServeMux).HandleFunc("/open-apis/drive/v1/export_tasks/pending",
				func(w http.ResponseWriter, _ *http.Request) {
					polls.Add(1)
					_, _ = io.WriteString(w, `{"code":0,"data":{"result":{"job_status":2}}}`)
				})
			c := NewClient(cfg)
			if cancelWhileWaiting {
				c.requestGate(http.MethodGet, "/open-apis/drive/v1/export_tasks/pending").cooldown(time.Second)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, _, err := c.ExportAndDownload(ctx, "document", "docx")
			if cancelWhileWaiting {
				if !errors.Is(err, context.DeadlineExceeded) || polls.Load() != 0 {
					t.Fatalf("polling ignored cancellation: polls=%d err=%v", polls.Load(), err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "after 3 polls") || polls.Load() != 3 {
				t.Fatalf("polling is unbounded: polls=%d err=%v", polls.Load(), err)
			}
			// Exercise the same wrapping and metadata path used by the connectors.
			metadata := FeishuErrorItemMeta(fmt.Errorf("export document: %w", err), nil)
			if metadata["error_reason_code"] != "feishu_timeout" {
				t.Fatalf("polling lost its timeout classification: %v", metadata)
			}
			if strings.Contains(metadata["error_reason"], "ticket=") {
				t.Fatal("export ticket leaked into the user-facing failure reason")
			}
			if creates.Load() != 1 {
				t.Fatalf("created %d tasks, want 1", creates.Load())
			}
		})
	}
}

func TestFeishuRefreshesTokenAfterCooldown(t *testing.T) {
	for _, download := range []bool{false, true} {
		t.Run(fmt.Sprint(download), func(t *testing.T) {
			resetFeishuRequestGates(t)
			var auths, attempts atomic.Int32
			var c *Client
			mux := http.NewServeMux()
			authHandler := func(w http.ResponseWriter, _ *http.Request) {
				token := fmt.Sprintf("token-%d", auths.Add(1))
				writeJSON(w, TokenResponse{TenantAccessToken: token, Expire: 7200})
			}
			mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", authHandler)
			mux.HandleFunc("/target", func(w http.ResponseWriter, r *http.Request) {
				if attempts.Add(1) == 1 {
					c.tokenMu.Lock()
					c.tokenExpAt = time.Now().Add(5 * time.Millisecond)
					c.tokenMu.Unlock()
					w.Header().Set("Retry-After", "0.03")
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				if r.Header.Get("Authorization") != "Bearer token-2" {
					t.Error("request reused the token that expired during cooldown")
				}
				_, _ = io.WriteString(w, `{"code":0}`)
			})
			ts := httptest.NewServer(mux)
			defer ts.Close()
			c = NewClient(&Config{AppID: t.Name(), BaseURL: ts.URL})
			var err error
			if download {
				_, err = c.downloadRawBytes(context.Background(), "/target")
			} else {
				err = c.DoRequest(context.Background(), http.MethodGet, "/target", nil, nil)
			}
			if err != nil || auths.Load() != 2 || attempts.Load() != 2 {
				t.Fatalf("auth/attempts=%d/%d err=%v", auths.Load(), attempts.Load(), err)
			}
		})
	}
}
