package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWeChatPublicRoutesAreMethodScoped(t *testing.T) {
	for path, method := range map[string]string{
		"attempts": http.MethodPost, "exchange": http.MethodPost, "complete": http.MethodPost,
		"start": http.MethodGet, "callback": http.MethodGet,
	} {
		route := "/api/v1/auth/wechat/" + path
		require.True(t, isNoAuthAPI(route, method))
		require.False(t, isNoAuthAPI(route, http.MethodDelete))
		require.False(t, isNoAuthAPI(route+"/other", method))
	}
	require.False(t, isNoAuthAPI("/api/v1/wechat/qrcode", http.MethodPost))
}

func TestWeChatRateLimitRedirectsBrowserNavigations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &ipRateLimiter{window: time.Minute, max: 1}
	r := gin.New()
	r.GET("/start", rateLimitByIP(limiter, "/#wechat_error=rate_limited"), func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})
	for i, want := range []int{http.StatusNoContent, http.StatusFound} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/start", nil))
		require.Equal(t, want, w.Code, "request %d", i)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/start", nil))
	require.Equal(t, "/#wechat_error=rate_limited", w.Header().Get("Location"))
	require.NotSame(t, publicAuthLimiter, weChatAuthLimiter, "sign-ins must not share the share-link budget")
}
