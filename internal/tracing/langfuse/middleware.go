package langfuse

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/propagation"
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
		// Extract a W3C traceparent from the incoming request so the WeKnora
		// trace inherits the upstream caller's trace id. This is what lets a
		// sop3 pipeline run (identified by its W3C trace_id) and the WeKnora
		// agent-chat call it triggers land under the same trace in LiteFuse.
		// When no traceparent is present (human UI calls, other clients) the
		// root span starts a fresh trace as before.
		ctx = propagator.Extract(ctx, propagation.HeaderCarrier(c.Request.Header))
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
		mergeMetadataHeader(opts.Metadata, c.Request.Header.Get(langfuseMetadataHeader))

		newCtx, trace := mgr.StartTrace(ctx, opts)
		c.Request = c.Request.WithContext(newCtx)

		c.Next()

		trace.Finish(map[string]interface{}{
			"status":        c.Writer.Status(),
			"response.size": c.Writer.Size(),
		}, nil)
	}
}

// langfuseMetadataHeader is the request header carrying caller-defined JSON
// key/value pairs that are merged into the Langfuse trace metadata. For
// example `X-Langfuse-Metadata: {"ticket_id":"T-123","biz_line":"ops"}` lets
// callers tag traces with their own business labels for filtering in the
// Langfuse UI. Values are optional and additive: a malformed body or an empty
// header is ignored and never fails the request.
const langfuseMetadataHeader = "X-Langfuse-Metadata"

// mergeMetadataHeader parses the given X-Langfuse-Metadata header value as a
// JSON object and merges its keys into md. Built-in correlation fields set by
// GinMiddleware (http.method, http.path, http.query, request_id, ...) always
// win on conflict so callers cannot clobber them. Non-object JSON, malformed
// JSON and the empty header are silently ignored.
func mergeMetadataHeader(md map[string]interface{}, raw string) {
	if md == nil || strings.TrimSpace(raw) == "" {
		return
	}
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &extra); err != nil {
		return
	}
	// Only apply keys that are not already present so the middleware's own
	// correlation fields are preserved.
	for k, v := range extra {
		if _, exists := md[k]; !exists {
			md[k] = v
		}
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
