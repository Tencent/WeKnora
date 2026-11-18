package utils

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type downloadTestTransport func(*http.Request) (*http.Response, error)

func (f downloadTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type downloadTestBody struct {
	remaining int64
	read      int64
	closed    bool
	err       error
}

func (b *downloadTestBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > b.remaining {
		n = b.remaining
	}
	clear(p[:int(n)])
	b.remaining -= n
	b.read += n
	return int(n), nil
}

func (b *downloadTestBody) Close() error {
	b.closed = true
	return nil
}

func TestDownloadBytesSizeLimit(t *testing.T) {
	const limit = int64(1024 * 1024)
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	t.Setenv("SSRF_WHITELIST", "download.example")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	ResetSSRFWhitelistForTest()
	t.Cleanup(ResetSSRFWhitelistForTest)
	originalClient := defaultHTTPClient
	t.Cleanup(func() { defaultHTTPClient = originalClient })

	tests := []struct {
		name          string
		contentLength int64
		size          int64
		status        int
		readErr       error
		wantErr       string
		wantRead      int64
	}{
		{name: "small body", contentLength: 4, size: 4, wantRead: 4},
		{name: "empty body"},
		{name: "exact limit", contentLength: limit, size: limit, wantRead: limit},
		{name: "exact limit without length", contentLength: -1, size: limit, wantRead: limit},
		{name: "oversized content length", contentLength: limit + 1, size: limit + 1, wantErr: "exceeds"},
		{
			name: "oversized stream without length", contentLength: -1, size: 2 * limit,
			wantErr: "exceeds", wantRead: limit + 1,
		},
		{
			name: "understated content length", contentLength: 1, size: 2 * limit,
			wantErr: "exceeds", wantRead: limit + 1,
		},
		{name: "HTTP failure", status: http.StatusNotFound, contentLength: 4, size: 4, wantErr: "HTTP 404"},
		{
			name: "read failure", contentLength: -1, readErr: errors.New("broken stream"),
			wantErr: "read body: broken stream",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &downloadTestBody{remaining: tt.size, err: tt.readErr}
			status := tt.status
			if status == 0 {
				status = http.StatusOK
			}
			transport := downloadTestTransport(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    status,
					ContentLength: tt.contentLength,
					Body:          body,
					Header:        make(http.Header),
					Request:       req,
				}, nil
			})
			defaultHTTPClient = &http.Client{Transport: transport}

			data, err := DownloadBytes("https://download.example/image.png")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				if data != nil {
					t.Fatal("failed download returned partial data")
				}
			} else if err != nil || int64(len(data)) != tt.size {
				t.Fatalf("download length = %d, error = %v, want %d bytes", len(data), err, tt.size)
			}
			if body.read != tt.wantRead {
				t.Errorf("read %d bytes, want %d", body.read, tt.wantRead)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}

// Issue: https://github.com/Tencent/WeKnora/issues/5240
// A stalled remote image server must not hold a cancelled caller hostage:
// DownloadBytesContext aborts the in-flight request when ctx is done.
func TestDownloadBytesContextAbortsOnCancellation(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "download.example")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	ResetSSRFWhitelistForTest()
	t.Cleanup(ResetSSRFWhitelistForTest)
	originalClient := defaultHTTPClient
	t.Cleanup(func() { defaultHTTPClient = originalClient })

	stalled := make(chan struct{})
	release := make(chan struct{})
	var sawAbort atomic.Bool
	transport := downloadTestTransport(func(req *http.Request) (*http.Response, error) {
		// Simulate a server that stalls before sending headers: the
		// response arrives only when the handler releases it, and the
		// transport reports the caller cancellation through req ctx.
		select {
		case <-release:
			return nil, errors.New("released without response")
		case <-req.Context().Done():
			sawAbort.Store(true)
			return nil, req.Context().Err()
		case <-stalled:
			return nil, errors.New("stalled")
		}
	})
	defaultHTTPClient = &http.Client{Transport: transport}

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := DownloadBytesContext(ctx, "https://download.example/stall.png")
		errc <- err
	}()

	// Give the request a moment to reach the transport, then cancel the
	// caller while the download is in flight.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("expected cancellation error, got nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("image download kept waiting after cancellation")
	}
	if !sawAbort.Load() {
		t.Fatal("in-flight request was not aborted with the caller context")
	}
}
