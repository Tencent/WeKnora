package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestPrivateAccessLogSkipsBrowserAndLearningPayloads(t *testing.T) {
	for _, test := range []struct {
		method, path string
		omitQuery    bool
	}{
		{"POST", "/api/v1/learning", true},
		{"POST", "/api/v1/learning/attempts", true},
		{"GET", "/api/v1/learning/export", true},
		{"POST", "/api/v1/local-browser/extension/authorize", false},
		{"POST", "/api/v1/local-browser/internal", false},
		{"POST", "/api/v1/me/browser", false},
		{"POST", "/api/v1/sessions/session/local-browser", false},
	} {
		t.Run(test.path, func(t *testing.T) {
			var output bytes.Buffer
			log := logrus.New()
			log.SetOutput(&output)
			body := io.NopCloser(strings.NewReader(`{"content":"private-request"}`))
			engine := gin.New()
			engine.ContextWithFallback = true
			engine.Use(Logger())
			engine.Handle(test.method, test.path, func(c *gin.Context) {
				require.Equal(t, body, c.Request.Body, "private request bodies must not be read or replaced")
				_, capturing := c.Writer.(*loggerResponseBodyWriter)
				require.False(t, capturing, "private responses must not be buffered")
				content, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Contains(t, string(content), "private-request")
				c.JSON(200, gin.H{"content": "private-response"})
			})
			request := httptest.NewRequest(
				test.method, test.path+"?token=private-query&filter=scope-filter", body,
			)
			request.Header.Set("Content-Type", "application/json")
			request = request.WithContext(
				context.WithValue(request.Context(), types.LoggerContextKey, logrus.NewEntry(log)),
			)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			require.Equal(t, 200, response.Code)
			require.Contains(t, response.Body.String(), "private-response")
			require.Contains(t, output.String(), test.path)
			for _, secret := range []string{
				"private-request", "private-response", "private-query", "request_body", "response_body",
			} {
				require.NotContains(t, output.String(), secret)
			}
			if test.omitQuery {
				require.NotContains(t, output.String(), "scope-filter")
			} else {
				require.Contains(t, output.String(), "scope-filter")
			}
		})
	}
}
