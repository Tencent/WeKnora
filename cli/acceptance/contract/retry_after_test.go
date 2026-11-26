package contract_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/Tencent/WeKnora/client"
)

// A 429 on a typed command must carry the same retry_after_seconds hint the
// raw `weknora api` passthrough already emits. The error contract says the
// field mirrors the response's Retry-After header; when a typed command drops
// it, an agent reading the envelope cannot tell how long to back off and
// either retries immediately or waits arbitrarily.
func TestRateLimitedTypedCommandCarriesRetryAfter(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/knowledge-bases") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "42")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"code":1006,"error":"too many requests"}`))
	}))
	defer ts.Close()

	f := newTestFactory(t, ts, sdk.NewClient(ts.URL))
	_, stderr, exit := runCmd(t, f, "kb", "list", "--format", "json")

	if exit != 6 {
		t.Errorf("exit = %d, want 6 (rate limited); stderr=%q", exit, stderr)
	}
	if !strings.Contains(stderr, `"server.rate_limited"`) {
		t.Errorf("stderr missing server.rate_limited; got %q", stderr)
	}
	if !strings.Contains(stderr, `"retry_after_seconds":42`) {
		t.Errorf("stderr missing retry_after_seconds:42; got %q", stderr)
	}
}
