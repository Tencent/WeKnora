package langfuse

import (
	"context"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// GinMiddleware returns a Gin handler that opens a Langfuse trace for each
// incoming request that hits a traced path. The trace is auto-finished when
// the handler chain returns; individual LLM calls inside the handler attach
// their generations to this trace via the request context.
//
// Only paths matching shouldTrace are traced — static assets, health checks
// and polling endpoints are noisy and uninteresting.
func GinMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		mgr := GetManager()
		if !mgr.Enabled() || !shouldTrace(c) {
			c.Next()
			return
		}

		ctx := c.Request.Context()
		userID := extractUserID(ctx)
		sessionID := extractSessionID(c)

		opts := TraceOptions{
			Name:      c.Request.Method + " " + c.FullPath(),
			UserID:    userID,
			SessionID: sessionID,
			Metadata: map[string]interface{}{
				"http.method": c.Request.Method,
				"http.path":   c.FullPath(),
				"http.query":  c.Request.URL.RawQuery,
			},
			Tags: []string{"http", strings.ToLower(c.Request.Method)},
		}
		if rid, ok := types.RequestIDFromContext(ctx); ok {
			opts.Metadata["request_id"] = rid
		}

		newCtx, trace := mgr.StartTrace(ctx, opts)
		c.Request = c.Request.WithContext(newCtx)

		c.Next()

		trace.Finish(map[string]interface{}{
			"status": c.Writer.Status(),
		}, map[string]interface{}{
			"http.status_code": c.Writer.Status(),
			"response.size":    c.Writer.Size(),
		})
	}
}

// shouldTrace restricts tracing to chat / search / agent / evaluation
// endpoints where LLMs run. Everything else (auth, list, config, static…)
// is skipped to keep the Langfuse dashboard signal-to-noise high.
func shouldTrace(c *gin.Context) bool {
	path := c.FullPath()
	if path == "" {
		return false
	}
	switch {
	case strings.HasPrefix(path, "/api/v1/knowledge-chat"),
		strings.HasPrefix(path, "/api/v1/agent-chat"),
		strings.HasPrefix(path, "/api/v1/knowledge-search"),
		strings.HasPrefix(path, "/api/v1/sessions") && strings.Contains(path, "generate_title"),
		strings.HasPrefix(path, "/api/v1/initialization/remote/check"),
		strings.HasPrefix(path, "/api/v1/initialization/embedding/test"),
		strings.HasPrefix(path, "/api/v1/evaluation"):
		return true
	}
	return false
}

func extractUserID(ctx context.Context) string {
	if v, ok := ctx.Value(types.UserIDContextKey).(string); ok && v != "" {
		return v
	}
	if v, ok := ctx.Value(types.TenantIDContextKey).(uint64); ok && v != 0 {
		return "tenant:" + strconv.FormatUint(v, 10)
	}
	return ""
}

func extractSessionID(c *gin.Context) string {
	if v := c.Param("session_id"); v != "" {
		return v
	}
	if v := c.Param("id"); v != "" && strings.Contains(c.FullPath(), "/sessions/") {
		return v
	}
	return ""
}
