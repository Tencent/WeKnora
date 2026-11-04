package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// closeCountingBody records every Close so a test can assert the SDK closed the
// response body. net/http requires the client to close it; a body left open
// keeps its connection checked out of the transport rather than idle in the
// pool, and whether the transport can reclaim it anyway depends on whether the
// decoder happened to observe EOF - which is why this asserts the contract
// rather than a connection count.
type closeCountingBody struct {
	io.ReadCloser
	closes *atomic.Int32
}

func (b *closeCountingBody) Close() error {
	b.closes.Add(1)
	return b.ReadCloser.Close()
}

type closeCountingTransport struct {
	base   http.RoundTripper
	closes atomic.Int32
}

func (t *closeCountingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &closeCountingBody{ReadCloser: resp.Body, closes: &t.closes}
	return resp, nil
}

// CreateKnowledgeFromURL is the only method in this package that reads
// resp.Body outside parseResponse, which is where the deferred Close lives. Its
// 409 branch returns ErrDuplicateURL straight after decoding, so without an
// explicit Close the body is never closed at all. That is the path a caller
// walking an already-ingested URL list hits on every entry.
func TestCreateKnowledgeFromURLClosesResponseBody(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{name: "conflict, URL already ingested", status: http.StatusConflict},
		{name: "success", status: http.StatusOK},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"success":false,"code":"duplicate",`+
					`"message":"URL already exists","data":{"id":"kn_1","knowledge_base_id":"kb_1"}}`)
			}))
			defer srv.Close()

			rt := &closeCountingTransport{base: http.DefaultTransport}
			c := NewClient(srv.URL, WithTransport(rt))

			if _, err := c.CreateKnowledgeFromURL(context.Background(), "kb_1",
				CreateKnowledgeFromURLRequest{URL: "https://example.com/already-ingested"},
			); err != nil && err != ErrDuplicateURL {
				t.Fatalf("unexpected error: %v", err)
			}

			if got := rt.closes.Load(); got == 0 {
				t.Errorf("response body was never closed; net/http requires the client to " +
					"close it, and an unclosed body holds its connection outside the idle pool")
			}
		})
	}
}
