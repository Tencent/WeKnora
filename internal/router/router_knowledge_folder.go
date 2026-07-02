package router

import (
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

// RegisterKnowledgeFolderRoutes registers knowledge folder related routes
func RegisterKnowledgeFolderRoutes(r *gin.RouterGroup, handler *handler.KnowledgeFolderHandler, g *rbacGuards) {
	if handler == nil {
		return
	}

	// Folder routes under knowledge base
	// All folder operations require Contributor+ role and KB write access
	folders := r.Group("/knowledge-bases/:id/folders")
	{
		// List folders under a parent (or root if parent_id not provided)
		folders.GET("", g.Viewer(), g.KBAccessRead("id"), handler.ListFolders)

		// Get full folder tree
		folders.GET("/tree", g.Viewer(), g.KBAccessRead("id"), handler.GetFolderTree)

		// Create a new folder
		folders.POST("", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.CreateFolder)

		// Get folder details
		folders.GET("/:folder_id", g.Viewer(), g.KBAccessRead("id"), handler.GetFolder)

		// Update folder properties
		folders.PUT("/:folder_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.UpdateFolder)

		// Delete folder
		folders.DELETE("/:folder_id", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.DeleteFolder)

		// Move folder to new parent
		folders.POST("/:folder_id/move", g.OwnedKBOrAdmin(), g.KBAccessWrite("id"), handler.MoveFolder)

		// Get breadcrumb path
		folders.GET("/:folder_id/breadcrumb", g.Viewer(), g.KBAccessRead("id"), handler.GetBreadcrumb)
	}
}
