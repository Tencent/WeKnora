package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/ratelimit"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const (
	embedRateLimitKeyPrefix      = "embed:ratelimit:"
	embedDailyRateLimitKeyPrefix = "embed:ratelimit:day:"

	// embedGlobalMinuteFactor derives a channel-wide per-minute cap from the
	// per-IP cap. The publish token is publicly visible, so a single attacker
	// can rotate IPs to defeat the per-IP limit; this bounds aggregate burst.
	embedGlobalMinuteFactor = 20
	// embedGlobalMinuteFloor keeps the global per-minute cap usable even when
	// the per-IP cap is tiny.
	embedGlobalMinuteFloor = 120
)

// EmbedChannelContextKey stores the authenticated embed channel on the request context.
const EmbedChannelContextKey types.ContextKey = "EmbedChannel"

var (
	embedLimiterOnce sync.Once
	embedLimiter     *ratelimit.Limiter

	embedDailyLimiterOnce sync.Once
	embedDailyLimiter     *ratelimit.Limiter
)

func embedRateLimiter(redisClient *redis.Client) *ratelimit.Limiter {
	embedLimiterOnce.Do(func() {
		embedLimiter = ratelimit.New(redisClient, embedRateLimitKeyPrefix, time.Minute, "")
		// Local-fallback eviction; Redis keys expire via PEXPIRE in the Lua script.
		stopCh := make(chan struct{})
		go embedLimiter.StartCleanup(stopCh)
	})
	return embedLimiter
}

func embedDailyRateLimiter(redisClient *redis.Client) *ratelimit.Limiter {
	embedDailyLimiterOnce.Do(func() {
		embedDailyLimiter = ratelimit.New(redisClient, embedDailyRateLimitKeyPrefix, 24*time.Hour, "")
		stopCh := make(chan struct{})
		go embedDailyLimiter.StartCleanup(stopCh)
	})
	return embedDailyLimiter
}

// embedGlobalPerMinute returns the channel-wide per-minute budget derived from
// the per-IP budget.
func embedGlobalPerMinute(perIP int) int {
	budget := perIP * embedGlobalMinuteFactor
	if budget < embedGlobalMinuteFloor {
		budget = embedGlobalMinuteFloor
	}
	return budget
}

// EmbedAuth validates publish tokens and injects a scoped tenant context for embed routes.
func EmbedAuth(
	svc interfaces.EmbedChannelService,
	tenantSvc interfaces.TenantService,
	redisClient *redis.Client,
) gin.HandlerFunc {
	limiter := embedRateLimiter(redisClient)
	dailyLimiter := embedDailyRateLimiter(redisClient)
	return func(c *gin.Context) {
		channelID := strings.TrimSpace(c.Param("channel_id"))
		if channelID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "channel_id is required"})
			c.Abort()
			return
		}

		token := extractEmbedToken(c)
		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "embed publish token is required"})
			c.Abort()
			return
		}

		var ch *types.EmbedChannel
		var err error
		if service.IsEmbedSessionToken(token) {
			resolvedID, resolveErr := svc.ResolveSessionToken(c.Request.Context(), token)
			if resolveErr != nil || resolvedID != channelID {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid embed channel or token"})
				c.Abort()
				return
			}
			ch, err = svc.LookupEnabledChannel(c.Request.Context(), channelID)
		} else {
			ch, err = svc.LookupForEmbed(c.Request.Context(), channelID, token)
		}
		if err != nil {
			if errors.Is(err, service.ErrEmbedChannelDisabled) {
				c.JSON(http.StatusForbidden, gin.H{"error": "embed channel is disabled"})
				c.Abort()
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid embed channel or token"})
			c.Abort()
			return
		}

		origin := requestOrigin(c)
		if !originAllowed(origin, ch.AllowedOriginsList()) {
			logger.Warnf(c.Request.Context(), "[embed_auth] origin %q not allowed for channel %s", origin, channelID)
			c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
			c.Abort()
			return
		}

		// Per-IP per-minute cap.
		rateKey := fmt.Sprintf("%s:%s", channelID, c.ClientIP())
		if !limiter.Allow(c.Request.Context(), rateKey, ch.RateLimitPerMinute) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		// Channel-wide per-minute cap (bounds burst across rotating IPs since
		// the publish token is publicly visible).
		if !limiter.Allow(c.Request.Context(), channelID+":__global", embedGlobalPerMinute(ch.RateLimitPerMinute)) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			c.Abort()
			return
		}
		// Channel-wide daily total cap (bounds sustained abuse).
		if !dailyLimiter.Allow(c.Request.Context(), channelID, ch.RateLimitPerDay) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "daily request limit exceeded"})
			c.Abort()
			return
		}

		tenant, err := tenantSvc.GetTenantByID(c.Request.Context(), ch.TenantID)
		if err != nil || tenant == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "tenant unavailable"})
			c.Abort()
			return
		}

		user := &types.User{
			ID:       fmt.Sprintf("embed-%s", channelID),
			Username: fmt.Sprintf("embed-%s", channelID),
			Email:    fmt.Sprintf("embed-%s@embed.local", channelID),
			TenantID: ch.TenantID,
			IsActive: true,
		}

		c.Set(types.TenantIDContextKey.String(), ch.TenantID)
		c.Set(types.TenantInfoContextKey.String(), tenant)
		c.Set(types.UserContextKey.String(), user)
		c.Set(types.UserIDContextKey.String(), user.ID)
		c.Set(types.TenantRoleContextKey.String(), types.TenantRoleViewer)
		c.Set(types.SystemAdminContextKey.String(), false)
		c.Set(string(EmbedChannelContextKey), ch)

		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, ch.TenantID)
		ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
		ctx = context.WithValue(ctx, types.UserContextKey, user)
		ctx = context.WithValue(ctx, types.UserIDContextKey, user.ID)
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleViewer)
		ctx = context.WithValue(ctx, types.SystemAdminContextKey, false)
		ctx = context.WithValue(ctx, EmbedChannelContextKey, ch)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func extractEmbedToken(c *gin.Context) string {
	// Only accept the token via the Authorization header. A query-string token
	// would be captured by proxy/access logs and browser history; the embed
	// client always sends "Authorization: Embed <token>".
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Embed ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Embed "))
	}
	return ""
}

func requestOrigin(c *gin.Context) string {
	if o := strings.TrimSpace(c.GetHeader("Origin")); o != "" {
		return o
	}
	ref := strings.TrimSpace(c.GetHeader("Referer"))
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func originAllowed(origin string, allowed []string) bool {
	// Empty allowlist rejects all origins. Management create/update requires at
	// least one origin; legacy rows with [] must be fixed before going live.
	if len(allowed) == 0 {
		return false
	}
	if origin == "" {
		return false
	}
	for _, pattern := range allowed {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if pattern == "*" || strings.EqualFold(pattern, origin) {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(origin, suffix) {
				return true
			}
		}
	}
	return false
}

// EmbedChannelFromContext returns the authenticated embed channel, if any.
func EmbedChannelFromContext(ctx context.Context) (*types.EmbedChannel, bool) {
	ch, ok := ctx.Value(EmbedChannelContextKey).(*types.EmbedChannel)
	return ch, ok && ch != nil
}
