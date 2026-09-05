package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type legacyKnowledgeStub struct {
	previewKnowledgeServiceStub
	reads int
}

func (s *legacyKnowledgeStub) GetKnowledgeFile(context.Context, string) (io.ReadCloser, string, error) {
	s.reads++
	return io.NopCloser(bytes.NewBufferString("original")), s.filename, nil
}

type persistentPreviewStub struct {
	interfaces.DocumentPreviewService
	status string
	err    error
	calls  int
	tenant uint64
}

func (s *persistentPreviewStub) Get(
	_ context.Context,
	tenant uint64,
	_ string,
	_ bool,
) (*types.DocumentPreviewResult, error) {
	s.calls++
	s.tenant = tenant
	return &types.DocumentPreviewResult{Status: s.status, Content: io.NopCloser(bytes.NewBufferString("docx"))}, s.err
}

func TestKnowledgePersistentPreviewAndOriginalDownload(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, status string
		err          error
		code         int
	}{
		{"ready", "ready", nil, 200},
		{"pending", "pending", nil, 202},
		{"failed", "failed", nil, 415},
		{"error", "", errors.New("private path"), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &legacyKnowledgeStub{
				previewKnowledgeServiceStub: previewKnowledgeServiceStub{filename: "original.DOC"},
			}
			preview := &persistentPreviewStub{status: tc.status, err: tc.err}
			h := &KnowledgeHandler{kgService: source, preview: preview}
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			r.Use(func(c *gin.Context) { c.Set(types.TenantIDContextKey.String(), uint64(42)); c.Next() })
			r.GET("/knowledge/:id/preview", h.PreviewKnowledgeFile)
			r.GET("/knowledge/:id/download", h.DownloadKnowledgeFile)
			request := func(path string) *closeNotifyRecorder {
				w := &closeNotifyRecorder{ResponseRecorder: httptest.NewRecorder()}
				r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				return w
			}
			got := request("/knowledge/k1/preview")
			require.Equal(t, tc.code, got.Code)
			require.Equal(t, 0, source.reads, "preview must never fetch original")
			require.Equal(t, uint64(42), preview.tenant)
			require.NotContains(t, got.Body.String(), "private path")
			if tc.code == 200 {
				require.Equal(t, "docx", got.Body.String())
				require.Equal(t, docparser.LegacyDocPreviewMIME, got.Header().Get("Content-Type"))
				_, params, err := mime.ParseMediaType(got.Header().Get("Content-Disposition"))
				require.NoError(t, err)
				require.Equal(t, "original.docx", params["filename"])
			}
			if tc.code == 202 {
				require.JSONEq(t, `{"code":"preview_pending","retry_after":2}`, got.Body.String())
			}
			got = request("/knowledge/k1/download")
			require.Equal(t, 200, got.Code)
			require.Equal(t, "original", got.Body.String())
			require.Equal(t, 1, source.reads)
			require.Equal(t, 1, preview.calls)
		})
	}
}

func TestLegacyPreviewCannotRunWithoutKnowledgeAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	preview := &persistentPreviewStub{status: "ready"}
	h := &KnowledgeHandler{
		kgService: &legacyKnowledgeStub{
			previewKnowledgeServiceStub: previewKnowledgeServiceStub{filename: "original.doc"},
		},
		preview: preview,
	}
	r := gin.New()
	r.Use(middleware.ErrorHandler())
	r.GET("/knowledge/:id/preview", h.PreviewKnowledgeFile)
	got := httptest.NewRecorder()
	r.ServeHTTP(got, httptest.NewRequest("GET", "/knowledge/k1/preview", nil))
	require.NotEqual(t, 200, got.Code)
	require.Equal(t, 0, preview.calls)
}
