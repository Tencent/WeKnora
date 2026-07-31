package container

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type knowledgeFolderMoveWiringFolderService struct {
	interfaces.KnowledgeFolderService
}

type knowledgeFolderMoveWiringService struct {
	calls int
}

func (s *knowledgeFolderMoveWiringService) MoveKnowledge(
	_ context.Context,
	_ *types.KnowledgeFolderMoveInput,
) (*types.KnowledgeFolderMoveResult, error) {
	s.calls++
	return &types.KnowledgeFolderMoveResult{ChangedCount: 1}, nil
}

func TestProvideKnowledgeFolderHandlerWiresRequiredMoveService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	moveService := &knowledgeFolderMoveWiringService{}
	folderHandler := provideKnowledgeFolderHandler(
		&knowledgeFolderMoveWiringFolderService{},
		moveService,
	)
	engine := gin.New()
	engine.Use(middleware.ErrorHandler())
	engine.Use(func(c *gin.Context) {
		ctx := context.WithValue(
			c.Request.Context(),
			types.TenantIDContextKey,
			uint64(7),
		)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	engine.POST(
		"/knowledge-bases/:id/folders/move-knowledge",
		folderHandler.MoveKnowledge,
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/knowledge-bases/kb-1/folders/move-knowledge",
		strings.NewReader(
			`{"knowledge_ids":["11111111-1111-4111-8111-111111111111"],`+
				`"target_folder_id":""}`,
		),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, 1, moveService.calls)
}
