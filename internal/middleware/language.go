package middleware

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// Language extracts the user's language preference and injects it into the request context.
//
// Priority (highest to lowest):
//  1. WEKNORA_LANGUAGE environment variable (deployment-level override for document processing language)
//  2. Accept-Language HTTP header (first tag, e.g. "zh-CN,zh;q=0.9" → "zh-CN")
//  3. "zh-CN" hardcoded fallback
//
// WEKNORA_LANGUAGE takes precedence over Accept-Language because the UI locale (menu language)
// and the document processing language (question/summary generation) are separate concerns.
// A user may prefer English UI while processing Korean documents.
func Language() gin.HandlerFunc {
	// Read env var once at startup; empty string means "not configured"
	envLang := types.EnvLanguage()

	return func(c *gin.Context) {
		// 1. WEKNORA_LANGUAGE env takes precedence (deployment-level document language)
		if envLang != "" {
			c.Set(types.LanguageContextKey.String(), envLang)
			ctx := context.WithValue(c.Request.Context(), types.LanguageContextKey, envLang)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
			return
		}

		lang := ""

		// 2. Try Accept-Language header
		if acceptLang := c.GetHeader("Accept-Language"); acceptLang != "" {
			lang = parseFirstLanguageTag(acceptLang)
		}

		// 3. Fallback to hardcoded default
		if lang == "" {
			lang = "zh-CN"
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
