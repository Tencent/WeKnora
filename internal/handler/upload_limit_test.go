package handler

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// multipartBody frames a file of exactly size bytes the way a browser would,
// so the envelope slack is measured against real boundary lines.
func multipartBody(t *testing.T, size int) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", "upload.bin")
	require.NoError(t, err)
	_, err = part.Write(bytes.Repeat([]byte("a"), size))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return writer.FormDataContentType(), buf.Bytes()
}

func uploadLimitProbe(t *testing.T, maxBytes int64, fileSize int) (int, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/upload", func(c *gin.Context) {
		limitUploadBody(c, maxBytes)
		if _, err := c.FormFile("file"); err != nil {
			if isRequestBodyTooLarge(err) {
				c.String(http.StatusRequestEntityTooLarge, "too large")
				return
			}
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		c.String(http.StatusOK, "ok")
	})

	contentType, body := multipartBody(t, fileSize)
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// The cap has to be the multipart parse's problem, not a size check that runs
// after it: a handler reaching FormFile has already let the body be buffered to
// a temp file, which is exactly the amplification the limit exists to stop.
func TestLimitUploadBodyRejectsAnOversizedUploadDuringTheParse(t *testing.T) {
	code, body := uploadLimitProbe(t, 1<<20, 4<<20)

	require.Equal(t, http.StatusRequestEntityTooLarge, code)
	require.Equal(t, "too large", body)
}

// The slack exists so the boundary lines of a legal upload cannot push it over.
func TestLimitUploadBodyAcceptsAFileAtExactlyTheCap(t *testing.T) {
	code, body := uploadLimitProbe(t, 1<<20, 1<<20)

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "ok", body)
}

// Every byte of framing still counts, so a body that clears the cap only by
// exceeding the slack is refused rather than silently allowed.
func TestLimitUploadBodyRejectsAFileBeyondTheSlack(t *testing.T) {
	code, _ := uploadLimitProbe(t, 1<<20, (1<<20)+uploadEnvelopeSlack+1)

	require.Equal(t, http.StatusRequestEntityTooLarge, code)
}

// A body cap firing and a request that named no file are different answers.
// Conflating them reports "file is required" for a file that was sent.
func TestIsRequestBodyTooLargeIgnoresAnAbsentPart(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/upload", func(c *gin.Context) {
		limitUploadBody(c, 1<<20)
		_, err := c.FormFile("file")
		require.Error(t, err)
		require.False(t, isRequestBodyTooLarge(err))
		c.Status(http.StatusBadRequest)
	})

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	require.NoError(t, writer.WriteField("input", strings.Repeat("a", 16)))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	engine.ServeHTTP(httptest.NewRecorder(), req)
}
