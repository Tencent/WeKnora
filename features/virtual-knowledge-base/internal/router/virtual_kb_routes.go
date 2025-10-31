package router

import (
	"github.com/gin-gonic/gin"
	handler "github.com/tencent/weknora/features/virtualkb/internal/handler"
	"github.com/tencent/weknora/features/virtualkb/internal/middleware"
	service "github.com/tencent/weknora/features/virtualkb/internal/service/interfaces"
)

// RegisterVirtualKBRoutes wires all virtual knowledge base feature routes.
func RegisterVirtualKBRoutes(r *gin.Engine, deps Dependencies) {
	group := r.Group("/api/v1/virtual-kb")
	group.Use(middleware.APIKeyAuth(deps.APIKey))

	tagHandler := handler.NewTagHandler(deps.TagService)
	documentTagHandler := handler.NewDocumentTagHandler(deps.DocumentTagService)
	virtualKBHandler := handler.NewVirtualKBHandler(deps.VirtualKBService)
	enhancedSearchHandler := handler.NewEnhancedSearchHandler(deps.EnhancedSearchService)

	tagHandler.RegisterRoutes(group)
	documentTagHandler.RegisterRoutes(group)
	virtualKBHandler.RegisterRoutes(group.Group("/instances"))
	enhancedSearchHandler.RegisterRoutes(group.Group("/search"))
}

// Dependencies collects runtime dependencies required by the router.
type Dependencies struct {
	APIKey                string
	TagService            service.TagService
	DocumentTagService    service.DocumentTagService
	VirtualKBService      service.VirtualKBService
	EnhancedSearchService service.EnhancedSearchService
}
