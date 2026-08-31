package middleware

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const loginRateLimitKeyPrefix = "auth:login:ratelimit:"

var (
	loginLimiterOnce sync.Once
	loginLimiter     *ratelimit.Limiter
)

// loginRateLimiter creates one shared limiter for password logins. Redis makes
// its sliding window effective across application instances; the limiter's
// local fallback keeps standalone deployments protected when Redis is absent.
func loginRateLimiter(redisClient *redis.Client, window time.Duration) *ratelimit.Limiter {
	loginLimiterOnce.Do(func() {
		loginLimiter = ratelimit.New(redisClient, loginRateLimitKeyPrefix, window, "")
		stopCh := make(chan struct{})
		go loginLimiter.StartCleanup(stopCh)
	})
	return loginLimiter
}

// LoginRateLimit protects POST /auth/login from online password guessing. The
// key is the resolved client IP; Router's trusted-proxy policy prevents a
// caller from bypassing the budget by forging X-Forwarded-For.
func LoginRateLimit(redisClient *redis.Client, max int, window time.Duration) gin.HandlerFunc {
	limiter := loginRateLimiter(redisClient, window)
	retryAfter := int(window.Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	return func(c *gin.Context) {
		if !limiter.Allow(c.Request.Context(), c.ClientIP(), max) {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			c.Error(&apperrors.AppError{
				Code:     apperrors.ErrTooManyRequests,
				Message:  "too many login attempts; please retry later",
				HTTPCode: http.StatusTooManyRequests,
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
