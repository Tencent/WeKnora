package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestLoginRateLimitRejectsRequestsOverBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Use a distinct max/window for this test. The package singleton is created
	// only once, so this test owns the first construction in this package.
	r := gin.New()
	r.Use(ErrorHandler())
	r.POST("/login", LoginRateLimit(nil, 2, time.Hour), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
		require.Equal(t, http.StatusNoContent, w.Code)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/login", nil))
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Equal(t, "3600", w.Header().Get("Retry-After"))
}
