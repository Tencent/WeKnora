package web_search

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fillerReader streams size bytes without allocating them, so a test can hand an
// endpoint a body far larger than any cap without spending the memory itself.
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

// readCapped must refuse an oversized body rather than buffering it, and must
// still pass a body at exactly the cap through untouched.
func TestReadCapped(t *testing.T) {
	t.Run("rejects over the cap", func(t *testing.T) {
		const limit = 1 << 10
		_, err := readCapped(&fillerReader{remaining: limit + 1}, limit)
		if err == nil {
			t.Fatal("expected an over-cap body to be refused")
		}
		if !errors.Is(err, errResponseTooLarge) {
			t.Errorf("err = %v, want errResponseTooLarge", err)
		}
		if !strings.Contains(err.Error(), "exceeds maximum size") {
			t.Errorf("error text = %q, want the canonical over-size message", err)
		}
	})

	t.Run("accepts exactly the cap", func(t *testing.T) {
		const limit = 1 << 10
		got, err := readCapped(strings.NewReader(strings.Repeat("a", limit)), limit)
		if err != nil {
			t.Fatalf("readCapped() = %v, want nil at exactly the cap", err)
		}
		if len(got) != limit {
			t.Errorf("len = %d, want %d", len(got), limit)
		}
	})

	t.Run("non-positive limit falls back to the production cap", func(t *testing.T) {
		got, err := readCapped(strings.NewReader("{}"), 0)
		if err != nil {
			t.Fatalf("readCapped(_, 0) = %v, want nil", err)
		}
		if string(got) != "{}" {
			t.Errorf("body = %q, want %q", got, "{}")
		}
	})
}

// An error preview ends up in a log line and in the error the caller sees, so it
// has to be bounded too. searxng already capped its preview; its siblings did not.
func TestReadErrorPreviewIsBounded(t *testing.T) {
	preview := readErrorPreview(&fillerReader{remaining: 1 << 20})
	if len(preview) != maxErrorPreviewBytes {
		t.Errorf("preview is %d bytes, want %d", len(preview), maxErrorPreviewBytes)
	}
	if preview == "" {
		t.Error("preview is empty; a truncated body is more useful than none")
	}
}

// doSearch reads the response itself, so these two drive the real call sites.
// Bing is the one that used to read the whole body before it even looked at the
// status code.
func TestBingProviderRejectsOversizedSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Valid JSON prefix, then streamed past the cap without the test ever
		// allocating it. Only the size guard can reject this body: a parse error
		// instead would mean the body was capped but then still parsed.
		_, _ = w.Write([]byte(`{"webPages":{"value":[`))
		_, _ = io.Copy(w, &fillerReader{remaining: maxJSONResponseBytes + (1 << 20)})
	}))
	defer srv.Close()

	p := &BingProvider{client: srv.Client()}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	results, err := p.doSearch(context.Background(), req)
	if err == nil {
		t.Fatalf("expected an oversized body to be refused, got %d results", len(results))
	}
	if !errors.Is(err, errResponseTooLarge) {
		t.Errorf("err = %v, want errResponseTooLarge", err)
	}
	if results != nil {
		t.Errorf("results = %v, want nil on a refused body", results)
	}
}

// Before the fix this error carried the entire failing body, so a broken or
// hostile endpoint could push an unbounded blob into the caller's error string
// and into the log line alongside it.
func TestBingProviderBoundsFailedResponseInError(t *testing.T) {
	const bodySize = 1 << 20
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.Copy(w, &fillerReader{remaining: bodySize})
	}))
	defer srv.Close()

	p := &BingProvider{client: srv.Client()}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = p.doSearch(context.Background(), req)
	if err == nil {
		t.Fatal("expected an error for a failing status")
	}
	// Room for the fixed prefix ("bing API returned status 502: ") on top of the cap.
	if limit := len("bing API returned status 502: ") + maxErrorPreviewBytes; len(err.Error()) > limit {
		t.Errorf("error is %d bytes for a %d byte body, want at most %d: the failed "+
			"response is being echoed unbounded", len(err.Error()), bodySize, limit)
	}
}
