package session

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// uploadOversizedAttachment drives the handler with a multipart body past the
// upload cap, so FormFile fails on the cap rather than on a missing part.
func uploadOversizedAttachment(t *testing.T, h *Handler) (*gin.Context, string) {
	t.Helper()
	// One byte past the cap the handler installs (MAX_FILE_SIZE_MB + 1 MiB of
	// multipart slack), written as a file part so the failure is the cap.
	payload := strings.Repeat("x", int(secutils.GetMaxFileSizeMB()*1024*1024)+2*1024*1024)
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	part, err := form.CreateFormFile("file", "huge.bin")
	require.NoError(t, err)
	_, err = part.Write([]byte(payload))
	require.NoError(t, err)
	require.NoError(t, form.Close())

	c, ctx := newResolveAgentTestContext(7)
	c.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	req := httptest.NewRequest(http.MethodPost, "/", body).WithContext(ctx)
	req.Header.Set("Content-Type", form.FormDataContentType())
	c.Request = req
	h.UploadTemporaryDocument(c)

	message := ""
	if len(c.Errors) > 0 {
		message = c.Errors.Last().Error()
	}
	return c, message
}

// An oversized attachment is refused by the body cap, which fails the multipart
// parse. Reporting that as "invalid attachment upload" points the caller at a
// missing field for a file that was sent; it has to be reported as the limit it
// hit.
func TestUploadTemporaryDocumentReportsOversizedAttachmentAsLimit(t *testing.T) {
	documents := &engineTestDocuments{}
	h := &Handler{sessionService: engineTestSessions{}, temporaryDocuments: documents}

	_, message := uploadOversizedAttachment(t, h)

	require.NotEmpty(t, message, "an oversized upload must be refused")
	require.Contains(t, message, "upload limit",
		"the refusal must name the limit, not a malformed upload")
	require.NotContains(t, message, "invalid attachment upload",
		"an oversized upload is not a malformed one")
}
