package core

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFeishuRejectedCachedTokenRecovers(t *testing.T) {
	for _, mode := range []string{"json", "download"} {
		for _, status := range []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusOK} {
			if mode == "download" && status == http.StatusOK {
				continue // An HTTP 200 JSON file is content, not an authentication response.
			}
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				resetFeishuRequestGates(t)
				t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "60000")
				var auths, requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
						auths.Add(1)
						writeJSON(w, TokenResponse{TenantAccessToken: "fresh", Expire: 7200})
						return
					}
					requests.Add(1)
					if mode == "json" {
						var body map[string]string
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["key"] != "value" {
							t.Error("request body was not preserved across authentication retry")
						}
					}
					if r.Header.Get("Authorization") == "Bearer stale" {
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"code":99991663,"msg":"Invalid access token"}`))
						return
					}
					if r.Header.Get("Authorization") != "Bearer fresh" {
						t.Errorf("unexpected authorization header")
					}
					_, _ = w.Write([]byte(`{"code":0,"data":"complete"}`))
				}))
				defer server.Close()
				c := NewClient(&Config{AppID: t.Name(), BaseURL: server.URL})
				c.tokenCache, c.tokenExpAt = "stale", time.Now().Add(time.Hour)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if mode == "download" {
					got, err := c.downloadRawBytes(ctx, "/open-apis/drive/v1/files/file/download")
					if err != nil || string(got) != `{"code":0,"data":"complete"}` {
						t.Fatalf("download = %s, %v", got, err)
					}
				} else {
					var got struct {
						Code int    `json:"code"`
						Data string `json:"data"`
					}
					err := c.DoRequest(ctx, http.MethodPost, "/open-apis/wiki/v2/spaces",
						map[string]string{"key": "value"}, &got)
					if err != nil {
						t.Fatal(err)
					}
					if got.Code != 0 || got.Data != "complete" {
						t.Fatalf("response = %+v", got)
					}
				}
				if auths.Load() != 1 || requests.Load() != 2 {
					t.Fatalf("auths/requests = %d/%d, want 1/2", auths.Load(), requests.Load())
				}
			})
		}
	}
}

func TestFeishuTokenRejectionIsBoundedAndPermissionErrorsDoNotRefresh(t *testing.T) {
	for _, mode := range []string{"json", "download"} {
		statuses := []int{http.StatusUnauthorized, http.StatusBadRequest, http.StatusForbidden, http.StatusOK}
		for _, status := range statuses {
			if mode == "download" && status == http.StatusOK {
				continue
			}
			t.Run(fmt.Sprintf("%s/%d", mode, status), func(t *testing.T) {
				resetFeishuRequestGates(t)
				t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "60000")
				var auths, requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
						auths.Add(1)
						writeJSON(w, TokenResponse{TenantAccessToken: "fresh", Expire: 7200})
						return
					}
					requests.Add(1)
					w.WriteHeader(status)
					code := 99991663
					if status == http.StatusForbidden {
						code = 99991672
					}
					_, _ = fmt.Fprintf(w, `{"code":%d,"msg":"not authorized"}`, code)
				}))
				defer server.Close()
				c := NewClient(&Config{AppID: t.Name(), BaseURL: server.URL})
				c.tokenCache, c.tokenExpAt = "stale", time.Now().Add(time.Hour)
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var err error
				if mode == "download" {
					_, err = c.downloadRawBytes(ctx, "/open-apis/drive/v1/files/file/download")
				} else {
					err = c.DoRequest(ctx, http.MethodGet, "/open-apis/wiki/v2/spaces", nil, nil)
				}
				if err == nil {
					t.Fatal("expected rejection")
				}
				wantAuths, wantRequests := int32(1), int32(2)
				if status == http.StatusForbidden {
					wantAuths, wantRequests = 0, 1
					if c.tokenCache != "stale" {
						t.Error("permission denial invalidated a valid cached token")
					}
				}
				if auths.Load() != wantAuths || requests.Load() != wantRequests {
					t.Fatalf("auths/requests = %d/%d, want %d/%d",
						auths.Load(), requests.Load(), wantAuths, wantRequests)
				}
			})
		}
	}
}

func TestFeishuDownloadDoesNotInterpretSuccessfulJSONFileAsAuthError(t *testing.T) {
	resetFeishuRequestGates(t)
	const content = `{"code":99991663,"msg":"file contents"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(content))
	}))
	defer server.Close()
	c := NewClient(&Config{AppID: t.Name(), BaseURL: server.URL})
	c.tokenCache, c.tokenExpAt = "valid", time.Now().Add(time.Hour)
	got, err := c.downloadRawBytes(context.Background(), "/open-apis/drive/v1/files/file/download")
	if err != nil || string(got) != content || c.tokenCache != "valid" {
		t.Fatalf("download = %s, %v; token changed = %v", got, err, c.tokenCache != "valid")
	}
}

func TestFeishuLateTokenRejectionPreservesConcurrentRefresh(t *testing.T) {
	resetFeishuRequestGates(t)
	t.Setenv("FEISHU_API_REQUESTS_PER_MINUTE", "60000")
	var auths, staleCalls atomic.Int32
	bothStale := make(chan struct{})
	freshUsed := make(chan struct{})
	var freshOnce sync.Once
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal" {
			auths.Add(1)
			writeJSON(w, TokenResponse{TenantAccessToken: "fresh", Expire: 7200})
			return
		}
		if r.Header.Get("Authorization") == "Bearer stale" {
			if staleCalls.Add(1) == 1 {
				select {
				case <-bothStale:
				case <-ctx.Done():
					return
				}
			} else {
				close(bothStale)
				select {
				case <-freshUsed:
				case <-ctx.Done():
					return
				}
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":99991663}`))
			return
		}
		freshOnce.Do(func() { close(freshUsed) })
		_ = json.NewEncoder(w).Encode(map[string]int{"code": 0})
	}))
	defer server.Close()
	c := NewClient(&Config{AppID: t.Name(), BaseURL: server.URL})
	c.tokenCache, c.tokenExpAt = "stale", time.Now().Add(time.Hour)
	errs := make(chan error, 2)
	for range 2 {
		go func() { errs <- c.DoRequest(ctx, http.MethodGet, "/open-apis/wiki/v2/spaces", nil, nil) }()
	}
	for range 2 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
	if auths.Load() != 1 || staleCalls.Load() != 2 {
		t.Fatalf("refreshes/stale calls = %d/%d, want 1/2", auths.Load(), staleCalls.Load())
	}
}
