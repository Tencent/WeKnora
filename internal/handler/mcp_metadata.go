package handler

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// GetMCPMetadata reads the stored directory without connecting upstream.
func (h *MCPServiceHandler) GetMCPMetadata(c *gin.Context) { h.mcpMetadata(c, false) }

// RefreshMCPMetadata explicitly synchronizes a complete directory.
func (h *MCPServiceHandler) RefreshMCPMetadata(c *gin.Context) { h.mcpMetadata(c, true) }

func (h *MCPServiceHandler) mcpMetadata(c *gin.Context, refresh bool) {
	tenant := c.GetUint64(types.TenantIDContextKey.String())
	if tenant == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Workspace ID cannot be empty"})
		return
	}
	svc, ok := h.mcpServiceService.(interfaces.MCPMetadataService)
	if !ok {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "MCP metadata storage is unavailable"})
		return
	}
	var snapshot *types.MCPMetadata
	var err error
	if refresh {
		snapshot, err = svc.RefreshMCPMetadata(c.Request.Context(), tenant, c.Param("id"))
	} else {
		snapshot, err = svc.GetMCPMetadata(c.Request.Context(), tenant, c.Param("id"))
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"message": err.Error()}})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": snapshot})
}
