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

// shouldTrace restricts tracing to endpoints where LLM work (or the asynq
// jobs that will run LLM work) originates. Everything else — auth, list,
// config fetches, static assets, health checks — is skipped to keep the
// Langfuse dashboard's signal-to-noise ratio high.
//
// The list below is grouped by purpose:
//   - online inference: knowledge-chat / agent-chat / knowledge-search /
//     generate_title are the existing chat/retrieval surface.
//   - ingestion: POST on knowledge-bases/:id/knowledge (file / url / manual)
//     and reparse / move / copy all kick off asynq jobs that later run
//     embedding/VLM/chat calls. Tracing the HTTP side means the Langfuse UI
//     shows a parent trace whose children are the worker spans.
//   - batch ops: FAQ import + knowledge batch delete also enqueue jobs.
//   - model/setup diagnostics: initialization endpoints exercise live
//     models; evaluation runs arbitrary chat pipelines.
//   - wiki: auto-fix kicks off wiki ingest, which calls embedding.
//
// Read-only listing / GET endpoints are deliberately excluded; they never
// trigger LLM work and would only add noise.
func shouldTrace(c *gin.Context) bool {
	path := c.FullPath()
	if path == "" {
		return false
	}
	method := c.Request.Method
	// Online inference
	switch {
	case strings.HasPrefix(path, "/api/v1/knowledge-chat"),
		strings.HasPrefix(path, "/api/v1/agent-chat"),
		strings.HasPrefix(path, "/api/v1/knowledge-search"),
		strings.HasPrefix(path, "/api/v1/sessions") && strings.Contains(path, "generate_title"),
		strings.HasPrefix(path, "/api/v1/initialization/remote/check"),
		strings.HasPrefix(path, "/api/v1/initialization/embedding/test"),
		strings.HasPrefix(path, "/api/v1/initialization/rerank/check"),
		strings.HasPrefix(path, "/api/v1/initialization/asr/check"),
		strings.HasPrefix(path, "/api/v1/initialization/multimodal/test"),
		strings.HasPrefix(path, "/api/v1/initialization/extract/"),
		strings.HasPrefix(path, "/api/v1/evaluation"):
		return true
	}
	// Ingestion (all POST/PUT that enqueue LLM-backed async work)
	if method == "POST" || method == "PUT" {
		switch {
		// Per-knowledge-base ingestion surface.
		case strings.Contains(path, "/knowledge-bases/") && strings.Contains(path, "/knowledge/"):
			return true
		// Knowledge-level mutations that trigger re-processing.
		case strings.HasPrefix(path, "/api/v1/knowledge/") &&
			(strings.HasSuffix(path, "/reparse") ||
				strings.HasSuffix(path, "/move") ||
				strings.Contains(path, "/manual/")):
			return true
		// Knowledge base copy (clones an entire KB, fanning out documents).
		case path == "/api/v1/knowledge-bases/copy":
			return true
		// FAQ bulk import.
		case strings.Contains(path, "/faq/entries") ||
			strings.Contains(path, "/faq/entry") ||
			strings.Contains(path, "/faq/import"):
			return true
		// Wiki auto-fix enqueues wiki ingest.
		case strings.Contains(path, "/wiki/auto-fix") ||
			strings.Contains(path, "/wiki/rebuild-links"):
			return true
		// Chunk-level mutations that rerun embeddings on update.
		case strings.HasPrefix(path, "/api/v1/chunks/") && method == "PUT":
			return true
		// Manual data source sync triggers asynq sync.
		case strings.Contains(path, "/datasource/") && strings.HasSuffix(path, "/sync"):
			return true
		}
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
