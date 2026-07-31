package handler

import (
	goerrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
)

// writeKnowledgeFolderError maps the folder sentinel errors onto HTTP status
// codes. Conflicts (duplicate name, non-empty folder, depth limit) are client
// mistakes the UI can recover from, so they must not surface as a 500 the way a
// generic error path would render them.
func writeKnowledgeFolderError(c *gin.Context, err error) {
	switch {
	case goerrors.Is(err, repository.ErrKnowledgeFolderNotFound):
		c.Error(errors.NewNotFoundError(err.Error()))
	case goerrors.Is(err, repository.ErrKnowledgeFolderConflict),
		goerrors.Is(err, repository.ErrKnowledgeFolderNotEmpty),
		goerrors.Is(err, repository.ErrKnowledgeFolderTooDeep):
		c.Error(errors.NewConflictError(err.Error()))
	default:
		c.Error(errors.NewInternalServerError(err.Error()))
	}
}

// ListKnowledgeFolders godoc
//
//	@Summary		List knowledge base folders
//	@Description	Returns the folder tree of a document knowledge base. By default the whole
//	@Description	tree is returned in a single response so the UI can hydrate without one
//	@Description	request per level; pass recursive=false with parent_id to fetch one level.
//	@Tags			knowledge-folders
//	@Produce		json
//	@Param			id			path		string	true	"Knowledge base ID"
//	@Param			parent_id	query		string	false	"Parent folder ID ('' = root)"
//	@Param			recursive	query		bool	false	"Return the whole tree (default true)"
//	@Success		200			{object}	types.KnowledgeFolderListResponse
//	@Router			/api/v1/knowledge-bases/{id}/folders [get]
func (h *KnowledgeHandler) ListKnowledgeFolders(c *gin.Context) {
	ctx := c.Request.Context()

	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	parentID := c.Query("parent_id")
	// Default to the full tree: folder counts are navigation-sized and one
	// request beats N level-by-level round-trips on a deep hierarchy.
	recursive := c.DefaultQuery("recursive", "true") != "false"

	result, err := h.folderService.ListFolders(ctx, effectiveTenantID, kbID, parentID, recursive)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

// CreateKnowledgeFolder godoc
//
//	@Summary		Create a knowledge base folder
//	@Tags			knowledge-folders
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string								true	"Knowledge base ID"
//	@Param			body	body		types.KnowledgeFolderCreateRequest	true	"Folder to create"
//	@Success		201		{object}	types.KnowledgeFolder
//	@Router			/api/v1/knowledge-bases/{id}/folders [post]
func (h *KnowledgeHandler) CreateKnowledgeFolder(c *gin.Context) {
	ctx := c.Request.Context()

	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	var req types.KnowledgeFolderCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}

	folder, err := h.folderService.CreateFolder(ctx, effectiveTenantID, kbID, req.ParentID, req.Name)
	if err != nil {
		if isFolderValidationError(err) {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		writeKnowledgeFolderError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"success": true, "data": folder})
}

// UpdateKnowledgeFolder godoc
//
//	@Summary		Rename or move a knowledge base folder
//	@Description	The parent is changed only when move_parent is true, so a pure rename cannot
//	@Description	relocate a folder to the root by omitting parent_id.
//	@Tags			knowledge-folders
//	@Accept			json
//	@Produce		json
//	@Param			id			path		string								true	"Knowledge base ID"
//	@Param			folder_id	path		string								true	"Folder ID"
//	@Param			body		body		types.KnowledgeFolderUpdateRequest	true	"Update payload"
//	@Success		200			{object}	types.KnowledgeFolder
//	@Router			/api/v1/knowledge-bases/{id}/folders/{folder_id} [put]
func (h *KnowledgeHandler) UpdateKnowledgeFolder(c *gin.Context) {
	ctx := c.Request.Context()

	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.Error(errors.NewBadRequestError("Folder ID cannot be empty"))
		return
	}

	var req types.KnowledgeFolderUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if strings.TrimSpace(req.Name) == "" && !req.MoveParent {
		c.Error(errors.NewBadRequestError("Provide a new name or set move_parent"))
		return
	}

	folder, err := h.folderService.RenameOrMoveFolder(
		ctx, effectiveTenantID, kbID, folderID, req.Name, req.ParentID, req.MoveParent,
	)
	if err != nil {
		if isFolderValidationError(err) {
			c.Error(errors.NewBadRequestError(err.Error()))
			return
		}
		writeKnowledgeFolderError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": folder})
}

// DeleteKnowledgeFolder godoc
//
//	@Summary		Delete a knowledge base folder
//	@Description	Refuses by default when the folder still holds documents or child folders.
//	@Description	Pass strategy=reparent to delete the subtree and lift its documents to the
//	@Description	parent folder. Documents are never deleted by this endpoint.
//	@Tags			knowledge-folders
//	@Produce		json
//	@Param			id			path	string	true	"Knowledge base ID"
//	@Param			folder_id	path	string	true	"Folder ID"
//	@Param			strategy	query	string	false	"fail (default) or reparent"
//	@Success		200			{object}	map[string]interface{}
//	@Router			/api/v1/knowledge-bases/{id}/folders/{folder_id} [delete]
func (h *KnowledgeHandler) DeleteKnowledgeFolder(c *gin.Context) {
	ctx := c.Request.Context()

	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccess(c)
	if err != nil {
		c.Error(err)
		return
	}

	folderID := secutils.SanitizeForLog(c.Param("folder_id"))
	if folderID == "" {
		c.Error(errors.NewBadRequestError("Folder ID cannot be empty"))
		return
	}

	strategy := c.DefaultQuery("strategy", types.KnowledgeFolderDeleteFail)
	if strategy != types.KnowledgeFolderDeleteFail && strategy != types.KnowledgeFolderDeleteReparent {
		c.Error(errors.NewBadRequestError("strategy must be 'fail' or 'reparent'"))
		return
	}

	if err := h.folderService.DeleteFolder(ctx, effectiveTenantID, kbID, folderID, strategy); err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MoveKnowledgeToFolder godoc
//
//	@Summary		Move documents into a folder
//	@Description	Relocates documents within one knowledge base. folder_id '' moves them back
//	@Description	to the root. This only changes organisation — no re-parsing or re-indexing
//	@Description	is triggered.
//	@Tags			knowledge-folders
//	@Accept			json
//	@Produce		json
//	@Param			body	body		types.KnowledgeMoveToFolderRequest	true	"Move payload"
//	@Success		200		{object}	map[string]interface{}
//	@Router			/api/v1/knowledge/move-to-folder [put]
func (h *KnowledgeHandler) MoveKnowledgeToFolder(c *gin.Context) {
	ctx := c.Request.Context()

	var req types.KnowledgeMoveToFolderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError(err.Error()))
		return
	}
	if len(req.KnowledgeIDs) == 0 {
		c.Error(errors.NewBadRequestError("knowledge_ids cannot be empty"))
		return
	}

	// The knowledge base comes from the body here, so authorise against it
	// explicitly — the route cannot derive it from a path parameter.
	_, kbID, effectiveTenantID, _, err := h.validateKnowledgeBaseAccessWithKBID(c, req.KnowledgeBaseID)
	if err != nil {
		c.Error(err)
		return
	}
	if err := h.requireKBOwnershipOrAdmin(c, kbID); err != nil {
		c.Error(err)
		return
	}

	moved, err := h.folderService.MoveKnowledgeToFolder(
		ctx, effectiveTenantID, kbID, req.KnowledgeIDs, req.FolderID,
	)
	if err != nil {
		writeKnowledgeFolderError(c, err)
		return
	}

	// A short count means some ids did not belong to this knowledge base and
	// were skipped rather than moved across a boundary. Report it so the client
	// can refresh instead of assuming a clean success.
	if moved < int64(len(req.KnowledgeIDs)) {
		logger.Warnf(ctx, "Folder move matched %d of %d requested documents in kb %s",
			moved, len(req.KnowledgeIDs), kbID)
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"moved":     moved,
		"requested": len(req.KnowledgeIDs),
	})
}

// parseFolderIDQuery turns a folder_id query value into the id list the folder
// service expects.
//
// The root bucket has no id of its own, so it has to be spelled somehow in a
// URL: both an empty segment and the literal "root" map onto the "" sentinel.
// Accepting a comma separated list lets the UI show a multi-folder selection
// without inventing a second parameter.
func parseFolderIDQuery(raw string) []string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]struct{}, len(parts))
	ids := make([]string, 0, len(parts))
	for _, part := range parts {
		id := strings.TrimSpace(part)
		if id == "root" {
			id = types.KnowledgeFolderRootID
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// isFolderValidationError reports whether err is a user-input problem raised by
// the service (bad name, self-referential move) rather than a sentinel or an
// infrastructure failure. Those deserve a 400 instead of a 500.
func isFolderValidationError(err error) bool {
	if err == nil {
		return false
	}
	if goerrors.Is(err, repository.ErrKnowledgeFolderNotFound) ||
		goerrors.Is(err, repository.ErrKnowledgeFolderConflict) ||
		goerrors.Is(err, repository.ErrKnowledgeFolderNotEmpty) ||
		goerrors.Is(err, repository.ErrKnowledgeFolderTooDeep) {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"folder name cannot",
		"folder name is reserved",
		"cannot move a folder",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
