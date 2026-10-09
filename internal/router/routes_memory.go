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
		memoryGroup.GET("/items", memoryHandler.ListItems)
		memoryGroup.POST("/items", memoryHandler.CreateItem)
		memoryGroup.DELETE("/items", memoryHandler.Clear)
		memoryGroup.PUT("/items/:id", memoryHandler.UpdateItem)
		memoryGroup.DELETE("/items/:id", memoryHandler.DeleteItem)
		memoryGroup.POST("/items/:id/confirm", memoryHandler.ConfirmItem)
		memoryGroup.POST("/items/:id/reject", memoryHandler.RejectItem)
		memoryGroup.GET("/topics", memoryHandler.ListTopics)
		memoryGroup.DELETE("/topics/:id", memoryHandler.DeleteTopic)
		memoryGroup.POST("/topics/:id/promote", memoryHandler.PromoteTopic)
		memoryGroup.GET("/documents", memoryHandler.ListDocuments)
		memoryGroup.DELETE("/documents/:id", memoryHandler.DeleteDocument)
		memoryGroup.GET("/export", memoryHandler.Export)
		memoryGroup.POST("/consolidate", memoryHandler.Consolidate)
	}
}

// RegisterMasteryRoutes registers the personal answer-feedback endpoints and
// the KB-scoped knowledge-guidance endpoints. The latter deliberately carry the
// KB in the path so the shared KBAccessRead guard can run before any handler
// touches wiki pages or returns profile metadata.
func RegisterMasteryRoutes(r *gin.RouterGroup, masteryHandler *handler.MasteryHandler, g *rbacGuards) {
	if masteryHandler == nil {
		return
	}

	// Answer likes and profile deletion are scoped only to the caller's own
	// memory subject; they do not read a client-selected knowledge base.
	personal := g.apiKeyGroup(r.Group("/memory", g.Viewer()), apiKeyFullAccess())
	{
		personal.POST("/answer-like", masteryHandler.RecordAnswerLike)
		personal.DELETE("/answer-like/:message_id", masteryHandler.CancelAnswerLike)
		personal.DELETE("/mastery", masteryHandler.DeleteMastery)
	}

	// All endpoints below accept or return KB content. Keep the KB identifier in
	// the route so authorization is performed before GetPageBySlug,
	// ListPagesCursor, or the export renderer can observe anything about it.
	kbMastery := g.apiKeyGroup(r.Group("/knowledge-bases/:kb_id/memory"), apiKeyFullAccess())
	{
		kbMastery.POST("/page-view", g.Viewer(), g.KBAccessRead("kb_id"), masteryHandler.RecordPageView)
		kbMastery.POST("/exposure", g.Viewer(), g.KBAccessRead("kb_id"), masteryHandler.RecordExposure)
		kbMastery.POST("/exposure/click", g.Viewer(), g.KBAccessRead("kb_id"), masteryHandler.ExposureClick)
		kbMastery.GET("/mastery", g.Viewer(), g.KBAccessRead("kb_id"), masteryHandler.Profile)
		kbMastery.GET("/mastery/export", g.Viewer(), g.KBAccessRead("kb_id"), masteryHandler.ExportProfile)
	}
}
