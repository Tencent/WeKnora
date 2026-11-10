package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterMemoryRoutes 注册个人记忆接口和仅限 JWT 管理员的租户记忆管理接口。
// 个人接口始终从 principal 派生主体；管理接口使用主体 UUID，且不声明 API Key 策略。
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
	adminGroup := r.Group("/admin/memory/subjects", g.Admin())
	{
		adminGroup.GET("", memoryHandler.AdminListSubjects)
		adminGroup.GET("/:subject_id/items", memoryHandler.AdminListItems)
		adminGroup.POST("/:subject_id/items/:id/confirm", memoryHandler.AdminConfirmItem)
		adminGroup.POST("/:subject_id/items/:id/reject", memoryHandler.AdminRejectItem)
		adminGroup.DELETE("/:subject_id/items", memoryHandler.AdminClear)
		adminGroup.PUT("/:subject_id/settings", memoryHandler.AdminUpdateSettings)
	}
}
