package im

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// The cap is the only thing standing between a public callback endpoint and an
// arbitrarily large allocation, so pin that it bounds the read rather than
// relying on the caller's body size.
func TestLimitWebhookBodyBoundsTheRead(t *testing.T) {
	const payloadSize = 32 << 20

	src := &repeatReader{remaining: payloadSize}
	body := &countingBody{src: src}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/1", nil)
	c.Request.Body = body

	LimitWebhookBody(c)
	if _, err := io.ReadAll(c.Request.Body); err == nil {
		t.Fatal("expected the cap to fire on an oversized callback body")
	} else {
		var tooLarge *http.MaxBytesError
		if !errors.As(err, &tooLarge) {
			t.Fatalf("err = %v, want an *http.MaxBytesError", err)
		}
	}

	if body.n >= payloadSize {
		t.Errorf("read %d bytes of a %d byte body; the cap never fired", body.n, payloadSize)
	}
	if body.n > maxWebhookBodyBytes+1 {
		t.Errorf("read %d bytes, want at most the %d byte cap plus the probe read",
			body.n, maxWebhookBodyBytes)
	}
}

// A body inside the cap reads through untouched.
func TestLimitWebhookBodyAllowsNormalBody(t *testing.T) {
	payload := []byte(`{"event":"message","text":"hello"}`)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/1",
		bytes.NewReader(payload))

	LimitWebhookBody(c)
	got, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil for a %d byte body", err, len(payload))
	}
	if string(got) != string(payload) {
		t.Errorf("body = %q, want %q", got, payload)
	}
}

// Limiting twice must not corrupt the body: adapters that buffer the bytes
// replace c.Request.Body with an in-memory copy, and the next cap wraps that.
func TestLimitWebhookBodyIsIdempotentAcrossReads(t *testing.T) {
	payload := []byte(`{"event":"message","text":"hello"}`)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/im/callback/1",
		bytes.NewReader(payload))

	LimitWebhookBody(c)
	first, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("first ReadAll() = %v", err)
	}

	c.Request.Body = io.NopCloser(bytes.NewReader(first))
	LimitWebhookBody(c)
	second, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("second ReadAll() = %v", err)
	}

	if string(second) != string(payload) {
		t.Errorf("body after re-cap = %q, want %q", second, payload)
	}
}

// A request with no body must not panic.
func TestLimitWebhookBodyHandlesNilBody(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/im/callback/1", nil)
	c.Request.Body = nil

	LimitWebhookBody(c)

	if c.Request.Body != nil {
		t.Error("LimitWebhookBody replaced a nil body")
	}
}

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

type repeatReader struct {
	remaining int64
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
