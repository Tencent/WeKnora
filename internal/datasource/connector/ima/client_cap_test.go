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
