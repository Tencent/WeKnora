package middleware

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMultipartFormCleanupRemovesDiskTemporaryFiles 验证超过内存阈值的上传在响应后不遗留 multipart 临时文件。
// 入参：2 MiB multipart 文件与 1 字节的 Gin 内存阈值，确保标准库将上传内容写入临时目录。
// 出参：处理时发现 multipart 临时文件；响应完成后专用临时目录为空。
func TestMultipartFormCleanupRemovesDiskTemporaryFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	t.Setenv("TMPDIR", tempDir)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "large.txt")
	if err != nil {
		t.Fatalf("create multipart file = %v", err)
	}
	if _, err = part.Write(bytes.Repeat([]byte("a"), 2*1024*1024)); err != nil {
		t.Fatalf("write multipart file = %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("close multipart writer = %v", err)
	}

	engine := gin.New()
	engine.MaxMultipartMemory = 1
	engine.Use(MultipartFormCleanup())
	temporaryFileFound := false
	engine.POST("/upload", func(c *gin.Context) {
		if _, formErr := c.FormFile("file"); formErr != nil {
			t.Errorf("read uploaded file = %v", formErr)
			return
		}
		entries, readErr := os.ReadDir(tempDir)
		if readErr != nil {
			t.Errorf("read temporary directory = %v", readErr)
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "multipart-") {
				temporaryFileFound = true
				break
			}
		}
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(http.MethodPost, "/upload", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("response status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if !temporaryFileFound {
		t.Fatal("multipart temporary file was not created during request")
	}
	remaining, err := filepath.Glob(filepath.Join(tempDir, "multipart-*"))
	if err != nil {
		t.Fatalf("find remaining multipart files = %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining multipart temporary files = %v, want none", remaining)
	}
}
