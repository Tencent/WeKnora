package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanManageChunkFeedback_UsesKBAccessPermission(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	access := &middleware.KBAccess{Permission: types.OrgRoleViewer}
	c.Set(middleware.KBAccessContextKey, access)

	h := &ChunkHandler{}
	assert.False(t, h.canManageChunkFeedback(c))
	assert.False(t, h.canManageChunkFeedbackByChunkID(c))

	access.Permission = types.OrgRoleAdmin
	require.True(t, h.canManageChunkFeedback(c))
	require.True(t, h.canManageChunkFeedbackByChunkID(c))
}
