package handler

import (
	"net/http"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/storageallowlist"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type StorageBackendHandler struct {
	repo    interfaces.StorageBackendRepository
	service interfaces.StorageBackendService
}

func NewStorageBackendHandler(repo interfaces.StorageBackendRepository, service interfaces.StorageBackendService) *StorageBackendHandler {
	return &StorageBackendHandler{repo: repo, service: service}
}

type storageBackendRequest struct {
	Name     string                     `json:"name" binding:"required"`
	Provider string                     `json:"provider" binding:"required"`
	Config   types.StorageBackendConfig `json:"config"`
	Status   string                     `json:"status,omitempty"`
}

func storageTenantID(c *gin.Context) uint64 { return c.GetUint64(types.TenantIDContextKey.String()) }

func (h *StorageBackendHandler) List(c *gin.Context) {
	tenantID := storageTenantID(c)
	backends, err := h.repo.List(c.Request.Context(), tenantID)
	if err != nil {
		c.Error(err)
		return
	}
	result := make([]types.StorageBackend, 0, len(backends))
	for _, backend := range backends {
		result = append(result, types.NewStorageBackendResponse(backend))
	}
	tenant, _ := types.TenantInfoFromContext(c.Request.Context())
	var defaultID *string
	if tenant != nil {
		defaultID = tenant.DefaultStorageBackendID
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result, "default_storage_backend_id": defaultID})
}

func (h *StorageBackendHandler) Get(c *gin.Context) {
	backend, err := h.repo.GetByID(c.Request.Context(), storageTenantID(c), c.Param("id"))
	if err != nil {
		c.Error(err)
		return
	}
	if backend == nil {
		c.Error(apperrors.NewNotFoundError("storage backend not found"))
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": types.NewStorageBackendResponse(backend)})
}

func (h *StorageBackendHandler) Create(c *gin.Context) {
	var req storageBackendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	backend := &types.StorageBackend{TenantID: storageTenantID(c), Name: req.Name, Provider: req.Provider, Config: req.Config, Status: req.Status}
	if err := h.service.Create(c.Request.Context(), backend); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": types.NewStorageBackendResponse(backend)})
}

func (h *StorageBackendHandler) Update(c *gin.Context) {
	var req storageBackendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	backend := &types.StorageBackend{ID: c.Param("id"), TenantID: storageTenantID(c), Name: req.Name, Provider: req.Provider, Config: req.Config, Status: req.Status}
	if err := h.service.Update(c.Request.Context(), backend); err != nil {
		c.Error(err)
		return
	}
	updated, err := h.repo.GetByID(c.Request.Context(), backend.TenantID, backend.ID)
	if err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": types.NewStorageBackendResponse(updated)})
}

func (h *StorageBackendHandler) Delete(c *gin.Context) {
	if err := h.service.Delete(c.Request.Context(), storageTenantID(c), c.Param("id")); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *StorageBackendHandler) SetDefault(c *gin.Context) {
	if err := h.service.SetDefault(c.Request.Context(), storageTenantID(c), c.Param("id")); err != nil {
		c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *StorageBackendHandler) TestRaw(c *gin.Context) {
	var req storageBackendRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError(err.Error()))
		return
	}
	backend := &types.StorageBackend{TenantID: storageTenantID(c), Name: req.Name, Provider: req.Provider, Config: req.Config}
	if err := backend.Validate(); err != nil {
		c.Error(err)
		return
	}
	if err := h.service.Test(c.Request.Context(), backend); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *StorageBackendHandler) TestByID(c *gin.Context) {
	backend, err := h.repo.GetByID(c.Request.Context(), storageTenantID(c), c.Param("id"))
	if err != nil {
		c.Error(err)
		return
	}
	if backend == nil {
		c.Error(apperrors.NewNotFoundError("storage backend not found"))
		return
	}
	if err := h.service.Test(c.Request.Context(), backend); err != nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *StorageBackendHandler) Types(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": storageallowlist.AllowedList()})
}
