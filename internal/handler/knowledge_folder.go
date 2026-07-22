package handler

import (
	stderrors "errors"
	"net/http"

	"github.com/Tencent/WeKnora/internal/application/service"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// KnowledgeFolderHandler handles knowledge base folder operations.
// KB access middleware supplies the effective tenant through the request context.
type KnowledgeFolderHandler struct {
	service interfaces.KnowledgeFolderService
}

// NewKnowledgeFolderHandler creates a knowledge folder handler.
func NewKnowledgeFolderHandler(
	service interfaces.KnowledgeFolderService,
) *KnowledgeFolderHandler {
	return &KnowledgeFolderHandler{service: service}
}

// ListFolders lists direct child folders.
func (h *KnowledgeFolderHandler) ListFolders(c *gin.Context) {
	ctx := c.Request.Context()
	page, pageSize, ok := parseListPagination(c)
	if !ok {
		return
	}

	result, err := h.service.ListFolders(
		ctx,
		c.Param("id"),
		c.Query("parent_id"),
		&types.Pagination{Page: page, PageSize: pageSize},
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// CreateFolder creates a folder.
func (h *KnowledgeFolderHandler) CreateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.KnowledgeFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}

	folder, err := h.service.CreateFolder(ctx, c.Param("id"), &req)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": folder})
}

// GetFolder gets a folder and its direct navigation statistics.
func (h *KnowledgeFolderHandler) GetFolder(c *gin.Context) {
	folder, err := h.service.GetFolder(
		c.Request.Context(),
		c.Param("id"),
		c.Param("folder_id"),
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// UpdateFolder renames, reorders, or moves a folder.
func (h *KnowledgeFolderHandler) UpdateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	var req types.KnowledgeFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(apperrors.NewBadRequestError("请求参数不合法"))
		return
	}

	folder, err := h.service.UpdateFolder(
		ctx,
		c.Param("id"),
		c.Param("folder_id"),
		&req,
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// DeleteFolder deletes an empty folder.
func (h *KnowledgeFolderHandler) DeleteFolder(c *gin.Context) {
	err := h.service.DeleteFolder(
		c.Request.Context(),
		c.Param("id"),
		c.Param("folder_id"),
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// GetBreadcrumb returns the ordered folder chain without a virtual root record.
func (h *KnowledgeFolderHandler) GetBreadcrumb(c *gin.Context) {
	folders, err := h.service.GetBreadcrumb(
		c.Request.Context(),
		c.Param("id"),
		c.Param("folder_id"),
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folders})
}

func writeKnowledgeFolderError(c *gin.Context, err error) {
	var appErr error
	switch {
	case stderrors.Is(err, service.ErrKnowledgeFolderInvalidArgument),
		stderrors.Is(err, service.ErrKnowledgeFolderInvalidName):
		appErr = apperrors.NewBadRequestError("请求参数不合法")
	case stderrors.Is(err, service.ErrKnowledgeFolderNotFound):
		appErr = apperrors.NewNotFoundError("目录不存在")
	case stderrors.Is(err, service.ErrKnowledgeFolderConflict):
		appErr = apperrors.NewConflictError("同级目录名称已存在")
	case stderrors.Is(err, service.ErrKnowledgeFolderNotEmpty):
		appErr = apperrors.NewConflictError("目录不为空")
	case stderrors.Is(err, service.ErrKnowledgeFolderCycle):
		appErr = apperrors.NewConflictError("不能将目录移动到自身或其子目录")
	case stderrors.Is(err, service.ErrKnowledgeFolderDepthExceeded):
		appErr = apperrors.NewConflictError("目录层级超过限制")
	default:
		logger.ErrorWithFields(c.Request.Context(), err, map[string]interface{}{
			"knowledge_base_id": secutils.SanitizeForLog(c.Param("id")),
			"folder_id":         secutils.SanitizeForLog(c.Param("folder_id")),
		})
		appErr = apperrors.NewInternalServerError("目录操作失败")
	}
	c.Error(appErr)
}
