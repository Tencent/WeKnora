package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestParseRetryAfter(t *testing.T) {
	fallback := 5 * time.Second
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", fallback},
		{"0", 100 * time.Millisecond},
		{"-1", 100 * time.Millisecond},
		{"3", 3 * time.Second},
		{"abc", fallback},
		{"10", 10 * time.Second},
	}
	for _, tt := range tests {
		got := parseRetryAfter(tt.header, fallback)
		if got != tt.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"exactly10!", 10, "exactly10!"},
		{"this is longer", 10, "this is lo..."},
		{"", 10, ""},
		{"中文测试", 5, "中..."},
	}

	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.expected {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.expected)
		}
	}
}

func TestSleepCtx_Canceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := sleepCtx(ctx, time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestSleepCtx_Completes(t *testing.T) {
	ctx := context.Background()
	start := time.Now()
	err := sleepCtx(ctx, 50*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("sleep was too short: %v", elapsed)
	}
}

func resetAccessTokenCacheForTest() {
	accessTokenCache.Lock()
	defer accessTokenCache.Unlock()
	accessTokenCache.entries = make(map[string]cachedAccessToken)
}

func allowLocalDingTalkHTTPForTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST_EXTRA", "127.0.0.1,localhost,::1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

const asyncTestTimeout = 5 * time.Second

type doneObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *doneObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

type repeatingByteReader byte

func (r repeatingByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func waitForSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(asyncTestTimeout):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitForError(t *testing.T, ch <-chan error, name string) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(asyncTestTimeout):
		t.Fatalf("timed out waiting for %s", name)
		return nil
	}
}

// clientTestServer wraps httptest.Server with DingTalk API handlers for testing.
type clientTestServer struct {
	server      *httptest.Server
	mux         *http.ServeMux
	token       string
	workspaceID string
}

func newClientTestServer() *clientTestServer {
	cts := &clientTestServer{
		mux: http.NewServeMux(),
	}
	cts.server = httptest.NewServer(cts.mux)
	cts.setupHandlers()
	return cts
}

func (cts *clientTestServer) Close() {
	cts.server.Close()
}

func (cts *clientTestServer) setupHandlers() {
	// Token endpoint
	cts.mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		var req accessTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.AppKey == "valid-key" && req.AppSecret == "valid-secret" {
			json.NewEncoder(w).Encode(accessTokenResponse{
				AccessToken: "test-access-token-123",
				ExpireIn:    7200,
			})
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(dingtalkErrorResponse{
				ErrCode: 401,
				ErrMsg:  "invalid credentials",
			})
		}
	})

	// Workspaces endpoint
	cts.mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("x-acs-dingtalk-access-token")
		w.Header().Set("Content-Type", "application/json")
		if token != "test-access-token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(dingtalkErrorResponse{
				ErrCode: 401,
				ErrMsg:  "unauthorized",
			})
			return
		}
		json.NewEncoder(w).Encode(wikiWorkspacesResponse{
			Workspaces: []WikiWorkspace{
				{
					WorkspaceID:  "ws-test-123",
					Name:         "测试知识库",
					Type:         "TEAM",
					RootNodeID:   "root-node-456",
					URL:          "https://wiki.dingtalk.com/space/ws-test-123",
					ModifiedTime: "2026-01-15T10:00:00+08:00",
				},
			},
		})
	})

	// Nodes endpoint
	cts.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("x-acs-dingtalk-access-token")
		w.Header().Set("Content-Type", "application/json")
		if token != "test-access-token-123" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(dingtalkErrorResponse{
				ErrCode: 401,
				ErrMsg:  "unauthorized",
			})
			return
		}
		json.NewEncoder(w).Encode(wikiNodesResponse{
			Nodes: []WikiNode{
				{
					NodeID:       "node-1",
					Name:         "测试文档",
					NodeType:     "FILE",
					Category:     "ALIDOC",
					URL:          "https://wiki.dingtalk.com/doc/node-1",
					ModifiedTime: "2026-01-15T10:00:00+08:00",
					WordCount:    100,
				},
				{
					NodeID:       "folder-1",
					Name:         "测试文件夹",
					NodeType:     "FOLDER",
					Category:     "FOLDER",
					ModifiedTime: "2026-01-15T10:00:00+08:00",
				},
			},
		})
	})
}

// TestClientDoRequest_Unauthorized tests that 401 errors wrap ErrInvalidCredentials.
func TestClientDoRequest_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"errcode":401,"errmsg":"unauthorized"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for 401")
	}
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Errorf("expected ErrInvalidCredentials, got: %v", err)
	}
}

func TestClientDoRequest_RefreshesTokenOnceAfterUnauthorized(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	var tokenRequests int
	var apiRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"accessToken":"token-%d","expireIn":7200}`, tokenRequests)
	})
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("x-acs-dingtalk-access-token") {
		case "token-1":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"InvalidAccessToken","message":"token expired"}`)
		case "token-2":
			_, _ = io.WriteString(w, `{"value":"ok"}`)
		default:
			t.Errorf("unexpected access token %q", r.Header.Get("x-acs-dingtalk-access-token"))
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		httpClient:   srv.Client(),
	}

	var result map[string]string
	if err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, &result); err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if result["value"] != "ok" {
		t.Fatalf("result = %v, want value=ok", result)
	}
	if tokenRequests != 2 || apiRequests != 2 {
		t.Fatalf("requests token=%d api=%d, want token=2 api=2", tokenRequests, apiRequests)
	}
}

func TestClientDoRequest_PersistentUnauthorizedRefreshesOnlyOnce(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	var tokenRequests int
	var apiRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"accessToken":"token-%d","expireIn":7200}`, tokenRequests)
	})
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":"InvalidAccessToken","message":"token expired"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		httpClient:   srv.Client(),
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, nil)
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("doRequest() error = %v, want ErrInvalidCredentials", err)
	}
	if tokenRequests != 2 || apiRequests != 2 {
		t.Fatalf("requests token=%d api=%d, want token=2 api=2", tokenRequests, apiRequests)
	}
}

func TestClientDoRequest_PersistentUnauthorizedInvalidatesRejectedRefreshToken(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	var tokenRequests int
	var apiRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"fresh-but-rejected","expireIn":7200}`)
	})
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"code":"InvalidAccessToken","message":"token expired"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		accessToken:  "stale-token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   srv.Client(),
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, nil)
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("doRequest() error = %v, want ErrInvalidCredentials", err)
	}
	if tokenRequests != 1 || apiRequests != 2 {
		t.Fatalf("requests token=%d api=%d, want token=1 api=2", tokenRequests, apiRequests)
	}
	if got := c.accessTokenSnapshot(); got != "" {
		t.Errorf("local access token = %q after terminal 401, want invalidated", got)
	}
	if token, _, ok := loadCachedAccessToken(c.cacheKey()); ok {
		t.Errorf("shared cache retained rejected token %q after terminal 401", token)
	}
}

func TestClientDoRequest_ConcurrentStaleTokenDoesNotInvalidateFreshToken(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	var mu sync.Mutex
	tokenRequests := 0
	staleRequests := 0
	freshRequests := 0
	bothStaleStarted := make(chan struct{})
	freshRequestStarted := make(chan struct{})
	var bothStaleOnce sync.Once
	var freshOnce sync.Once

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		tokenRequests++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"fresh-token","expireIn":7200}`)
	})
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch token := r.Header.Get("x-acs-dingtalk-access-token"); token {
		case "stale-token":
			mu.Lock()
			staleRequests++
			staleRequestNumber := staleRequests
			if staleRequests == 2 {
				bothStaleOnce.Do(func() { close(bothStaleStarted) })
			}
			mu.Unlock()

			select {
			case <-bothStaleStarted:
			case <-r.Context().Done():
				return
			case <-time.After(asyncTestTimeout):
				t.Errorf("timed out waiting for both stale-token requests")
				http.Error(w, "stale request barrier timed out", http.StatusInternalServerError)
				return
			}
			if staleRequestNumber == 2 {
				select {
				case <-freshRequestStarted:
				case <-r.Context().Done():
					return
				case <-time.After(asyncTestTimeout):
					t.Errorf("timed out waiting for the first fresh-token request")
					http.Error(w, "fresh request barrier timed out", http.StatusInternalServerError)
					return
				}
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"InvalidAccessToken","message":"token expired"}`)
		case "fresh-token":
			mu.Lock()
			freshRequests++
			mu.Unlock()
			freshOnce.Do(func() { close(freshRequestStarted) })
			_, _ = io.WriteString(w, `{"value":"ok"}`)
		default:
			t.Errorf("unexpected access token %q", token)
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		accessToken:  "stale-token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   srv.Client(),
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			var result map[string]string
			err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, &result)
			if err == nil && result["value"] != "ok" {
				err = fmt.Errorf("result = %v, want value=ok", result)
			}
			errs <- err
		}()
	}

	waitForSignal(t, bothStaleStarted, "both stale-token requests")
	for i := 0; i < 2; i++ {
		if err := waitForError(t, errs, fmt.Sprintf("authenticated request %d", i)); err != nil {
			t.Fatalf("concurrent doRequest() error = %v", err)
		}
	}
	mu.Lock()
	gotTokenRequests := tokenRequests
	gotStaleRequests := staleRequests
	gotFreshRequests := freshRequests
	mu.Unlock()
	if gotTokenRequests != 1 || gotStaleRequests != 2 || gotFreshRequests != 2 {
		t.Fatalf(
			"requests token=%d stale=%d fresh=%d, want token=1 stale=2 fresh=2",
			gotTokenRequests,
			gotStaleRequests,
			gotFreshRequests,
		)
	}
}

// TestClientDoRequest_Forbidden tests that permission failures are not reported
// as invalid credentials.
func TestClientDoRequest_Forbidden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"Forbidden.AccessDenied","message":"permission denied"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for 403")
	}
	if errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Errorf("permission error should not be reported as invalid credentials: %v", err)
	}
	var apiErr *dingtalkAPIError
	if !errors.As(err, &apiErr) || apiErr.Code != "Forbidden.AccessDenied" {
		t.Errorf("expected DingTalk API permission error, got: %T %v", err, err)
	}
}

func TestClientDoRequest_ForbiddenPermissionTextDoesNotRefreshToken(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	var tokenRequests int
	var apiRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"fresh-token","expireIn":7200}`)
	})
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"Forbidden.AccessDenied","message":"caller is unauthorized to read this workspace"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		accessToken:  "valid-token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   srv.Client(),
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, nil)
	var apiErr *dingtalkAPIError
	if !errors.As(err, &apiErr) || apiErr.Code != "Forbidden.AccessDenied" {
		t.Fatalf("doRequest() error = %T %v, want permission API error", err, err)
	}
	if errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("permission error was misclassified as invalid credentials: %v", err)
	}
	if tokenRequests != 0 || apiRequests != 1 {
		t.Fatalf("requests token=%d api=%d, want token=0 api=1", tokenRequests, apiRequests)
	}
}

func TestClientDoRequest_ForbiddenInvalidAccessTokenRefreshesOnce(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	var tokenRequests int
	var apiRequests int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"fresh-token","expireIn":7200}`)
	})
	mux.HandleFunc("/resource", func(w http.ResponseWriter, r *http.Request) {
		apiRequests++
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("x-acs-dingtalk-access-token") == "stale-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"code":"InvalidAccessToken","message":"access token expired"}`)
			return
		}
		_, _ = io.WriteString(w, `{"value":"ok"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		accessToken:  "stale-token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   srv.Client(),
	}

	var result map[string]string
	if err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, &result); err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if result["value"] != "ok" || tokenRequests != 1 || apiRequests != 2 {
		t.Fatalf("result=%v requests token=%d api=%d, want ok/token=1/api=2", result, tokenRequests, apiRequests)
	}
}

func TestClientDoRequest_ForbiddenQPSRetries(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0")
		w.Header().Set("Content-Type", "application/json")
		if attempts == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"code":"Forbidden.AccessDenied.QpsLimitForAppkeyAndApi","message":"qps limited"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok": "yes"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	var result map[string]string
	if err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, &result); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if result["ok"] != "yes" {
		t.Fatalf("result = %v", result)
	}
}

// TestClientDoRequest_TooManyRequests tests 429 handling with Retry-After.
func TestClientDoRequest_TooManyRequests(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "0") // Use 0 for fast test
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"errcode":429,"errmsg":"rate limited"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts < 2 {
		t.Errorf("expected retry, got %d attempts", attempts)
	}
}

// TestClientDoRequest_ServerError tests that 5xx errors are retried.
func TestClientDoRequest_ServerError(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"errcode":500,"errmsg":"server error"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error after retries")
	}
	if attempts < 2 {
		t.Errorf("expected retry on 5xx, got %d attempts", attempts)
	}
}

func TestClientDoRequest_RetriesFirstServerErrorAfterRateLimit(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		switch attempts {
		case 1:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"code":"TooManyRequests","message":"rate limited"}`)
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"code":"InternalError","message":"temporary failure"}`)
		default:
			_, _ = io.WriteString(w, `{"value":"ok"}`)
		}
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	var result map[string]string
	if err := c.doRequest(context.Background(), http.MethodGet, "/resource", nil, nil, &result); err != nil {
		t.Fatalf("doRequest() error = %v", err)
	}
	if result["value"] != "ok" || attempts != 3 {
		t.Fatalf("result=%v attempts=%d, want ok/3", result, attempts)
	}
}

// TestClientDoRequest_BadRequest tests that 4xx errors (except 401/403) are not retried.
func TestClientDoRequest_BadRequest(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errcode":400,"errmsg":"bad request"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for 400")
	}
	if attempts != 1 {
		t.Errorf("expected no retry for 400, got %d attempts", attempts)
	}
}

func TestClientDoRequest_ErrorDoesNotIncludeResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"errcode":400,"errmsg":"bad request","secret":"document body must not leak"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "document body must not leak") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error leaked response body: %v", err)
	}
}

func TestClientDoRequest_RejectsOversizedResponseBody(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.CopyN(w, repeatingByteReader('x'), maxResponseBodyBytes+1)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, nil)
	if !errors.Is(err, errResponseBodyTooLarge) {
		t.Fatalf("doRequest() error = %v, want response size error", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (oversized responses are not retryable)", requests)
	}
}

func TestReadResponseBody_AllowsExactLimit(t *testing.T) {
	body, err := readResponseBody(io.LimitReader(repeatingByteReader('x'), maxResponseBodyBytes))
	if err != nil {
		t.Fatalf("readResponseBody() error = %v at exact limit", err)
	}
	if len(body) != maxResponseBodyBytes {
		t.Fatalf("len(body) = %d, want %d", len(body), maxResponseBodyBytes)
	}
}

func TestEnsureAccessToken_RejectsOversizedResponseBody(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.CopyN(w, repeatingByteReader('x'), maxResponseBodyBytes+1)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "app-key",
		clientSecret: "app-secret",
		httpClient:   srv.Client(),
	}
	err := c.ensureAccessToken(context.Background())
	if !errors.Is(err, errResponseBodyTooLarge) {
		t.Fatalf("ensureAccessToken() error = %v, want response size error", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

// TestClientDoRequest_Success tests successful request.
func TestClientDoRequest_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"test": "data"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	var result map[string]string
	err := c.doRequest(context.Background(), http.MethodGet, "/test", nil, nil, &result)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result["test"] != "data" {
		t.Errorf("result = %v, want {test: data}", result)
	}
}

// TestClientDoRequest_QueryParams tests that query parameters are properly encoded.
func TestClientDoRequest_QueryParams(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	params := map[string]string{
		"operatorId": "user-123",
		"maxResults": "30",
	}
	err := c.doRequest(context.Background(), http.MethodGet, "/test", params, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedQuery, "operatorId=user-123") {
		t.Errorf("expected operatorId in query, got %q", receivedQuery)
	}
}

func TestClientListWorkspacesPaginates(t *testing.T) {
	var seenTokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2.0/wiki/workspaces" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("operatorId"); got != "operator-1" {
			t.Errorf("operatorId = %q, want operator-1", got)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.URL.Query().Get("maxResults"); got != "30" {
			t.Errorf("maxResults = %q, want official workspace page size 30", got)
			http.Error(w, "unexpected maxResults", http.StatusBadRequest)
			return
		}

		nextToken := r.URL.Query().Get("nextToken")
		seenTokens = append(seenTokens, nextToken)
		w.Header().Set("Content-Type", "application/json")
		switch nextToken {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"workspaces": []WikiWorkspace{{WorkspaceID: "ws-1", Name: "First"}},
				"nextToken":  "page-2",
			})
		case "page-2":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"workspaces": []WikiWorkspace{{WorkspaceID: "ws-2", Name: "Second"}},
			})
		default:
			t.Errorf("unexpected nextToken %q", nextToken)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	workspaces, err := c.ListWorkspaces(context.Background(), "operator-1")
	if err != nil {
		t.Fatalf("ListWorkspaces() error = %v", err)
	}
	if len(workspaces) != 2 {
		t.Fatalf("len(workspaces) = %d, want 2: %+v", len(workspaces), workspaces)
	}
	if strings.Join(seenTokens, ",") != ",page-2" {
		t.Fatalf("seen nextToken sequence = %v, want [ page-2]", seenTokens)
	}
}

func TestClientListWorkspacesRejectsRepeatedNextToken(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workspaces":[],"nextToken":"same-token"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := c.ListWorkspaces(ctx, "operator-1")
	if err == nil || !strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("ListWorkspaces() error = %v, want repeated nextToken error", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestPaginationGuardRejectsExcessivePages(t *testing.T) {
	guard := newPaginationGuard("test pagination")
	guard.pages = maxPaginationPages - 1
	err := guard.record("another-page")
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeded %d pages", maxPaginationPages)) {
		t.Fatalf("record() error = %v, want page limit error", err)
	}
}

func TestClientListAllNodesRejectsExcessiveUniqueDepth(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"nodes":[{"nodeId":"child","nodeType":"FOLDER","hasChildren":true}]}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	_, err := c.listAllNodesFrom(
		context.Background(),
		"root",
		"operator-1",
		make(map[string]bool),
		newTraversalBudget("test traversal"),
		maxTraversalDepth-1,
	)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeded %d levels", maxTraversalDepth)) {
		t.Fatalf("listAllNodesFrom() error = %v, want traversal depth error", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 before child depth is rejected", requests)
	}
}

func TestTraversalBudgetAllowsMoreRequestsThanPaginationLimit(t *testing.T) {
	budget := newTraversalBudget("test traversal")
	budget.requests = maxPaginationPages
	if err := budget.record(0); err != nil {
		t.Fatalf("record() rejected request %d: %v", maxPaginationPages+1, err)
	}
}

func TestTraversalBudgetRejectsExcessiveRequests(t *testing.T) {
	budget := newTraversalBudget("test traversal")
	budget.requests = maxTraversalRequests
	err := budget.record(0)
	if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("exceeded %d requests", maxTraversalRequests)) {
		t.Fatalf("record() error = %v, want traversal request limit error", err)
	}
}

func TestClientPingIncludesOperatorID(t *testing.T) {
	var operatorID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2.0/wiki/workspaces" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		operatorID = r.URL.Query().Get("operatorId")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workspaces":[{"workspaceId":"workspace-1","name":"Test"}]}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	if err := c.Ping(context.Background(), "operator-123"); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if operatorID != "operator-123" {
		t.Fatalf("operatorId = %q, want operator-123", operatorID)
	}
}

func TestClientPingFollowsEmptyPageWithNextToken(t *testing.T) {
	var seenTokens []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextToken := r.URL.Query().Get("nextToken")
		seenTokens = append(seenTokens, nextToken)
		w.Header().Set("Content-Type", "application/json")
		switch nextToken {
		case "":
			_, _ = io.WriteString(w, `{"workspaces":[],"nextToken":"page-2"}`)
		case "page-2":
			_, _ = io.WriteString(w, `{"workspaces":[{"workspaceId":"workspace-1","name":"Test"}]}`)
		default:
			w.WriteHeader(http.StatusTeapot)
			_, _ = io.WriteString(w, `{"code":"unexpected-page"}`)
		}
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	if err := c.Ping(context.Background(), "operator-123"); err != nil {
		t.Fatalf("Ping() error = %v, want success after following nextToken", err)
	}
	if got, want := strings.Join(seenTokens, ","), ",page-2"; got != want {
		t.Fatalf("seen nextTokens = %q, want %q", got, want)
	}
}

func TestClientPingStopsAfterFirstAccessibleWorkspace(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		if requests > 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"code":"unexpected-page","message":"Ping should already have succeeded"}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"workspaces":[{"workspaceId":"workspace-1","name":"Test"}],
			"nextToken":"unused-page"
		}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	if err := c.Ping(context.Background(), "operator-123"); err != nil {
		t.Fatalf("Ping() error = %v, want success from first accessible workspace", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestClientPingAllowsNoAccessibleWorkspaces(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v2.0/wiki/workspaces" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"workspaces":[]}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	if err := c.Ping(context.Background(), "operator-123"); err != nil {
		t.Fatalf("Ping() error = %v, want authentication success with empty resources", err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestClientListNodePageUsesOfficialMaximumPageSize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("maxResults"); got != "50" {
			t.Errorf("maxResults = %q, want 50", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"nodes":[]}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	if _, _, err := c.listNodePage(context.Background(), "root-1", "operator-1", ""); err != nil {
		t.Fatalf("listNodePage() error = %v", err)
	}
}

func TestClientListAllNodesRejectsRepeatedNextToken(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"nodes":[],"nextToken":"same-token"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := c.ListAllNodes(ctx, "root-1", "operator-1")
	if err == nil || !strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("ListAllNodes() error = %v, want repeated nextToken error", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestListDirectNodesRejectsRepeatedNextToken(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"nodes":[],"nextToken":"same-token"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := listDirectNodes(ctx, c, "root-1", "operator-1")
	if err == nil || !strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("listDirectNodes() error = %v, want repeated nextToken error", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestCollectAncestorPathsRejectsRepeatedNextToken(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"nodes":[],"nextToken":"same-token"}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}
	connector := &Connector{}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := connector.collectAncestorPaths(
		ctx,
		c,
		"workspace-1",
		"root-1",
		"operator-1",
		map[string]bool{"target-1": true},
		[]string{"workspace-1"},
		make(map[string]bool),
	)
	if err == nil || !strings.Contains(err.Error(), "repeated nextToken") {
		t.Fatalf("collectAncestorPaths() error = %v, want repeated nextToken error", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}

func TestEnsureAccessToken_OfficialResponseAndSharedCache(t *testing.T) {
	allowLocalDingTalkHTTPForTest(t)
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	tokenRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/oauth2/accessToken" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		tokenRequests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"official-token","expireIn":7200}`)
	}))
	defer srv.Close()

	cfg := &Config{ClientID: "app-key", ClientSecret: "app-secret", BaseURL: srv.URL}
	first := newClient(cfg)
	if err := first.ensureAccessToken(context.Background()); err != nil {
		t.Fatalf("first ensureAccessToken() error = %v", err)
	}
	second := newClient(cfg)
	if err := second.ensureAccessToken(context.Background()); err != nil {
		t.Fatalf("second ensureAccessToken() error = %v", err)
	}
	if tokenRequests != 1 {
		t.Fatalf("tokenRequests = %d, want 1", tokenRequests)
	}
	if second.accessToken != "official-token" {
		t.Fatalf("second.accessToken = %q", second.accessToken)
	}
}

func TestEnsureAccessToken_RejectsTokenLifetimeInsideRefreshSkew(t *testing.T) {
	for _, expireIn := range []int{300, 0} {
		t.Run(fmt.Sprintf("expireIn_%d", expireIn), func(t *testing.T) {
			resetAccessTokenCacheForTest()
			t.Cleanup(resetAccessTokenCacheForTest)

			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				w.Header().Set("Content-Type", "application/json")
				if requests > 1 {
					w.WriteHeader(http.StatusTeapot)
					_, _ = io.WriteString(w, `{"code":"unexpected-second-refresh","message":"short token lifetime was not rejected"}`)
					return
				}
				_, _ = fmt.Fprintf(w, `{"accessToken":"short-lived-token","expireIn":%d}`, expireIn)
			}))
			defer srv.Close()

			c := &client{
				baseURL:      srv.URL,
				clientID:     "app-key",
				clientSecret: "app-secret",
				httpClient:   srv.Client(),
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err := c.ensureAccessToken(ctx)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "expir") {
				t.Fatalf("ensureAccessToken() error = %v, want explicit token expiry error", err)
			}
			if requests != 1 {
				t.Fatalf("token requests = %d, want 1", requests)
			}
		})
	}
}

func TestEnsureAccessToken_CoalescesConcurrentRefreshes(t *testing.T) {
	allowLocalDingTalkHTTPForTest(t)
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	tokenRequests := 0
	var mu sync.Mutex
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/oauth2/accessToken" {
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		mu.Lock()
		tokenRequests++
		mu.Unlock()
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"shared-token","expireIn":7200}`)
	}))
	defer srv.Close()
	var releaseOnce sync.Once
	releaseRefresh := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRefresh()

	cfg := &Config{ClientID: "app-key", ClientSecret: "app-secret", BaseURL: srv.URL}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	entered := make([]chan struct{}, 8)
	for i := 0; i < 8; i++ {
		entered[i] = make(chan struct{})
		wg.Add(1)
		go func(observed chan struct{}) {
			defer wg.Done()
			c := newClient(cfg)
			ctx := &doneObservedContext{Context: context.Background(), observed: observed}
			if err := c.ensureAccessToken(ctx); err != nil {
				errs <- err
				return
			}
			if c.accessToken != "shared-token" {
				errs <- fmt.Errorf("accessToken = %q, want shared-token", c.accessToken)
			}
		}(entered[i])
	}
	for i, observed := range entered {
		waitForSignal(t, observed, fmt.Sprintf("refresh waiter %d to enter select", i))
	}
	releaseRefresh()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	mu.Lock()
	gotRequests := tokenRequests
	mu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("tokenRequests = %d, want 1", gotRequests)
	}
}

func TestEnsureAccessToken_LeaderCancellationDoesNotCancelSharedRefresh(t *testing.T) {
	allowLocalDingTalkHTTPForTest(t)
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var requestMu sync.Mutex
	tokenRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		tokenRequests++
		requestMu.Unlock()
		once.Do(func() { close(started) })
		select {
		case <-release:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"accessToken":"shared-token","expireIn":7200}`)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	var releaseOnce sync.Once
	releaseRefresh := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRefresh()

	cfg := &Config{ClientID: "app-key", ClientSecret: "app-secret", BaseURL: srv.URL}
	leader := newClient(cfg)
	waiter := newClient(cfg)
	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()
	leaderErr := make(chan error, 1)
	waiterErr := make(chan error, 1)
	waiterObserved := make(chan struct{})

	go func() { leaderErr <- leader.ensureAccessToken(leaderCtx) }()
	waitForSignal(t, started, "leader token request")
	waiterCtx := &doneObservedContext{Context: context.Background(), observed: waiterObserved}
	go func() { waiterErr <- waiter.ensureAccessToken(waiterCtx) }()
	waitForSignal(t, waiterObserved, "shared refresh waiter to enter select")
	cancelLeader()

	if err := waitForError(t, leaderErr, "canceled refresh leader"); !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want context.Canceled", err)
	}
	releaseRefresh()
	if err := waitForError(t, waiterErr, "shared refresh waiter"); err != nil {
		t.Fatalf("waiter error = %v, want shared refresh success", err)
	}
	if waiter.accessTokenSnapshot() != "shared-token" {
		t.Fatalf("waiter token = %q, want shared-token", waiter.accessTokenSnapshot())
	}
	requestMu.Lock()
	gotRequests := tokenRequests
	requestMu.Unlock()
	if gotRequests != 1 {
		t.Fatalf("tokenRequests = %d, want 1 shared refresh", gotRequests)
	}
}

func TestEnsureAccessToken_WaiterCanCancelWhileSharedRefreshContinues(t *testing.T) {
	allowLocalDingTalkHTTPForTest(t)
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"shared-token","expireIn":7200}`)
	}))
	defer srv.Close()
	var releaseOnce sync.Once
	releaseRefresh := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseRefresh()

	cfg := &Config{ClientID: "app-key", ClientSecret: "app-secret", BaseURL: srv.URL}
	leader := newClient(cfg)
	waiter := newClient(cfg)
	leaderErr := make(chan error, 1)
	waiterErr := make(chan error, 1)

	go func() { leaderErr <- leader.ensureAccessToken(context.Background()) }()
	waitForSignal(t, started, "leader token request")
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	defer cancelWaiter()
	waiterObserved := make(chan struct{})
	observedCtx := &doneObservedContext{Context: waiterCtx, observed: waiterObserved}
	go func() { waiterErr <- waiter.ensureAccessToken(observedCtx) }()
	waitForSignal(t, waiterObserved, "cancelable refresh waiter to enter select")
	cancelWaiter()

	err := waitForError(t, waiterErr, "canceled refresh waiter")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter error = %v, want context.Canceled", err)
	}
	releaseRefresh()
	leaderResult := waitForError(t, leaderErr, "refresh leader")
	if leaderResult != nil {
		t.Fatalf("leader error = %v, want success", leaderResult)
	}
}

func TestStoreCachedAccessTokenPurgesExpiredEntries(t *testing.T) {
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	storeCachedAccessToken("expired", "expired-token", time.Now().Add(-time.Minute))
	storeCachedAccessToken("valid", "valid-token", time.Now().Add(time.Hour))

	accessTokenCache.Lock()
	_, expiredExists := accessTokenCache.entries["expired"]
	_, validExists := accessTokenCache.entries["valid"]
	accessTokenCache.Unlock()
	if expiredExists {
		t.Fatal("expired token cache entry was not purged")
	}
	if !validExists {
		t.Fatal("valid token cache entry was unexpectedly removed")
	}
}

// TestClientDoRequest_EmptyQueryValue tests that empty query values are skipped.
func TestClientDoRequest_EmptyQueryValue(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	params := map[string]string{
		"withValue": "yes",
		"empty":     "",
	}
	err := c.doRequest(context.Background(), http.MethodGet, "/test", params, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(receivedQuery, "empty=") {
		t.Errorf("empty query value should be skipped, got %q", receivedQuery)
	}
}

// TestClientDoRequest_WithBody tests POST request with JSON body.
func TestClientDoRequest_WithBody(t *testing.T) {
	var receivedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedBody = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		accessToken:  "token",
		tokenExpiry:  time.Now().Add(time.Hour),
		httpClient:   &http.Client{},
	}

	body := map[string]string{"key": "value"}
	err := c.doRequest(context.Background(), http.MethodPost, "/test", nil, body, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(receivedBody, "application/json") {
		t.Errorf("expected application/json content type, got %q", receivedBody)
	}
}

func TestClientDoRequest_RejectsUnencodableBody(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}
	body := map[string]interface{}{"unsupported": make(chan int)}
	err := c.doRequest(context.Background(), http.MethodPost, "/test", nil, body, nil)
	if err == nil || !strings.Contains(err.Error(), "encode request body") {
		t.Fatalf("doRequest() error = %v, want request body encoding error", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0 when body encoding fails", requests)
	}
}

// TestClientDoRequest_ContextCanceled tests that cancellation stops an in-flight request.
func TestClientDoRequest_ContextCanceled(t *testing.T) {
	started := make(chan struct{})
	handlerDone := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(handlerDone)
	}))
	defer srv.Close()

	c := &client{
		baseURL:     srv.URL,
		accessToken: "token",
		tokenExpiry: time.Now().Add(time.Hour),
		httpClient:  srv.Client(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.doRequest(ctx, http.MethodGet, "/test", nil, nil, nil)
	}()
	waitForSignal(t, started, "HTTP request to reach handler")
	cancel()

	err := waitForError(t, errCh, "canceled HTTP request")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	waitForSignal(t, handlerDone, "HTTP handler to observe cancellation")
}

// TestDingTalkAPIError_Error tests the error string format.
func TestDingTalkAPIError_ErrorFromClientTest(t *testing.T) {
	err := &dingtalkAPIError{Code: "123", Msg: "test message"}
	got := err.Error()
	expected := "dingtalk api error: code=123 msg=test message"
	if got != expected {
		t.Errorf("Error() = %q, want %q", got, expected)
	}
}

// TestClientNewClient tests client creation with config.
func TestClientNewClient(t *testing.T) {
	cfg := &Config{
		ClientID:     "test-id",
		ClientSecret: "test-secret",
	}
	c := newClient(cfg)

	if c.clientID != "test-id" {
		t.Errorf("clientID = %q, want %q", c.clientID, "test-id")
	}
	if c.clientSecret != "test-secret" {
		t.Errorf("clientSecret = %q, want %q", c.clientSecret, "test-secret")
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	if c.httpClient == nil {
		t.Error("httpClient should not be nil")
	}
}
