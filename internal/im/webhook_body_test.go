package im

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
// replace c.Request.Body with an in-memory copy, so a later cap wraps that.
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

// ReadIMResponse caps an outbound IM provider response body. These tests verify
// that the cap is enforced: an oversized response is refused rather than
// buffered whole.
func TestReadIMResponse(t *testing.T) {
	t.Run("reads small body in full", func(t *testing.T) {
		body := strings.Repeat("hello", 100)
		data, err := ReadIMResponse(strings.NewReader(body))
		if err != nil {
			t.Fatalf("ReadIMResponse(small) = %v, want nil", err)
		}
		if string(data) != body {
			t.Errorf("ReadIMResponse returned %q, want %q", string(data), body)
		}
	})

	t.Run("rejects body over the cap", func(t *testing.T) {
		const limit = maxWebhookBodyBytes
		_, err := ReadIMResponse(&fillerReader{remaining: limit + 1})
		if !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("ReadIMResponse(oversized) = %v, want errResponseTooLarge", err)
		}
	})

	t.Run("accepts body exactly at the cap", func(t *testing.T) {
		const limit = maxWebhookBodyBytes
		data, err := ReadIMResponse(strings.NewReader(strings.Repeat("a", limit)))
		if err != nil {
			t.Fatalf("ReadIMResponse(at cap) = %v, want nil", err)
		}
		if len(data) != limit {
			t.Errorf("len = %d, want %d", len(data), limit)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		data, err := ReadIMResponse(strings.NewReader(""))
		if err != nil {
			t.Fatalf("ReadIMResponse(empty) = %v, want nil", err)
		}
		if len(data) != 0 {
			t.Errorf("len = %d, want 0", len(data))
		}
	})
}

// DecodeIMResponse applies the same cap when decoding a successful reply. A
// decoder alone stops once the JSON value is complete, but that value can still
// carry a multi-megabyte string, so decoding has to be bounded too.
func TestDecodeIMResponse(t *testing.T) {
	t.Run("decodes a small body", func(t *testing.T) {
		var got struct {
			AccessToken string `json:"access_token"`
		}
		if err := DecodeIMResponse(strings.NewReader(`{"access_token":"tok-abc"}`), &got); err != nil {
			t.Fatalf("DecodeIMResponse() = %v, want nil", err)
		}
		if got.AccessToken != "tok-abc" {
			t.Errorf("AccessToken = %q, want %q", got.AccessToken, "tok-abc")
		}
	})

	t.Run("rejects an oversized JSON value", func(t *testing.T) {
		payload := `{"access_token":"` + strings.Repeat("a", maxWebhookBodyBytes) + `"}`
		var got struct {
			AccessToken string `json:"access_token"`
		}
		err := DecodeIMResponse(strings.NewReader(payload), &got)
		if !errors.Is(err, errResponseTooLarge) {
			t.Fatalf("DecodeIMResponse(oversized) = %v, want errResponseTooLarge", err)
		}
		if got.AccessToken != "" {
			t.Errorf("target was partially populated with %d bytes", len(got.AccessToken))
		}
	})
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

// fillerReader streams size bytes without allocating them, so a test can hand
// ReadIMResponse a body far larger than the cap without spending memory itself.
type fillerReader struct {
	remaining int64
}

func (f *fillerReader) Read(p []byte) (int, error) {
	if f.remaining <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > f.remaining {
		p = p[:f.remaining]
	}
	for i := range p {
		p[i] = 'a'
	}
	f.remaining -= int64(len(p))
	return len(p), nil
}
