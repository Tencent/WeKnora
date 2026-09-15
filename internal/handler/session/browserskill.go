package session

import (
	"context"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func browserSkillScope(ctx context.Context) browserskill.Scope {
	tenant, _ := types.TenantIDFromContext(ctx)
	user, _ := types.UserIDFromContext(ctx)
	return browserskill.Scope{Tenant: tenant, User: user}
}

// BrowserSkillConnection exposes task status and user controls for an owned conversation.
func (h *Handler) BrowserSkillConnection(c *gin.Context) {
	id := c.Param("session_id")
	if id == "" {
		id = c.Param("id")
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	if _, err := h.sessionService.GetOwnedSession(ctx, id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
		return
	}
	c.Header("Cache-Control", "no-store")
	scope := browserSkillScope(ctx)
	if c.Request.Method == http.MethodGet {
		status, err := h.browserSkill.GetStatus(ctx, scope, id)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
		return
	}
	if !h.browserSkill.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "local browser is unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Action string `json:"action"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid browser action"})
		return
	}
	var err error
	switch input.Action {
	case "preview":
		var frame []byte
		frame, err = h.browserSkill.Preview(ctx, scope, id)
		if err == nil {
			c.Data(http.StatusOK, "application/json", append(append([]byte(`{"success":true,"data":`), frame...), '}'))
			return
		}
	case "focus":
		err = h.browserSkill.Focus(ctx, scope, id)
	case "select", "start", "resume", "pause", "stop":
		err = h.browserSkill.Control(ctx, scope, id, input.Action)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid browser action"})
		return
	}
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	status, err := h.browserSkill.GetStatus(ctx, scope, id)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
}

// BrowserSkillExtension upgrades a device-authenticated extension connection.
func (h *Handler) BrowserSkillExtension(c *gin.Context) {
	h.browserSkill.ServeHTTP(c.Writer, c.Request)
}

// BrowserSkillAccount manages this member's shared browser connection independently
// of any conversation. Normal authentication and tenant membership apply upstream.
func (h *Handler) BrowserSkillAccount(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	scope := browserSkillScope(ctx)
	if scope.User == "" || scope.Tenant == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return
	}
	c.Header("Cache-Control", "no-store")
	if c.Request.Method == http.MethodGet {
		status, err := h.browserSkill.Account(ctx, scope)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "browser connection status unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
		return
	}
	if !h.browserSkill.Enabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "local browser is unavailable"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var input struct {
		Action string `json:"action"`
		Origin string `json:"origin"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid browser action"})
		return
	}
	switch input.Action {
	case "pair":
		link, err := h.browserSkill.Pair(ctx, scope, input.Origin)
		if err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"pairing_link": link}})
	case "revoke":
		if err := h.browserSkill.Revoke(ctx, scope); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not revoke browser authorization"})
			return
		}
		status, err := h.browserSkill.Account(ctx, scope)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "browser connection status unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": status})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account browser action"})
	}
}

// BrowserSkillAuthorize exchanges one-use pairing and renewable device credentials.
func (h *Handler) BrowserSkillAuthorize(c *gin.Context) {
	h.browserSkill.AuthorizeHTTP(c.Writer, c.Request)
}

// BrowserSkillInternal forwards signed requests to the owning node's browser gateway.
func (h *Handler) BrowserSkillInternal(c *gin.Context) {
	h.browserSkill.InternalHTTP(c.Writer, c.Request)
}

// BrowserSkillDownload serves the extension archive for installation.
func (h *Handler) BrowserSkillDownload(c *gin.Context) {
	h.browserSkill.DownloadExtension(c.Writer, c.Request)
}
