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

func TestLearningAccessLogNeverCapturesAssessmentContent(t *testing.T) {
	var output bytes.Buffer
	log := logrus.New()
	log.SetOutput(&output)
	log.SetLevel(logrus.DebugLevel)
	engine := gin.New()
	engine.ContextWithFallback = true
	engine.Use(Logger())
	engine.POST("/api/v1/learning/attempts", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "learner-private-choice")
		c.JSON(200, gin.H{"correct_option": "private-key", "explanation": "private-explanation"})
	})
	request := httptest.NewRequest("POST", "/api/v1/learning/attempts?answer=private-query", strings.NewReader(`{"option_id":"learner-private-choice"}`))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(context.WithValue(request.Context(), types.LoggerContextKey, logrus.NewEntry(log)))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "private-explanation")
	require.Contains(t, output.String(), "/api/v1/learning/attempts")
	for _, secret := range []string{"learner-private-choice", "private-key", "private-explanation", "private-query"} {
		require.NotContains(t, output.String(), secret)
	}
}
