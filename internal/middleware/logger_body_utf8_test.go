package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/gin-gonic/gin"
)

// A Chinese body larger than the 10 KiB log cap must be cut between
// characters. Slicing bytes leaves half a rune in request_body, which the log
// then shows as U+FFFD instead of the character the caller sent.
func TestReadRequestBodyKeepsMultiByteCharactersAtTheLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := `{"content":"` + strings.Repeat("知", 4000) + `"}`
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/test", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	got := readRequestBody(c)

	if !utf8.ValidString(got) {
		t.Fatalf("request body preview is not valid UTF-8 (len=%d)", len(got))
	}
	if !strings.Contains(got, bodyTruncatedMarker) {
		t.Fatalf("body over the limit must carry the truncation marker")
	}
	if len(got) > maxBodySize+len(bodyTruncatedMarker) {
		t.Fatalf("preview length %d exceeds the %d-byte budget", len(got), maxBodySize)
	}
}

// The same cap applies to the captured response body.
func TestLoggerKeepsMultiByteCharactersInResponseBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logBuf bytes.Buffer
	logger.SetOutput(&logBuf)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })

	payload := `{"content":"` + strings.Repeat("知", 4000) + `"}`

	r := gin.New()
	r.Use(Logger())
	r.GET("/api/v1/echo", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.String(http.StatusOK, payload)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/echo", nil))

	out := logBuf.String()
	if !utf8.ValidString(out) {
		t.Fatalf("access log is not valid UTF-8")
	}
	if strings.Contains(out, "\uFFFD") {
		t.Fatalf("access log contains a replacement character: response body was cut mid-rune")
	}
	if !strings.Contains(out, bodyTruncatedMarker) {
		t.Fatalf("oversized response body must carry the truncation marker")
	}
}

// The captured response must be byte-contiguous across Write calls. Cutting to
// a rune boundary inside each call drops bytes that a later call cannot bring
// back, so the same response logs differently depending on how the handler
// splits its writes.
func TestLoggerCapturesResponseBodyAcrossWritesContiguously(t *testing.T) {
	prefix := strings.Repeat("x", maxBodySize-1)
	full := []byte(prefix + "界" + "Z")
	runeStart := len(prefix)

	// Same bytes, two write layouts: the rune is whole in the first call, or
	// split between the calls.
	layouts := map[string]int{
		"whole_rune_in_first_write": runeStart + len("界"),
		"rune_split_across_writes":  runeStart + 1,
	}
	for name, cut := range layouts {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var logBuf bytes.Buffer
			logger.SetOutput(&logBuf)
			t.Cleanup(func() { logger.SetOutput(os.Stdout) })

			r := gin.New()
			r.Use(Logger())
			r.GET("/api/v1/stream", func(c *gin.Context) {
				c.Header("Content-Type", "application/json")
				_, _ = c.Writer.Write(full[:cut])
				_, _ = c.Writer.Write(full[cut:])
			})

			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/stream", nil))

			out := logBuf.String()
			want := prefix + bodyTruncatedMarker
			if !strings.Contains(out, want) {
				t.Fatalf("response body preview must keep the %d captured bytes before the marker", len(prefix))
			}
			if strings.Contains(out, "\uFFFD") {
				t.Fatalf("access log contains a replacement character")
			}
		})
	}
}
