package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type previewKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	filename string
	content  string
	seekable bool
}

type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (r *closeNotifyRecorder) CloseNotify() <-chan bool {
	ch := make(chan bool, 1)
	return ch
}

func (s *previewKnowledgeServiceStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return &types.Knowledge{ID: "k1", TenantID: 42, KnowledgeBaseID: "kb1"}, nil
}

func (s *previewKnowledgeServiceStub) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	reader := strings.NewReader(s.content)
	if s.seekable {
		return &seekablePreviewFile{reader}, s.filename, nil
	}
	return io.NopCloser(reader), s.filename, nil
}

type seekablePreviewFile struct {
	*strings.Reader
}

func (*seekablePreviewFile) Close() error { return nil }

func TestPreviewKnowledgeFileForcesActiveContentDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.Use(func(c *gin.Context) {
		c.Set(types.TenantIDContextKey.String(), uint64(42))
		c.Next()
	})

	h := &KnowledgeHandler{kgService: &previewKnowledgeServiceStub{
		filename: "payload.html", content: `<html><script>alert(1)</script></html>`,
	}}
	router.GET("/knowledge/:id/preview", h.PreviewKnowledgeFile)

	req := httptest.NewRequest(http.MethodGet, "/knowledge/k1/preview", nil)
	w := &closeNotifyRecorder{ResponseRecorder: httptest.NewRecorder()}
	router.ServeHTTP(w, req)

	if got, want := w.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d; body=%s", got, want, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Fatalf("Content-Type = %q, want application/octet-stream", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment") {
		t.Fatalf("Content-Disposition = %q, want attachment", got)
	}
}

func TestKnowledgeTextFilePreviewEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const gbk = "CentOS\xb3\xa3\xd3\xc3\xc3\xfc\xc1\xee\r\n"
	const utf8 = "CentOS常用命令\r\n"
	for _, tc := range []struct {
		name, filename, action, input, want, contentType, byteRange, contentRange string
		seekable                                                                  bool
		status                                                                    int
	}{
		{
			name: "GBK_stream", filename: "commands.TXT", action: "preview",
			input: gbk, want: utf8, contentType: "text/plain; charset=utf-8", status: http.StatusOK,
		},
		{
			name: "GBK_range", filename: "commands.txt", action: "preview",
			input: gbk, want: "常用", contentType: "text/plain; charset=utf-8",
			seekable: true, byteRange: "bytes=6-11", contentRange: "bytes 6-11/20", status: http.StatusPartialContent,
		},
		{
			name: "UTF8_with_BOM", filename: "commands.txt", action: "preview",
			input: "\xef\xbb\xbf" + utf8, want: "\xef\xbb\xbf" + utf8,
			contentType: "text/plain; charset=utf-8", seekable: true, status: http.StatusOK,
		},
		{
			name: "UTF16_with_BOM", filename: "commands.txt", action: "preview",
			input: "\xff\xfe\x2d\x4e\x87\x65", want: "中文",
			contentType: "text/plain; charset=utf-8", status: http.StatusOK,
		},
		{
			name: "CSV_preview", filename: "commands.csv", action: "preview",
			input: gbk, want: gbk, contentType: "text/csv; charset=utf-8", status: http.StatusOK,
		},
		{
			name: "GBK_download", filename: "commands.txt", action: "download",
			input: gbk, want: gbk, contentType: "application/octet-stream", status: http.StatusOK,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(middleware.ErrorHandler())
			router.Use(func(c *gin.Context) {
				c.Set(types.TenantIDContextKey.String(), uint64(42))
				c.Next()
			})
			h := &KnowledgeHandler{kgService: &previewKnowledgeServiceStub{
				filename: tc.filename, content: tc.input, seekable: tc.seekable,
			}}
			router.GET("/api/v1/knowledge/:id/preview", h.PreviewKnowledgeFile)
			router.GET("/api/v1/knowledge/:id/download", h.DownloadKnowledgeFile)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/knowledge/k1/"+tc.action, nil)
			req.Header.Set("Range", tc.byteRange)
			w := &closeNotifyRecorder{ResponseRecorder: httptest.NewRecorder()}
			router.ServeHTTP(w, req)

			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d; body=%q", w.Code, tc.status, w.Body.String())
			}
			if got := w.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tc.contentType)
			}
			if got := w.Body.String(); got != tc.want {
				t.Errorf("body = %q, want %q", got, tc.want)
			}
			if got := w.Header().Get("Content-Range"); got != tc.contentRange {
				t.Errorf("Content-Range = %q, want %q", got, tc.contentRange)
			}
		})
	}
}
