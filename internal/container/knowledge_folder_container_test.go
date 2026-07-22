package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/dig"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeFolderContainerGraphResolvesServiceHandlerAndRouter(t *testing.T) {
	c := dig.New()
	require.NoError(t, c.Provide(func() (*gorm.DB, error) {
		return gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	}))
	require.NoError(t, c.Provide(repository.NewKnowledgeBaseRepository))
	require.NoError(t, c.Provide(repository.NewKnowledgeRepository))
	require.NoError(t, c.Provide(repository.NewKnowledgeFolderRepository))
	require.NoError(t, c.Provide(service.NewKnowledgeFolderService))
	require.NoError(t, c.Provide(handler.NewKnowledgeFolderHandler))
	require.NoError(t, c.Provide(func(h *handler.KnowledgeFolderHandler) *gin.Engine {
		r := gin.New()
		r.GET("/knowledge-bases/:id/folders/tree", h.GetTree)
		return r
	}))

	require.NoError(t, c.Invoke(func(
		interfaces.KnowledgeFolderService,
		*handler.KnowledgeFolderHandler,
		*gin.Engine,
	) {
	}))
}
