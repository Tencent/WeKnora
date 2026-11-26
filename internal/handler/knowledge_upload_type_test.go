package handler

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type uploadTypeKnowledgeService struct {
	interfaces.KnowledgeService
}

func (s *uploadTypeKnowledgeService) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	file *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	// Same gate as the service: an unsupported extension rejects the upload.
	if strings.HasSuffix(file.Filename, ".exe") {
		return nil, service.ErrInvalidFileType
	}
	return &types.Knowledge{ID: "doc", Title: file.Filename}, nil
}

func uploadFileRequest(t *testing.T, filename string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	require.NoError(t, err)
	_, err = part.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/kb/knowledge/file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// POST /knowledge-bases/:id/knowledge/file answers 400 for an unsupported
// extension. The service returns ErrInvalidFileType for such files, and the
// handler only forwards *AppError unchanged; a plain sentinel falls into the
// catch-all branch and is reported as HTTP 500.
func TestCreateKnowledgeFromFileRejectsUnsupportedType(t *testing.T) {
	h := &KnowledgeHandler{
		kgService: &uploadTypeKnowledgeService{},
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/:id/knowledge/file", h.CreateKnowledgeFromFile)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, uploadFileRequest(t, "malware.exe"))

	require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "unsupported file type")
}

// A supported extension still goes through, so the type check cannot reject
// valid uploads.
func TestCreateKnowledgeFromFileAcceptsSupportedType(t *testing.T) {
	h := &KnowledgeHandler{
		kgService: &uploadTypeKnowledgeService{},
		kbService: &stubKBService{get: func(context.Context, string) (*types.KnowledgeBase, error) {
			return &types.KnowledgeBase{ID: "kb", TenantID: 7}, nil
		}},
	}
	r := documentHandlerRouter()
	r.POST("/:id/knowledge/file", h.CreateKnowledgeFromFile)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, uploadFileRequest(t, "notes.md"))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
}
