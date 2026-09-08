package router

import (
	"context"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/gin-gonic/gin"
)

// Workbench responses include single-use tickets, audit command summaries and
// user files. Do not pass them through the general body-capturing logger.
func workbenchAwareRequestLogger() gin.HandlerFunc {
	ordinary := middleware.Logger()
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path != "/api/v1/sandbox-terminal" && !(strings.HasPrefix(path, "/api/v1/sessions/") && strings.Contains(path, "/sandbox/")) {
			ordinary(c)
			return
		}
		start := time.Now()
		c.Next()
		logger.GetLogger(c).WithFields(map[string]any{
			"method": c.Request.Method, "route": c.FullPath(),
			"status_code": c.Writer.Status(), "latency": time.Since(start).String(),
		}).Info("workbench request")
	}
}

// RegisterWorkbenchPublicRoutes must run before global Auth. The socket uses
// its own bounded, first-frame ticket authentication, not a noAuth exception.
func RegisterWorkbenchPublicRoutes(r *gin.Engine, h *handler.WorkbenchHandler) {
	if h != nil {
		r.GET("/api/v1/sandbox-terminal", h.Terminal)
	}
}

func RegisterWorkbenchRoutes(r *gin.RouterGroup, h *handler.WorkbenchHandler) {
	if h == nil {
		return
	}
	// No API-key capability is declared: this surface is strictly web-only.
	group := r.Group("/sessions", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Minute)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	// Existing Gin method trees use :session_id for POST and :id for GET/DELETE.
	group.GET("/:id/sandbox/workbench", h.Status)
	group.POST("/:session_id/sandbox/workbench", h.Bind)
	group.POST("/:session_id/sandbox/terminal-ticket", h.Ticket)
	group.GET("/:id/sandbox/files", h.ListFiles)
	group.GET("/:id/sandbox/files/download", h.Download)
	group.POST("/:session_id/sandbox/files", h.Upload)
	group.POST("/:session_id/sandbox/directories", h.Directory)
	group.PATCH("/:id/sandbox/files", h.Rename)
	group.DELETE("/:id/sandbox/files", h.Remove)
	group.GET("/:id/sandbox/audit", h.Audit)
}
