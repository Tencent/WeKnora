package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// fillerReader streams size bytes without allocating them, so a test can offer
// a body far larger than the cap without spending the memory.
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

// allowLoopback whitelists the loopback host Endpoint.newPost's SSRF check
// rejects, so a test can point an Endpoint at its own httptest server.
func allowLoopback(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}

func TestReadResponseCapped(t *testing.T) {
	t.Run("rejects over the cap", func(t *testing.T) {
		const limit = 1 << 10
		_, err := ReadResponseCapped(&fillerReader{remaining: limit + 1}, limit)
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("err = %v, want ErrResponseTooLarge", err)
		}
		if !strings.Contains(err.Error(), "exceeds maximum size") {
			t.Errorf("error text = %q, want the canonical over-size message", err)
		}
	})

	t.Run("accepts exactly the cap", func(t *testing.T) {
		const limit = 1 << 10
		got, err := ReadResponseCapped(strings.NewReader(strings.Repeat("a", limit)), limit)
		if err != nil {
			t.Fatalf("ReadResponseCapped() = %v, want nil at exactly the cap", err)
		}
		if len(got) != limit {
			t.Errorf("len = %d, want %d", len(got), limit)
		}
	})

	t.Run("non-positive limit falls back to the production cap", func(t *testing.T) {
		if _, err := ReadResponseCapped(strings.NewReader(`{"ok":true}`), 0); err != nil {
			t.Fatalf("ReadResponseCapped(_, 0) = %v, want nil", err)
		}
	})
}

// The cap must not silently truncate: a short read that still parses would look
// like a successful reply with fewer fields than the vendor sent.
func TestReadResponseCappedNeverTruncates(t *testing.T) {
	const limit = 1 << 10
	got, err := ReadResponseCapped(strings.NewReader(strings.Repeat("a", limit*4)), limit)
	if err == nil {
		t.Fatalf("expected refusal, got %d bytes", len(got))
	}
	if got != nil {
		t.Errorf("returned %d bytes alongside the error; a caller could decode them", len(got))
	}
}

// PostJSON is the whole round-trip for rerank, embeddings and transcription. An
// oversized success body has to surface as an error rather than a decode
// failure on a truncated payload.
//
// Endpoint.newPost validates the URL, so the test server's loopback address has
// to be whitelisted the same way other tests in this repo reach an httptest
// server.
func TestPostJSONRejectsOversizedBody(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[`))
		_, _ = io.Copy(w, &fillerReader{remaining: MaxResponseBytes + (1 << 20)})
	}))
	defer srv.Close()

	var out map[string]any
	err := Endpoint{Client: srv.Client()}.PostJSON(
		context.Background(), srv.URL, []byte(`{}`), &out)
	if err == nil {
		t.Fatal("expected an oversized provider body to be refused")
	}
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("err = %v, want ErrResponseTooLarge", err)
	}
	if out != nil {
		t.Errorf("out = %v, want nothing decoded from a refused body", out)
	}
}

// withRetry retries only TransportError. An oversized reply is the same answer
// every time, and retrying it bills the request again for the same bytes, so it
// must not be classified as a transport failure.
func TestOversizedBodyIsNotRetried(t *testing.T) {
	allowLoopback(t)

	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[`))
		_, _ = io.Copy(w, &fillerReader{remaining: MaxResponseBytes + (1 << 20)})
	}))
	defer srv.Close()

	policy := RetryPolicy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	var out map[string]any
	err := Endpoint{Client: srv.Client()}.PostJSONWithRetry(
		context.Background(), srv.URL, []byte(`{}`), &out, policy, "embedding")
	if err == nil {
		t.Fatal("expected an oversized provider body to be refused")
	}
	if attempts != 1 {
		t.Errorf("made %d attempts, want 1: an oversized reply is deterministic "+
			"and retrying it bills the request again", attempts)
	}
}
