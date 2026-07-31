package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// KnowledgeFolderHandler serves document knowledge folders.
type KnowledgeFolderHandler struct {
	folderService interfaces.KnowledgeFolderService
}

// NewKnowledgeFolderHandler creates a new KnowledgeFolderHandler.
func NewKnowledgeFolderHandler(folderService interfaces.KnowledgeFolderService) *KnowledgeFolderHandler {
	return &KnowledgeFolderHandler{folderService: folderService}
}

// CreateFolderRequest is the body for creating a folder.
type CreateFolderRequest struct {
	Name     string `json:"name" binding:"required"`
	ParentID string `json:"parent_id"`
}

// UpdateFolderRequest is the body for renaming and/or moving a folder.
// MoveParent distinguishes "move to the root" (move_parent=true, parent_id="")
// from "keep the current parent" (move_parent=false).
type UpdateFolderRequest struct {
	Name       string `json:"name"`
	ParentID   string `json:"parent_id"`
	MoveParent bool   `json:"move_parent"`
}

// MoveKnowledgeToFolderRequest is the body for batch-filing documents.
type MoveKnowledgeToFolderRequest struct {
	KnowledgeIDs []string `json:"knowledge_ids" binding:"required"`
	FolderID     string   `json:"folder_id"`
}

// ListFolders godoc
// @Summary      获取文件夹列表
// @Description  获取某个父文件夹的直接子文件夹（parent_id 为空表示根目录），含文档数量与是否有子文件夹；all=true 时返回整棵树的平铺列表
// @Tags         知识管理
// @Produce      json
// @Param        id         path   string  true   "知识库ID"
// @Param        parent_id  query  string  false  "父文件夹ID（空 = 根目录）"
// @Param        all        query  bool    false  "返回全量平铺列表（选择器用）"
// @Success      200  {object}  map[string]interface{}  "文件夹列表"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [get]
func (h *KnowledgeFolderHandler) ListFolders(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	if c.Query("all") == "true" {
		folders, err := h.folderService.ListAllFolders(ctx, kbID)
		if err != nil {
			logger.ErrorWithFields(ctx, err, nil)
			c.Error(err)
			return
		}
		if folders == nil {
			folders = []*types.KnowledgeFolder{}
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": folders})
		return
	}

	parentID := strings.TrimSpace(c.Query("parent_id"))
	nodes, err := h.folderService.ListFolders(ctx, kbID, parentID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	if nodes == nil {
		nodes = []*types.KnowledgeFolderNode{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": nodes, "parent_id": parentID})
}

// CreateFolder godoc
// @Summary      创建文件夹
// @Description  在 parent_id 下创建一个新的空文件夹
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id      path  string               true  "知识库ID"
// @Param        folder  body  CreateFolderRequest  true  "文件夹信息"
// @Success      200  {object}  map[string]interface{}  "创建的文件夹"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders [post]
func (h *KnowledgeFolderHandler) CreateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var req CreateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request body").WithDetails(err.Error()))
		return
	}
	tenantID := types.MustTenantIDFromContext(ctx)
	folder, err := h.folderService.CreateFolder(ctx, kbID, tenantID, strings.TrimSpace(req.ParentID), req.Name)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// UpdateFolder godoc
// @Summary      重命名/移动文件夹
// @Description  重命名和/或改变父级；整个子树的路径与深度会重新计算。移动到根目录需 move_parent=true 且 parent_id 为空
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id         path  string               true  "知识库ID"
// @Param        folder_id  path  string               true  "文件夹ID"
// @Param        folder     body  UpdateFolderRequest  true  "更新内容"
// @Success      200  {object}  map[string]interface{}  "更新后的文件夹"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeFolderHandler) UpdateFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.Error(errors.NewBadRequestError("Folder ID is required"))
		return
	}

	var req UpdateFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request body").WithDetails(err.Error()))
		return
	}
	folder, err := h.folderService.RenameOrMoveFolder(
		ctx, kbID, folderID, req.Name, strings.TrimSpace(req.ParentID), req.MoveParent)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// DeleteFolder godoc
// @Summary      删除文件夹
// @Description  删除文件夹。默认仅允许删除空文件夹；mode=promote 时先将其中的文档与子文件夹上移到父级（不会删除任何文档）
// @Tags         知识管理
// @Produce      json
// @Param        id         path   string  true   "知识库ID"
// @Param        folder_id  path   string  true   "文件夹ID"
// @Param        mode       query  string  false  "promote = 内容上移后删除"
// @Success      200  {object}  map[string]interface{}
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeFolderHandler) DeleteFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.Error(errors.NewBadRequestError("Folder ID is required"))
		return
	}
	promote := c.Query("mode") == "promote"
	if err := h.folderService.DeleteFolder(ctx, kbID, folderID, promote); err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// OrganizeByPath godoc
// @Summary      按上传路径整理文件夹
// @Description  将根目录下 file_name 携带相对路径（文件夹上传产生，如 "reports/2026/q1.pdf"）的存量文档按路径建立文件夹并归位。file_name 本身保持不变；操作幂等，已归位的文档不再匹配
// @Tags         知识管理
// @Produce      json
// @Param        id  path  string  true  "知识库ID"
// @Success      200  {object}  map[string]interface{}  "organized / folders_created 统计"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/folders/organize-by-path [post]
func (h *KnowledgeFolderHandler) OrganizeByPath(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))
	tenantID := types.MustTenantIDFromContext(ctx)

	organized, foldersCreated, err := h.folderService.OrganizeByPath(ctx, kbID, tenantID)
	if err != nil {
		logger.ErrorWithFields(ctx, err, nil)
		c.Error(err)
		return
	}
	logger.Infof(ctx, "Organized %d documents into %d new folders, kb_id=%s",
		organized, foldersCreated, kbID)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    gin.H{"organized": organized, "folders_created": foldersCreated},
	})
}

// MoveKnowledgeToFolder godoc
// @Summary      移动文档到文件夹
// @Description  将知识库内的一批文档放入指定文件夹（folder_id 为空 = 根目录）
// @Tags         知识管理
// @Accept       json
// @Produce      json
// @Param        id    path  string                        true  "知识库ID"
// @Param        move  body  MoveKnowledgeToFolderRequest  true  "文档与目标文件夹"
// @Success      200  {object}  map[string]interface{}  "moved 数量"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /knowledge-bases/{id}/knowledge/move-to-folder [post]
func (h *KnowledgeFolderHandler) MoveKnowledgeToFolder(c *gin.Context) {
	ctx := c.Request.Context()
	kbID := secutils.SanitizeForLog(c.Param("id"))

	var req MoveKnowledgeToFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("Invalid request body").WithDetails(err.Error()))
		return
	}
	if len(req.KnowledgeIDs) == 0 {
		c.Error(errors.NewBadRequestError("knowledge_ids is required"))
		return
	}
	moved, err := h.folderService.MoveKnowledgeToFolder(ctx, kbID, req.KnowledgeIDs, req.FolderID)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"moved": moved}})
}

// writeKnowledgeFolderError maps folder service errors to HTTP status codes.
func writeKnowledgeFolderError(c *gin.Context, err error) {
	switch {
	case stderrors.Is(err, repository.ErrKnowledgeFolderNotFound):
		c.Error(errors.NewNotFoundError(err.Error()))
	case stderrors.Is(err, repository.ErrKnowledgeFolderConflict),
		stderrors.Is(err, repository.ErrKnowledgeFolderNotEmpty):
		c.Error(errors.NewConflictError(err.Error()))
	default:
		c.Error(errors.NewBadRequestError(err.Error()))
	}
}
