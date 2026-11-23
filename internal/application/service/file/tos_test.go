package file

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/filetransport"
	"github.com/stretchr/testify/require"
	"github.com/volcengine/ve-tos-golang-sdk/v2/tos"
)

type tosPreviewTransport struct {
	body   string
	closed bool
}

type tosPreviewBody struct {
	io.Reader
	transport *tosPreviewTransport
}

func (b *tosPreviewBody) Close() error { b.transport.closed = true; return nil }

func (s *tosPreviewTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Length": []string{"10"}},
		ContentLength: int64(len(s.body)),
		Body:          &tosPreviewBody{Reader: strings.NewReader(s.body), transport: s},
		Request:       req,
	}, nil
}

// Use the real SDK: its download wrappers expose Seek even when the HTTP
// response body cannot seek. A plain io.NopCloser fixture misses this bug.
func TestTOSGetFilePreviewStreamsSDKBody(t *testing.T) {
	for _, filename := range []string{"document.pdf", "document.md", "payload.html"} {
		for _, scenario := range []struct {
			name, method, rangeHeader string
			download                  bool
		}{
			{"preview", http.MethodGet, "", false},
			{"range", http.MethodGet, "bytes=2-5", false},
			{"head", http.MethodHead, "", false},
			{"download", http.MethodGet, "", true},
		} {
			t.Run(filename+"/"+scenario.name, func(t *testing.T) {
				transport := &tosPreviewTransport{body: "0123456789"}
				client, err := tos.NewClientV2("https://tos.example.com", tos.WithRegion("cn-beijing"),
					tos.WithCredentials(tos.NewStaticCredentials("ak", "sk")), tos.WithHTTPTransport(transport))
				require.NoError(t, err)
				service := &tosFileService{client: client, bucketName: "documents"}
				reader, err := service.GetFile(context.Background(), "tos://documents/42/"+filename)
				require.NoError(t, err)
				req := httptest.NewRequest(scenario.method, "/api/v1/knowledge/k1/preview", nil)
				req.Header.Set("Range", scenario.rangeHeader)
				w := httptest.NewRecorder()
				options := filetransport.Options{Filename: filename, Download: scenario.download}
				require.NoError(t, filetransport.Serve(w, req, reader, options))
				require.Equal(t, http.StatusOK, w.Code, "preview response: %s", w.Body.String())
				if scenario.method == http.MethodGet {
					require.Equal(t, transport.body, w.Body.String())
				} else {
					require.Empty(t, w.Body.String())
				}
				require.True(t, transport.closed)
				require.Equal(t, "none", w.Header().Get("Accept-Ranges"))
				require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
				disposition := "inline"
				if filename == "payload.html" || scenario.download {
					disposition = "attachment"
				}
				require.True(t, strings.HasPrefix(w.Header().Get("Content-Disposition"), disposition))
			})
		}
	}
}
