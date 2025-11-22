package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	service "github.com/tencent/weknora/features/virtualkb/internal/service/interfaces"
	"github.com/tencent/weknora/features/virtualkb/internal/types"
)

// VirtualKBHandler exposes endpoints for virtual knowledge bases.
type VirtualKBHandler struct {
	service service.VirtualKBService
}

// NewVirtualKBHandler creates a new handler instance.
func NewVirtualKBHandler(service service.VirtualKBService) *VirtualKBHandler {
	return &VirtualKBHandler{service: service}
}

// RegisterRoutes wires virtual knowledge base routes.
func (h *VirtualKBHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("", h.list)
	rg.POST("", h.create)
	rg.GET(":id", h.get)
	rg.PUT(":id", h.update)
	rg.DELETE(":id", h.delete)
}

func (h *VirtualKBHandler) create(c *gin.Context) {
	var req types.VirtualKB
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid virtual knowledge base payload", err)
		return
	}

	if err := h.service.Create(c.Request.Context(), &req); err != nil {
		respondBadRequest(c, "failed to create virtual knowledge base", err)
		return
	}
	respondSuccess(c, http.StatusCreated, req)
}

func (h *VirtualKBHandler) update(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid virtual knowledge base id", err)
		return
	}

	var req types.VirtualKB
	if err := c.ShouldBindJSON(&req); err != nil {
		respondBadRequest(c, "invalid virtual knowledge base payload", err)
		return
	}
	req.ID = id

	if err := h.service.Update(c.Request.Context(), &req); err != nil {
		respondBadRequest(c, "failed to update virtual knowledge base", err)
		return
	}
	respondSuccess(c, http.StatusOK, req)
}

func (h *VirtualKBHandler) delete(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid virtual knowledge base id", err)
		return
	}

	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusNoContent, nil)
}

func (h *VirtualKBHandler) get(c *gin.Context) {
	id, err := parseIDParam(c.Param("id"))
	if err != nil {
		respondBadRequest(c, "invalid virtual knowledge base id", err)
		return
	}

	vkb, err := h.service.GetByID(c.Request.Context(), id)
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, vkb)
}

func (h *VirtualKBHandler) list(c *gin.Context) {
	vkbs, err := h.service.List(c.Request.Context())
	if err != nil {
		respondInternal(c, err)
		return
	}
	respondSuccess(c, http.StatusOK, vkbs)
}
