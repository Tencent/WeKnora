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
	"golang.org/x/sync/singleflight"
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

func TestTruncate_Empty(t *testing.T) {
	got := truncate("", 100)
	if got != "" {
		t.Errorf("truncate(\"\", 100) = %q, want empty string", got)
	}
}

func TestTruncate_ExactlyMaxLen(t *testing.T) {
	got := truncate("12345", 5)
	if got != "12345" {
		t.Errorf("truncate(\"12345\", 5) = %q, want \"12345\"", got)
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
	accessTokenRefreshGroup = singleflight.Group{}
}

func allowLocalDingTalkHTTPForTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST_EXTRA", "127.0.0.1,localhost,::1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
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
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("operatorId"); got != "operator-1" {
			t.Fatalf("operatorId = %q, want operator-1", got)
		}
		if got := r.URL.Query().Get("maxResults"); got == "" {
			t.Fatal("expected maxResults on every workspace list request")
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
			t.Fatalf("unexpected nextToken %q", nextToken)
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

func TestClientPingIncludesOperatorID(t *testing.T) {
	var operatorID string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2.0/wiki/workspaces" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		operatorID = r.URL.Query().Get("operatorId")
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
		t.Fatalf("Ping() error = %v", err)
	}
	if operatorID != "operator-123" {
		t.Fatalf("operatorId = %q, want operator-123", operatorID)
	}
}

func TestEnsureAccessToken_OfficialResponseAndSharedCache(t *testing.T) {
	allowLocalDingTalkHTTPForTest(t)
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	tokenRequests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/oauth2/accessToken" {
			t.Fatalf("unexpected path %s", r.URL.Path)
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

func TestEnsureAccessToken_CoalescesConcurrentRefreshes(t *testing.T) {
	allowLocalDingTalkHTTPForTest(t)
	resetAccessTokenCacheForTest()
	t.Cleanup(resetAccessTokenCacheForTest)

	tokenRequests := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0/oauth2/accessToken" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		mu.Lock()
		tokenRequests++
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"accessToken":"shared-token","expireIn":7200}`)
	}))
	defer srv.Close()

	cfg := &Config{ClientID: "app-key", ClientSecret: "app-secret", BaseURL: srv.URL}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := newClient(cfg)
			if err := c.ensureAccessToken(context.Background()); err != nil {
				errs <- err
				return
			}
			if c.accessToken != "shared-token" {
				errs <- fmt.Errorf("accessToken = %q, want shared-token", c.accessToken)
			}
		}()
	}
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

// TestClientDoRequest_ContextCanceled tests that context cancellation stops requests.
func TestClientDoRequest_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second) // Simulate slow response
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := &client{
		baseURL:      srv.URL,
		clientID:     "test",
		clientSecret: "secret",
		httpClient:   &http.Client{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before request

	err := c.doRequest(ctx, http.MethodGet, "/test", nil, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
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
