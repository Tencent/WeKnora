package middleware

import (
	"context"
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// DefaultLanguage is the fallback language when no preference is specified.
const DefaultLanguage = "zh-CN"

// Language extracts the user's language preference and injects it into the request context.
//
// Priority (highest to lowest):
//  1. Accept-Language HTTP header (first tag, e.g. "zh-CN,zh;q=0.9" → "zh-CN")
//  2. WEKNORA_LANGUAGE environment variable
//  3. DefaultLanguage ("zh-CN")
func Language() gin.HandlerFunc {
	// Read env var once at startup
	envLang := strings.TrimSpace(os.Getenv("WEKNORA_LANGUAGE"))

	return func(c *gin.Context) {
		lang := ""

		// 1. Try Accept-Language header
		if acceptLang := c.GetHeader("Accept-Language"); acceptLang != "" {
			// Parse the first language tag (e.g. "zh-CN,zh;q=0.9,en;q=0.8" → "zh-CN")
			lang = parseFirstLanguageTag(acceptLang)
		}

		// 2. Fallback to environment variable
		if lang == "" && envLang != "" {
			lang = envLang
		}

		// 3. Fallback to default
		if lang == "" {
			lang = DefaultLanguage
		}

		// Inject into context
		c.Set(types.LanguageContextKey.String(), lang)
		ctx := context.WithValue(c.Request.Context(), types.LanguageContextKey, lang)
		c.Request = c.Request.WithContext(ctx)

		c.Next()
	}
}

// parseFirstLanguageTag extracts the first language tag from an Accept-Language header value.
// e.g. "zh-CN,zh;q=0.9,en;q=0.8" → "zh-CN"
// e.g. "zh-CN" → "zh-CN"
func parseFirstLanguageTag(header string) string {
	// Split by comma and take the first entry
	parts := strings.SplitN(header, ",", 2)
	if len(parts) == 0 {
		return ""
	}
	// Remove quality value if present (e.g. "zh-CN;q=0.9" → "zh-CN")
	tag := strings.SplitN(strings.TrimSpace(parts[0]), ";", 2)[0]
	return strings.TrimSpace(tag)
}
