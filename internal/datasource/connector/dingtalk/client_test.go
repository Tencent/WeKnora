package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// newTestClient parses production-shaped credentials, then explicitly injects
// a private endpoint into this package-private test client. Production config
// never accepts an endpoint override.
func newTestClient(t *testing.T, serverURL string, settings map[string]interface{}) *client {
	t.Helper()
	cfg := testConfig(map[string]interface{}{
		"app_key":    "test-app-key-value",
		"app_secret": "s3cret-value-should-never-leak",
	})
	cfg.Settings = settings
	parsed, err := parseConfig(cfg)
	if err != nil {
		t.Fatalf("parseConfig: %v", err)
	}
	parsed.BaseURL = serverURL
	httpClient := &http.Client{
		Timeout: defaultTimeout,
	}
	applyDingTalkRedirectPolicy(httpClient)
	return &client{
		cfg:        parsed,
		httpClient: httpClient,
	}
}

// tokenHandler responds to the accessToken endpoint, counting calls.
func tokenHandler(calls *int64, token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(calls, 1)
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["appKey"] == "" || body["appSecret"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"code":"InvalidParameter","message":"missing appKey"}`)
			return
		}
		fmt.Fprintf(w, `{"accessToken":%q,"expireIn":7200}`, token)
	}
}

func TestGetTokenRequestShapeAndCaching(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	var gotPath, gotContentType string
	var gotBody map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&tokenCalls, 1)
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		fmt.Fprint(w, `{"accessToken":"tok-1","expireIn":7200}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		tok, err := cli.getToken(ctx)
		if err != nil {
			t.Fatalf("getToken: %v", err)
		}
		if tok != "tok-1" {
			t.Fatalf("token = %q", tok)
		}
	}
	if got := atomic.LoadInt64(&tokenCalls); got != 1 {
		t.Fatalf("token endpoint hit %d times, want 1 (cache)", got)
	}
	if gotPath != "/v1.0/oauth2/accessToken" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("content type = %q", gotContentType)
	}
	if gotBody["appKey"] == "" || gotBody["appSecret"] == "" {
		t.Fatalf("request body must carry appKey/appSecret, got %v", gotBody)
	}
}

// D3: concurrent callers sharing one cache entry must coalesce into a single
// token request rather than stampeding DingTalk's per-app quota.
func TestGetTokenConcurrentRefreshCoalesces(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&tokenCalls, 1)
		time.Sleep(50 * time.Millisecond) // widen the race window
		fmt.Fprint(w, `{"accessToken":"tok-conc","expireIn":7200}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := cli.getToken(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent getToken: %v", err)
	}
	if got := atomic.LoadInt64(&tokenCalls); got != 1 {
		t.Fatalf("token endpoint hit %d times under concurrency, want 1", got)
	}
}

func TestGetTokenWaiterHonorsContextCancellation(t *testing.T) {
	resetTokenCacheForTest()
	started := make(chan struct{})
	release := make(chan struct{})
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&tokenCalls, 1)
		select {
		case <-started:
		default:
			close(started)
		}
		<-release
		fmt.Fprint(w, `{"accessToken":"tok-cancel-waiter","expireIn":7200}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cli := newTestClient(t, srv.URL, nil)

	firstDone := make(chan error, 1)
	go func() {
		_, err := cli.getToken(context.Background())
		firstDone <- err
	}()
	<-started
	time.AfterFunc(500*time.Millisecond, func() {
		close(release)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := cli.getToken(ctx)
	elapsed := time.Since(start)
	if firstErr := <-firstDone; firstErr != nil {
		t.Fatalf("first token request: %v", firstErr)
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting token request error = %v, want context deadline", err)
	}
	if elapsed > 300*time.Millisecond {
		t.Fatalf("waiting token request ignored cancellation for %v", elapsed)
	}
	if got := atomic.LoadInt64(&tokenCalls); got != 1 {
		t.Fatalf("token endpoint hit %d times, want one coalesced request", got)
	}
}

func TestGetTokenRejectsIncompleteSuccessResponse(t *testing.T) {
	for _, response := range []string{
		`{"accessToken":"","expireIn":7200}`,
		`{"accessToken":"token-without-lifetime","expireIn":0}`,
	} {
		t.Run(response, func(t *testing.T) {
			resetTokenCacheForTest()
			mux := http.NewServeMux()
			mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, response)
			})
			srv := httptest.NewServer(mux)
			defer srv.Close()

			if token, err := newTestClient(t, srv.URL, nil).getToken(context.Background()); err == nil {
				t.Fatalf("incomplete token response returned token %q without an error", token)
			}
		})
	}
}

func TestGetTokenClassifiesRejectedCredentialsWithoutLeakingThem(t *testing.T) {
	resetTokenCacheForTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"code":"InvalidParameter","message":"test-app-key-value rejected s3cret-value-should-never-leak"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, nil).getToken(context.Background())
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("getToken error = %v, want ErrInvalidCredentials", err)
	}
	for _, private := range []string{"test-app-key-value", "s3cret-value-should-never-leak", srv.URL} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("credential rejection leaked %q in %v", private, err)
		}
	}
}

func TestGetTokenFailureDoesNotRetainCacheEntry(t *testing.T) {
	resetTokenCacheForTest()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"InvalidAuthentication","message":"rejected"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, nil).getToken(context.Background())
	if !errors.Is(err, datasource.ErrInvalidCredentials) {
		t.Fatalf("getToken error = %v, want ErrInvalidCredentials", err)
	}
	tokenCacheMu.Lock()
	cacheSize := len(tokenCache)
	tokenCacheMu.Unlock()
	if cacheSize != 0 {
		t.Fatalf("failed token request retained %d cache entries, want 0", cacheSize)
	}
}

func TestTokenCacheRemainsBoundedAcrossCredentialRotations(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "rotated-token"))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var firstKey string
	for i := 0; i < maxTokenCacheEntries+32; i++ {
		cli := newTestClient(t, srv.URL, map[string]interface{}{
			"tenant_id":      "tenant-rotation",
			"data_source_id": "ds-rotation",
		})
		cli.cfg.AppSecret = fmt.Sprintf("rotated-secret-%03d", i)
		if i == 0 {
			firstKey = cli.cfg.tokenCacheKey()
		}
		if _, err := cli.getToken(context.Background()); err != nil {
			t.Fatalf("rotation %d: %v", i, err)
		}
	}

	tokenCacheMu.Lock()
	cacheSize := len(tokenCache)
	_, firstStillCached := tokenCache[firstKey]
	tokenCacheMu.Unlock()
	if cacheSize != maxTokenCacheEntries {
		t.Fatalf("token cache size = %d, want hard cap %d", cacheSize, maxTokenCacheEntries)
	}
	if firstStillCached {
		t.Fatal("oldest credential identity was not evicted after cache reached its cap")
	}
	if got := atomic.LoadInt64(&tokenCalls); got != int64(maxTokenCacheEntries+32) {
		t.Fatalf("token endpoint calls = %d, want one per rotated credential", got)
	}
}

func TestGetTokenRetriesTransientServerFailure(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt64(&tokenCalls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"code":"ServiceUnavailable","message":"retry"}`)
			return
		}
		fmt.Fprint(w, `{"accessToken":"tok-after-retry","expireIn":7200}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token, err := newTestClient(t, srv.URL, nil).getToken(context.Background())
	if err != nil {
		t.Fatalf("getToken after transient failure: %v", err)
	}
	if token != "tok-after-retry" {
		t.Fatalf("token = %q, want tok-after-retry", token)
	}
	if got := atomic.LoadInt64(&tokenCalls); got != 2 {
		t.Fatalf("token endpoint calls = %d, want one retry", got)
	}
}

func TestCrossOriginRedirectNeverReceivesDingTalkCredentials(t *testing.T) {
	t.Run("oauth body", func(t *testing.T) {
		resetTokenCacheForTest()
		var attackerCalls int64
		attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&attackerCalls, 1)
			_, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"accessToken":"stolen","expireIn":7200}`)
		}))
		defer attacker.Close()

		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v1.0/oauth2/accessToken" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Location", attacker.URL+"/capture")
			w.WriteHeader(http.StatusTemporaryRedirect)
		}))
		defer origin.Close()

		if _, err := newTestClient(t, origin.URL, nil).getToken(context.Background()); err == nil {
			t.Fatal("cross-origin token redirect must be rejected")
		}
		if got := atomic.LoadInt64(&attackerCalls); got != 0 {
			t.Fatalf("redirect target received %d OAuth request(s), want 0", got)
		}
	})

	t.Run("access token header", func(t *testing.T) {
		resetTokenCacheForTest()
		var tokenCalls, attackerCalls int64
		attacker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt64(&attackerCalls, 1)
			if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "" {
				t.Errorf("redirect target received access token header %q", got)
			}
			fmt.Fprint(w, `{"workspaces":[],"hasMore":false}`)
		}))
		defer attacker.Close()

		mux := http.NewServeMux()
		mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-private"))
		mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", attacker.URL+"/capture")
			w.WriteHeader(http.StatusTemporaryRedirect)
		})
		origin := httptest.NewServer(mux)
		defer origin.Close()

		if _, err := newTestClient(t, origin.URL, nil).listWorkspaces(context.Background(), ""); err == nil {
			t.Fatal("cross-origin API redirect must be rejected")
		}
		if got := atomic.LoadInt64(&attackerCalls); got != 0 {
			t.Fatalf("redirect target received %d authorized request(s), want 0", got)
		}
	})
}

// D3: two tenants configuring the same DingTalk app must not share cache slots.
func TestGetTokenTenantIsolation(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&tokenCalls, 1)
		fmt.Fprintf(w, `{"accessToken":"tok-%d","expireIn":7200}`, n)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cliA := newTestClient(t, srv.URL, map[string]interface{}{"tenant_id": "tenant-a", "data_source_id": "ds-1"})
	cliB := newTestClient(t, srv.URL, map[string]interface{}{"tenant_id": "tenant-b", "data_source_id": "ds-1"})

	tokA, err := cliA.getToken(context.Background())
	if err != nil {
		t.Fatalf("tenant A: %v", err)
	}
	tokB, err := cliB.getToken(context.Background())
	if err != nil {
		t.Fatalf("tenant B: %v", err)
	}
	if tokA == tokB {
		t.Fatalf("tenants must not share tokens: both got %q", tokA)
	}
	if got := atomic.LoadInt64(&tokenCalls); got != 2 {
		t.Fatalf("token endpoint hit %d times, want 2 (one per tenant)", got)
	}
}

func TestAuthorizedGetSendsTokenHeaderAndOperator(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	var gotHeader, gotOperator string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-h"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-acs-dingtalk-access-token")
		gotOperator = r.URL.Query().Get("operatorId")
		fmt.Fprint(w, `{"workspaces":[],"hasMore":false}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	if _, err := cli.listWorkspaces(context.Background(), ""); err != nil {
		t.Fatalf("listWorkspaces: %v", err)
	}
	if gotHeader != "tok-h" {
		t.Fatalf("access token header = %q", gotHeader)
	}
	if gotOperator != "OPERATOR_UNION_ID" {
		t.Fatalf("operatorId = %q", gotOperator)
	}
}

func Test401InvalidatesTokenAndRetriesOnce(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls, wsCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt64(&tokenCalls, 1)
		fmt.Fprintf(w, `{"accessToken":"tok-%d","expireIn":7200}`, n)
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&wsCalls, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"code":"InvalidAuthentication","message":"token expired"}`)
			return
		}
		fmt.Fprint(w, `{"workspaces":[{"workspaceId":"ws1","name":"KB","rootNodeId":"root1"}],"hasMore":false}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	page, err := cli.listWorkspaces(context.Background(), "")
	if err != nil {
		t.Fatalf("listWorkspaces after 401: %v", err)
	}
	if len(page.Workspaces) != 1 || page.Workspaces[0].WorkspaceID != "ws1" {
		t.Fatalf("unexpected page %+v", page)
	}
	if atomic.LoadInt64(&tokenCalls) != 2 {
		t.Fatalf("token calls = %d, want 2 (server-side revocation must re-fetch)", tokenCalls)
	}
	// A second 401 in the same call must not loop forever.
	atomic.StoreInt64(&wsCalls, 0)
	mux2 := http.NewServeMux() // separate server that always 401s
	mux2.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-x"))
	mux2.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&wsCalls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"code":"InvalidAuthentication","message":"bad"}`)
	})
	srv2 := httptest.NewServer(mux2)
	defer srv2.Close()
	cli2 := newTestClient(t, srv2.URL, nil)
	_, err = cli2.listWorkspaces(context.Background(), "")
	if err == nil {
		t.Fatal("persistent 401 must fail")
	}
	if !errorsIsInvalidCredentials(err) {
		t.Fatalf("want credentials error, got %v", err)
	}
	if got := atomic.LoadInt64(&wsCalls); got != 2 {
		t.Fatalf("workspace endpoint hit %d times, want exactly 2 (one retry)", got)
	}
}

func Test401RefreshesTokenAfterEarlierTransientRetry(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls, workspaceCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt64(&tokenCalls, 1)
		fmt.Fprintf(w, `{"accessToken":"tok-%d","expireIn":7200}`, n)
	})
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, _ *http.Request) {
		switch atomic.AddInt64(&workspaceCalls, 1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, `{"code":"ServiceUnavailable","message":"retry"}`)
		case 2:
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"code":"InvalidAuthentication","message":"expired"}`)
		default:
			fmt.Fprint(w, `{"workspaces":[],"hasMore":false}`)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if _, err := newTestClient(t, srv.URL, nil).listWorkspaces(context.Background(), ""); err != nil {
		t.Fatalf("listWorkspaces after 5xx then 401: %v", err)
	}
	if got := atomic.LoadInt64(&tokenCalls); got != 2 {
		t.Fatalf("token calls = %d, want initial fetch plus one 401 refresh", got)
	}
	if got := atomic.LoadInt64(&workspaceCalls); got != 3 {
		t.Fatalf("workspace calls = %d, want transient retry, 401 retry, success", got)
	}
}

func errorsIsInvalidCredentials(err error) bool {
	return err != nil && (strings.Contains(err.Error(), datasource.ErrInvalidCredentials.Error()))
}

func Test429HonorsRetryAfterWithBound(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls, wsCalls int64
	start := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-r"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&wsCalls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"code":"Throttling.User","message":"too fast"}`)
			return
		}
		fmt.Fprint(w, `{"workspaces":[],"hasMore":false}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	if _, err := cli.listWorkspaces(context.Background(), ""); err != nil {
		t.Fatalf("listWorkspaces: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed < 900*time.Millisecond {
		t.Fatalf("Retry-After not honored: elapsed %v", elapsed)
	}
	if atomic.LoadInt64(&wsCalls) != 2 {
		t.Fatalf("workspace calls = %d, want 2", wsCalls)
	}
}

func TestRetryAfterIsCappedNotTrusted(t *testing.T) {
	if got := retryDelay(3600*time.Second, 1); got > maxRetryAfter {
		t.Fatalf("hostile Retry-After must be capped at %v, got %v", maxRetryAfter, got)
	}
	if got := retryDelay(0, 1); got <= 0 {
		t.Fatalf("zero Retry-After must still back off, got %v", got)
	}
}

func Test5xxRetriesAreBounded(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls, wsCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-5"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&wsCalls, 1)
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"code":"ServiceUnavailable","message":"upstream"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	_, err := cli.listWorkspaces(context.Background(), "")
	if err == nil {
		t.Fatal("persistent 502 must eventually fail")
	}
	if got := atomic.LoadInt64(&wsCalls); got != int64(maxAttempts) {
		t.Fatalf("workspace calls = %d, want %d (bounded retries)", got, maxAttempts)
	}
}

func Test403IsPermissionErrorNotRetried(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls, wsCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-f"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&wsCalls, 1)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"code":"Forbidden.AccessDenied","message":"no permission"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	_, err := cli.listWorkspaces(context.Background(), "")
	if err == nil {
		t.Fatal("403 must fail")
	}
	var apiErr *apiError
	if !asAPIError(err, &apiErr) || apiErr.StatusCode != http.StatusForbidden {
		t.Fatalf("want typed 403 apiError, got %v", err)
	}
	if got := atomic.LoadInt64(&wsCalls); got != 1 {
		t.Fatalf("403 must not be retried, endpoint hit %d times", got)
	}
}

// D4: secrets, tokens and the operator UnionID must never appear in errors,
// even when the failure is a transport error that embeds the request URL.
func TestErrorsNeverLeakSecretsTokenOrOperator(t *testing.T) {
	resetTokenCacheForTest()
	mux := http.NewServeMux()
	var tokenCalls int64
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-secret-value"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		// Force a mid-body failure so the client surfaces a transport error
		// whose wrapped *url.Error would normally carry the full query string.
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		panic("hijack unsupported")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	_, err := cli.listWorkspaces(context.Background(), "")
	if err == nil {
		t.Fatal("expected transport error")
	}
	msg := err.Error()
	for _, needle := range []string{
		"s3cret-value-should-never-leak", // app secret
		"tok-secret-value",               // access token
		"OPERATOR_UNION_ID",              // operator UnionID (personal data)
	} {
		if strings.Contains(msg, needle) {
			t.Fatalf("error leaks %q: %s", needle, msg)
		}
	}
}

func TestClientSanitizerRedactsRuntimeValuesAndFullURLs(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	cli := newTestClient(t, srv.URL, nil)
	cli.cfg.AppKey = "runtime-app-key-8675309"
	cli.cfg.AppSecret = "runtime-app-secret-8675309"
	cli.cfg.OperatorID = "runtime-operator-union-id-8675309"
	const token = "runtime-access-token-8675309"
	const signedURL = "https://media.example.test/file?signature=signed-secret&expires=999"

	err := fmt.Errorf(
		"request %s/v2.0/wiki/nodes?operatorId=%s failed: app=%s secret=%s token=%s media=%s",
		cli.cfg.BaseURL,
		cli.cfg.OperatorID,
		cli.cfg.AppKey,
		cli.cfg.AppSecret,
		token,
		signedURL,
	)
	msg := cli.sanitizeError(err, token).Error()

	for _, private := range []string{
		cli.cfg.BaseURL,
		cli.cfg.OperatorID,
		cli.cfg.AppKey,
		cli.cfg.AppSecret,
		token,
		signedURL,
		"signed-secret",
	} {
		if strings.Contains(msg, private) {
			t.Fatalf("sanitized error leaks %q: %s", private, msg)
		}
	}
	if !strings.Contains(msg, "[REDACTED") {
		t.Fatalf("sanitized error lacks a redaction marker: %s", msg)
	}
}

func TestAPIErrorRedactsPrivateQueryValues(t *testing.T) {
	resetTokenCacheForTest()
	const (
		workspaceID = "workspace-private-id-8675309"
		parentID    = "parent-private-id-8675309"
		nextToken   = "next-private-token-8675309"
	)
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-query"))
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(
			w,
			`{"code":"InvalidParameter","message":"workspaceId=%s parentNodeId=%s nextToken=%s"}`,
			workspaceID,
			parentID,
			nextToken,
		)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, nil).listChildren(
		context.Background(),
		workspaceID,
		parentID,
		nextToken,
	)
	if err == nil {
		t.Fatal("invalid private query must fail")
	}
	for _, private := range []string{workspaceID, parentID, nextToken} {
		if strings.Contains(err.Error(), private) {
			t.Fatalf("API error leaks query value %q: %v", private, err)
		}
	}
}

func TestAPIErrorRedactsPrivatePathIdentifier(t *testing.T) {
	resetTokenCacheForTest()
	const nodeID = "node-private-id-8675309"
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-path"))
	mux.HandleFunc("/v2.0/wiki/nodes/"+nodeID, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprintf(
			w,
			`{"code":"NotFound","message":"node %s does not exist"}`,
			nodeID,
		)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	_, err := newTestClient(t, srv.URL, nil).getNodeDetail(context.Background(), nodeID)
	if err == nil {
		t.Fatal("missing private node must fail")
	}
	if strings.Contains(err.Error(), nodeID) {
		t.Fatalf("API error leaks private path identifier %q: %v", nodeID, err)
	}
}

func TestResponseBodyOverCapFails(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-cap"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"workspaces":[{"name":"`))
		filler := strings.Repeat("A", 1<<20)
		for written := int64(0); written < maxResponseBytes+(1<<20); written += int64(len(filler)) {
			if _, err := w.Write([]byte(filler)); err != nil {
				return
			}
		}
		w.Write([]byte(`"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	_, err := cli.listWorkspaces(context.Background(), "")
	if err == nil {
		t.Fatal("oversized body must fail, not silently truncate")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("want explicit over-cap error, got %v", err)
	}
}

func TestPaginationStopsOnRepeatedTokenAndPageCap(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-p"))
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("workspaceId"); got != "" {
			t.Errorf("workspaceId is not part of the official ListNodes query, got %q", got)
		}
		if got := r.URL.Query().Get("parentNodeId"); got != "parent" {
			t.Errorf("parentNodeId = %q, want parent", got)
		}
		if got := r.URL.Query().Get("maxResults"); got != "50" {
			t.Errorf("maxResults = %q, want DingTalk node maximum 50", got)
		}
		// DingTalk exposes no hasMore flag. A repeated non-empty nextToken is a
		// loop unless the client detects it.
		fmt.Fprint(w, `{"nodes":[{"nodeId":"n1","type":"FOLDER","name":"a"}],"nextToken":"same"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	_, err := cli.listAllChildren(context.Background(), "ws1", "parent")
	if err == nil {
		t.Fatal("repeated nextToken must be detected as a pagination loop")
	}
	if !strings.Contains(err.Error(), "pagination") {
		t.Fatalf("want pagination loop error, got %v", err)
	}
}

func TestPaginatedAPIsRejectOversizedPages(t *testing.T) {
	t.Run("workspaces", func(t *testing.T) {
		resetTokenCacheForTest()
		var tokenCalls int64
		mux := http.NewServeMux()
		mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-workspace-page"))
		mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(workspacesPage{
				Workspaces: make([]workspace, workspaceMaxResults+1),
			})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		_, err := newTestClient(t, srv.URL, nil).listAllWorkspaces(context.Background())
		if err == nil || !strings.Contains(err.Error(), "requested page size") {
			t.Fatalf("oversized workspace page error = %v", err)
		}
	})

	t.Run("nodes", func(t *testing.T) {
		resetTokenCacheForTest()
		var tokenCalls int64
		mux := http.NewServeMux()
		mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-node-page"))
		mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(nodesPage{
				Nodes: make([]node, nodeMaxResults+1),
			})
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		_, err := newTestClient(t, srv.URL, nil).listAllChildren(
			context.Background(),
			"ws1",
			"parent",
		)
		if err == nil || !strings.Contains(err.Error(), "requested page size") {
			t.Fatalf("oversized node page error = %v", err)
		}
	})

	t.Run("blocks", func(t *testing.T) {
		resetTokenCacheForTest()
		var tokenCalls int64
		mux := http.NewServeMux()
		mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-block-page"))
		mux.HandleFunc("/v1.0/doc/suites/documents/doc-key-1/blocks", func(w http.ResponseWriter, _ *http.Request) {
			response := blocksResponse{Success: true}
			response.Result.Data = make([]block, documentBlockPageSize+1)
			_ = json.NewEncoder(w).Encode(response)
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		_, err := newTestClient(t, srv.URL, nil).listDocumentBlocks(
			context.Background(),
			"doc-key-1",
		)
		if err == nil || !strings.Contains(err.Error(), "requested page size") {
			t.Fatalf("oversized block page error = %v", err)
		}
	})
}

func TestAppendBoundedPageEnforcesAggregateLimits(t *testing.T) {
	t.Run("items", func(t *testing.T) {
		aggregateBytes := 0
		_, err := appendBoundedPage(
			[]string{"one", "two"},
			[]string{"three"},
			2,
			2,
			&aggregateBytes,
			"test",
		)
		if err == nil || !strings.Contains(err.Error(), "item limit") {
			t.Fatalf("aggregate item limit error = %v", err)
		}
	})

	t.Run("bytes", func(t *testing.T) {
		aggregateBytes := maxAggregatePageBytes - 1
		_, err := appendBoundedPage(
			[]string(nil),
			[]string{"x"},
			1,
			1,
			&aggregateBytes,
			"test",
		)
		if err == nil || !strings.Contains(err.Error(), "aggregate size limit") {
			t.Fatalf("aggregate byte limit error = %v", err)
		}
	})
}

func TestNodePaginationHasHardPageLimit(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls, nodeCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-page-cap"))
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt64(&nodeCalls, 1)
		fmt.Fprintf(w, `{"nodes":[],"nextToken":"page-%d"}`, call)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli := newTestClient(t, srv.URL, nil)
	_, err := cli.listAllChildren(ctx, "ws1", "parent")
	if err == nil || !strings.Contains(err.Error(), "page limit") {
		t.Fatalf("unbounded node pagination error = %v, want page limit", err)
	}
	if got := atomic.LoadInt64(&nodeCalls); got != 1000 {
		t.Fatalf("node pagination calls = %d, want hard limit 1000", got)
	}
}

func TestListDocumentBlocksUsesOfficialEnvelope(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	var gotPaths []string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-b"))
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-key-1/blocks", func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.RequestURI())
		fmt.Fprint(w, `{"success":true,"result":{"data":[{"blockType":"paragraph"},{"blockType":"heading"}]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	blocks, err := cli.listDocumentBlocks(context.Background(), "doc-key-1")
	if err != nil {
		t.Fatalf("listDocumentBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(blocks))
	}
	if len(gotPaths) != 1 {
		t.Fatalf("expected one blocks request, got %v", gotPaths)
	}
	if !strings.Contains(gotPaths[0], "operatorId=") {
		t.Fatalf("blocks call must carry operatorId: %s", gotPaths[0])
	}
	if !strings.Contains(gotPaths[0], "startIndex=0") ||
		!strings.Contains(gotPaths[0], "endIndex=99") {
		t.Fatalf("blocks call must request the first inclusive page: %s", gotPaths[0])
	}
	if strings.Contains(gotPaths[0], "nextToken=") {
		t.Fatalf("blocks API has no nextToken pagination: %s", gotPaths[0])
	}
}

func TestListDocumentBlocksFollowsInclusiveIndexPagination(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	var ranges [][2]string
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-block-pages"))
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-key-1/blocks", func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("startIndex")
		end := r.URL.Query().Get("endIndex")
		ranges = append(ranges, [2]string{start, end})

		response := blocksResponse{Success: true}
		switch start {
		case "0":
			response.Result.Data = make([]block, documentBlockPageSize)
			for i := range response.Result.Data {
				response.Result.Data[i] = textBlock(fmt.Sprintf("block-%d", i))
			}
		case "100":
			response.Result.Data = []block{textBlock("tail-block")}
		default:
			t.Errorf("unexpected block page startIndex=%q", start)
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	blocks, err := cli.listDocumentBlocks(context.Background(), "doc-key-1")
	if err != nil {
		t.Fatalf("listDocumentBlocks: %v", err)
	}
	if len(blocks) != documentBlockPageSize+1 {
		t.Fatalf("blocks = %d, want %d", len(blocks), documentBlockPageSize+1)
	}
	if got := blocks[len(blocks)-1].Value["text"]; got != "tail-block" {
		t.Fatalf("tail block = %v, want tail-block", got)
	}
	wantRanges := [][2]string{{"0", "99"}, {"100", "199"}}
	if !reflect.DeepEqual(ranges, wantRanges) {
		t.Fatalf("requested ranges = %v, want %v", ranges, wantRanges)
	}
}

func TestListDocumentBlocksRejectsUnsuccessfulEnvelope(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-bad-envelope"))
	mux.HandleFunc("/v1.0/doc/suites/documents/doc-key-1/blocks", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":false,"result":{"data":[{"blockType":"paragraph"}]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	if _, err := cli.listDocumentBlocks(context.Background(), "doc-key-1"); err == nil {
		t.Fatal("success=false response must not be indexed as a valid document")
	}
}

func TestContextCancellationStopsRetries(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-c"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprint(w, `{"code":"Throttling.User","message":"slow down"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	cli := newTestClient(t, srv.URL, nil)
	start := time.Now()
	_, err := cli.listWorkspaces(ctx, "")
	if err == nil {
		t.Fatal("expected context deadline")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancellation must interrupt the Retry-After sleep")
	}
}

// The current DingTalk response has no hasMore flag: a non-empty nextToken is
// the only pagination signal.
func TestListWorkspacesFollowsPagination(t *testing.T) {
	resetTokenCacheForTest()
	var tokenCalls int64
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", tokenHandler(&tokenCalls, "tok-w"))
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("maxResults"); got != "30" {
			t.Errorf("maxResults = %q, want DingTalk workspace maximum 30", got)
		}
		if r.URL.Query().Get("nextToken") == "" {
			fmt.Fprint(w, `{"workspaces":[{"workspaceId":"ws1","name":"One"}],"nextToken":"t2"}`)
			return
		}
		fmt.Fprint(w, `{"workspaces":[{"workspaceId":"ws2","name":"Two"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cli := newTestClient(t, srv.URL, nil)
	all, err := cli.listAllWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("listAllWorkspaces: %v", err)
	}
	if len(all) != 2 || all[0].WorkspaceID != "ws1" || all[1].WorkspaceID != "ws2" {
		t.Fatalf("workspaces = %+v", all)
	}
}
