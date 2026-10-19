package mattermost

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// countingBody reports how many bytes a handler actually pulled off the wire,
// so a test can tell "rejected the oversized callback" apart from "buffered all
// of it and then failed for some other reason".
type countingBody struct {
	src io.Reader
	n   int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.src.Read(p)
	b.n += int64(n)
	return n, err
}

func (b *countingBody) Close() error { return nil }

// repeatReader yields size bytes of filler without allocating size bytes.
type repeatReader struct {
	remaining int64
}

// The IM callback endpoint is public and the Mattermost outgoing-webhook token
// is compared against the body, so the body has to be read before the request is
// authenticated. Without a cap, one unauthenticated POST decides how much memory
// the process allocates.
func TestVerifyCallbackRejectsOversizedBody(t *testing.T) {
	const payloadSize = 64 << 20

	body := &countingBody{src: &repeatReader{remaining: payloadSize}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/1", nil)
	c.Request.Body = body
	c.Request.Header.Set("Content-Type", "application/json")

	a := NewAdapter(nil, "expected-token", "bot-user-id", false)
	err := a.VerifyCallback(c)
	if err == nil {
		t.Fatal("expected an oversized callback to be rejected")
	}

	var tooLarge *http.MaxBytesError
	if !errors.As(err, &tooLarge) {
		t.Fatalf("err = %v, want an *http.MaxBytesError: the body was not capped", err)
	}
	if body.n >= payloadSize {
		t.Errorf("handler read %d bytes of a %d byte body; the cap never fired",
			body.n, payloadSize)
	}
}

// A callback well inside the cap still verifies, so the bound rejects only what
// it is meant to.
func TestVerifyCallbackAcceptsNormalBody(t *testing.T) {
	payload := `{"token":"expected-token","channel_id":"c1","user_id":"u1","text":"hi"}`

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/1",
		strings.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")

	a := NewAdapter(nil, "expected-token", "bot-user-id", false)
	if err := a.VerifyCallback(c); err != nil {
		t.Fatalf("VerifyCallback() = %v, want nil for a %d byte body", err, len(payload))
	}
}

func (r *repeatReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = 'a'
	}
	r.remaining -= int64(len(p))
	return len(p), nil
}
