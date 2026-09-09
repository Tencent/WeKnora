package filetransport

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type seekFile struct {
	*bytes.Reader
	closed bool
}

func (f *seekFile) Close() error { f.closed = true; return nil }

func TestServeSeekableFile(t *testing.T) {
	for _, tt := range []struct {
		name, method, rangeHeader, body string
		status                          int
	}{
		{"whole", "GET", "", "0123456789", 200},
		{"range", "GET", "bytes=2-5", "2345", 206},
		{"suffix", "GET", "bytes=-3", "789", 206},
		{"head", "HEAD", "", "", 200},
		{"unsatisfiable", "GET", "bytes=20-30", "", 416},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reader := &seekFile{Reader: bytes.NewReader([]byte("0123456789"))}
			req := httptest.NewRequest(tt.method, "/file", nil)
			req.Header.Set("Range", tt.rangeHeader)
			w := httptest.NewRecorder()
			require.NoError(t, Serve(w, req, reader, Options{Filename: "image.png", CacheControl: "private, no-store"}))
			require.Equal(t, tt.status, w.Code)
			if tt.status != 416 {
				require.Equal(t, tt.body, w.Body.String())
			}
			require.True(t, reader.closed)
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			if tt.status == 206 {
				require.Contains(t, w.Header().Get("Content-Range"), "/10")
			}
		})
	}
}

type streamFile struct{ read, closed bool }

func (f *streamFile) Read([]byte) (int, error) { f.read = true; return 0, io.EOF }
func (f *streamFile) Close() error             { f.closed = true; return nil }

func TestServeStreamingHEADClosesWithoutReading(t *testing.T) {
	reader := &streamFile{}
	w := httptest.NewRecorder()
	require.NoError(
		t,
		Serve(w, httptest.NewRequest("HEAD", "/file", nil), reader, Options{Filename: "a.pdf", Size: 123}),
	)
	require.False(t, reader.read)
	require.True(t, reader.closed)
	require.Equal(t, "123", w.Header().Get("Content-Length"))
}

func TestServeActiveContentAndStreamingRange(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/file", nil)
	req.Header.Set("Range", "bytes=0-1")
	require.NoError(t, Serve(w, req, io.NopCloser(strings.NewReader("<svg/>")), Options{Filename: "../payload.svg"}))
	require.Equal(t, http.StatusOK, w.Code, "non-seekable readers return the complete body")
	require.Equal(t, "<svg/>", w.Body.String())
	require.Equal(t, "none", w.Header().Get("Accept-Ranges"))
	require.Equal(t, "attachment; filename=payload.svg", w.Header().Get("Content-Disposition"))
}
