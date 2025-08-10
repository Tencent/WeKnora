package middleware

import (
	"context"
	"errors"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// 无需认证的API列表
var noAuthAPI = map[string][]string{
	"/api/v1/test-data":        {"GET"},
	"/api/v1/tenants":          {"POST"},
	"/api/v1/initialization/*": {"GET", "POST"},
}

// 检查请求是否在无需认证的API列表中
func isNoAuthAPI(path string, method string) bool {
	for api, methods := range noAuthAPI {
		// 如果以*结尾，按照前缀匹配，否则按照全路径匹配
		if strings.HasSuffix(api, "*") {
			if strings.HasPrefix(path, strings.TrimSuffix(api, "*")) && slices.Contains(methods, method) {
				return true
			}
		} else if path == api && slices.Contains(methods, method) {
			return true
		}
	}
	return false
}

// Auth 认证中间件
func Auth(tenantService interfaces.TenantService, cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ignore OPTIONS request
		if c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}

		// 检查请求是否在无需认证的API列表中
		if isNoAuthAPI(c.Request.URL.Path, c.Request.Method) {
			c.Next()
			return
		}

		// Get API Key from request header
		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}

		// Get tenant information
		tenantID, err := tenantService.ExtractTenantIDFromAPIKey(apiKey)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: invalid API key format",
			})
			c.Abort()
			return
		}

		// Verify API key validity (matches the one in database)
		t, err := tenantService.GetTenantByID(c.Request.Context(), tenantID)
		if err != nil {
			log.Printf("Error getting tenant by ID: %v, tenantID: %d, apiKey: %s", err, tenantID, apiKey)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: invalid API key",
			})
			c.Abort()
			return
		}

		if t == nil || t.APIKey != apiKey {
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized: invalid API key",
			})
			c.Abort()
			return
		}

		// Store tenant ID in context
		c.Set(types.TenantIDContextKey.String(), tenantID)
		c.Set(types.TenantInfoContextKey.String(), t)
		c.Request = c.Request.WithContext(
			context.WithValue(
				context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID),
				types.TenantInfoContextKey, t,
			),
		)
		c.Next()
	}
}

// GetTenantIDFromContext helper function to get tenant ID from context
func GetTenantIDFromContext(ctx context.Context) (uint, error) {
	tenantID, ok := ctx.Value("tenantID").(uint)
	if !ok {
		return 0, errors.New("tenant ID not found in context")
	}
	return tenantID, nil
}
