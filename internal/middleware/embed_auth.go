package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// EmbedChannelContextKey stores the authenticated embed channel on the request context.
const EmbedChannelContextKey types.ContextKey = "EmbedChannel"

type embedRateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	buckets map[string][]time.Time
}

func newEmbedRateLimiter(window time.Duration) *embedRateLimiter {
	return &embedRateLimiter{window: window, buckets: make(map[string][]time.Time)}
}

func (l *embedRateLimiter) allow(key string, max int) bool {
	if max <= 0 {
		return true
	}
	now := time.Now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	times := l.buckets[key]
	filtered := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	if len(filtered) >= max {
		l.buckets[key] = filtered
		return false
	}
	filtered = append(filtered, now)
	l.buckets[key] = filtered
	return true
}

var embedLimiter = newEmbedRateLimiter(time.Minute)

// EmbedAuth validates publish tokens and injects a scoped tenant context for embed routes.
func EmbedAuth(svc interfaces.EmbedChannelService, tenantSvc interfaces.TenantService) gin.HandlerFunc {
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

		ch, err := svc.LookupForEmbed(c.Request.Context(), channelID, token)
		if err != nil {
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

		rateKey := fmt.Sprintf("%s:%s", channelID, c.ClientIP())
		if !embedLimiter.allow(rateKey, ch.RateLimitPerMinute) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
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
	auth := c.GetHeader("Authorization")
	if strings.HasPrefix(auth, "Embed ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Embed "))
	}
	return strings.TrimSpace(c.Query("token"))
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
	if len(allowed) == 0 {
		return true
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
