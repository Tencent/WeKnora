package ima

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCallAPI_RejectsOversizedResponse(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("a"), 1<<20+1))
	}))
	defer server.Close()

	c := newClient(&Config{ClientID: "cid", APIKey: "cak", BaseURL: server.URL})
	c.jsonLimit = 1 << 20
	err := c.callAPI(context.Background(), "get_knowledge_list", map[string]any{}, &map[string]any{})
	if err == nil {
		t.Fatal("callAPI accepted a response larger than the cap, want error")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("error = %v, want an explicit over-limit error", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hit %d times, want 1 (oversized responses must not be retried)", got)
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5198
// IMA reports throttling as HTTP 403 with business code 200001; that must
// retry with backoff like every other rate-limit path, not surface as a
// permanent credential failure.
func TestCallAPI_Retries403RateLimitCode200001(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("{\"code\":200001,\"msg\":\"throttled\",\"data\":{}}"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"code\":0,\"msg\":\"ok\",\"data\":{}}"))
	}))
	defer server.Close()

	c := newClient(&Config{ClientID: "cid", APIKey: "cak", BaseURL: server.URL})
	err := c.callAPI(context.Background(), "get_knowledge_list", map[string]any{}, &map[string]any{})
	if err != nil {
		t.Fatalf("throttled-then-ok sequence should succeed after retries: %v", err)
	}
	if got := hits.Load(); got < 3 {
		t.Fatalf("server hit %d times, want >= 3 (403/200001 must be retried)", got)
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5198
// A plain 403 without the rate-limit business code is still a credential
// failure: the retry carve-out must not mask real authorization problems.
func TestCallAPI_Plain403StillInvalidCredentials(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("{\"code\":110030,\"msg\":\"no permission\"}"))
	}))
	defer server.Close()

	c := newClient(&Config{ClientID: "cid", APIKey: "cak", BaseURL: server.URL})
	err := c.callAPI(context.Background(), "get_knowledge_list", map[string]any{}, &map[string]any{})
	if err == nil {
		t.Fatal("plain 403 must fail")
	}
	if !strings.Contains(err.Error(), "invalid credentials") {
		t.Fatalf("error = %v, want invalid credentials", err)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("server hit %d times, want 1 (auth failure must not be retried)", got)
	}
}
