package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterMemoryRoutes registers the personal long-term memory endpoints.
//
// There is no admin surface here and no subject parameter anywhere in the
// paths: the memory space is derived from the caller's principal, so these
// routes can only ever read or write the caller's own memories. That is why
// Viewer is the only role gate they need. An API key must be full-access,
// since a memory space belongs to a person and a scoped integration key has
// no business inheriting one.
func RegisterMemoryRoutes(r *gin.RouterGroup, memoryHandler *handler.MemoryHandler, g *rbacGuards) {
	if memoryHandler == nil {
		return
	}
	memoryGroup := g.apiKeyGroup(r.Group("/memory", g.Viewer()), apiKeyFullAccess())
	{
		memoryGroup.GET("/settings", memoryHandler.GetSettings)
		memoryGroup.PUT("/settings", memoryHandler.UpdateSettings)
		memoryGroup.GET("/profile", memoryHandler.GetProfile)
		memoryGroup.PUT("/profile", memoryHandler.SaveProfile)
		memoryGroup.DELETE("/profile", memoryHandler.DeleteProfile)
		memoryGroup.GET("/episodes", memoryHandler.ListEpisodes)
		memoryGroup.GET("/episodes/:id", memoryHandler.GetEpisode)
		memoryGroup.DELETE("/episodes/:id", memoryHandler.DeleteEpisode)
		memoryGroup.GET("/notes", memoryHandler.ListNotes)
		memoryGroup.POST("/notes", memoryHandler.CreateNote)
		memoryGroup.DELETE("/notes/:id", memoryHandler.DeleteNote)
		memoryGroup.DELETE("/all", memoryHandler.Clear)
		memoryGroup.GET("/export", memoryHandler.Export)
		memoryGroup.POST("/consolidate", memoryHandler.Consolidate)
	}
}
