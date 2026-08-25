package router

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKnowledgeAndMetadataRoutesUseConsistentWildcards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	guards := &rbacGuards{}

	require.NotPanics(t, func() {
		RegisterKnowledgeMetadataRoutes(v1, &handler.MetadataHandler{}, guards)
		RegisterKnowledgeRoutes(v1, &handler.KnowledgeHandler{}, guards)
	})
}
