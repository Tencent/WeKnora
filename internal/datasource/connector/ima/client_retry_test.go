package ima

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func TestCallAPI_RetriesRateLimitedForbidden(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"code":200001,"msg":"请求频率超限，请稍后重试"}`)
			return
		}
		_, _ = fmt.Fprint(w, `{"code":0,"data":{"knowledge_list":[],"is_end":true}}`)
	}))
	defer server.Close()

	c := newClient(&Config{ClientID: "cid", APIKey: "cak", BaseURL: server.URL})
	got, err := c.GetKnowledgeList(t.Context(), "kb", "", "", 10)
	if err != nil || !got.IsEnd {
		t.Fatalf("GetKnowledgeList() = %v, %v; want successful retry", got, err)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("server hit %d times, want 2", got)
	}
}

func TestCallAPI_ClassifiesRateLimitAndAuthErrors(t *testing.T) {
	for _, tt := range []struct {
		name        string
		status      int
		body        string
		retryAfter  string
		wantAuthErr bool
		wantHits    int32
	}{
		{"exhausted retries", http.StatusForbidden, `{"retcode":200001,"errmsg":"请求频率超限"}`, "0", false, 4},
		{"retry-after beyond budget", http.StatusForbidden, `{"code":200001,"msg":"请求频率超限"}`, "86400", false, 1},
		{"business rate limit", http.StatusOK, `{"code":200001,"msg":"请求频率超限"}`, "0", false, 4},
		{"real credentials failure", http.StatusForbidden, `{"code":110030,"msg":"无权限"}`, "", true, 1},
		{"unstructured forbidden", http.StatusForbidden, "Forbidden", "", true, 1},
		{"unauthorized", http.StatusUnauthorized, `{"code":200001}`, "", true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				hits.Add(1)
				w.Header().Set("Retry-After", tt.retryAfter)
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			c := newClient(&Config{ClientID: "cid", APIKey: "cak", BaseURL: server.URL})
			_, err := c.GetMediaInfo(t.Context(), "media")
			if err == nil || errors.Is(err, datasource.ErrInvalidCredentials) != tt.wantAuthErr {
				t.Fatalf("GetMediaInfo() error = %v, want authentication error: %t", err, tt.wantAuthErr)
			}
			message := err.Error()
			if !tt.wantAuthErr && (!strings.Contains(message, "rate limited") || !strings.Contains(message, "200001")) {
				t.Fatalf("GetMediaInfo() error = %v, want rate-limit status and business code", err)
			}
			if got := hits.Load(); got != tt.wantHits {
				t.Fatalf("server hit %d times, want %d", got, tt.wantHits)
			}
		})
	}
}
