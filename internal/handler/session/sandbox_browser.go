package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/gin-gonic/gin"
)

// SandboxBrowserCommand handles authenticated preview and manual browser actions.
func (h *Handler) SandboxBrowserCommand(c *gin.Context) {
	ctx := c.Request.Context()
	sessionID := c.Param("session_id")
	if _, err := h.sessionService.GetOwnedSession(ctx, sessionID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	if h.terminalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "sandbox service unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64*1024)
	var command service.BrowserCommand
	if err := c.ShouldBindJSON(&command); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid browser command"})
		return
	}
	if err := command.Validate(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	timeout := 45 * time.Second
	if command.Action == "start" {
		timeout = 120 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var result json.RawMessage
	var err error
	if command.Action == "start" {
		configID := h.terminalProvisionConfigID(ctx, c, true)
		result, err = h.terminalService.StartSessionBrowser(ctx, sessionID, configID, command)
	} else {
		result, err = h.terminalService.BrowserCommand(ctx, sessionID, command)
	}
	c.Header("Cache-Control", "no-store")
	if errors.Is(err, sandbox.ErrSandboxPaused) || errors.Is(err, sandbox.ErrNoLiveSessionSandbox) {
		state := "not_bound"
		if errors.Is(err, sandbox.ErrSandboxPaused) {
			state = "paused"
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"ok": false, "state": state}})
		return
	}
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
