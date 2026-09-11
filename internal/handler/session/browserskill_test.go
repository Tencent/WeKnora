package session

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/browserskill"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBrowserAccountStatusDoesNotRequireConversation(t *testing.T) {
	t.Setenv("BROWSERSKILL_BINARY", "/configured/bsk")
	t.Setenv("BROWSERSKILL_PUBLIC_URL", "")
	h := &Handler{browserSkill: browserskill.NewManager()}
	// No session service or conversation ID is supplied: pairing is a personal setting.
	request := httptest.NewRequest("GET", "/api/v1/me/browser", nil)
	ctx := context.WithValue(request.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.UserIDContextKey, "alice")
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = request.WithContext(ctx)
	h.BrowserSkillAccount(c)
	require.Equal(t, 200, response.Code)
	var payload struct {
		Data browserskill.Status `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.True(t, payload.Data.Enabled)
	require.False(t, payload.Data.Connected)
}

func TestBrowserAccountRequiresUser(t *testing.T) {
	h := &Handler{browserSkill: browserskill.NewManager()}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("GET", "/api/v1/me/browser", nil)
	h.BrowserSkillAccount(c)
	require.Equal(t, 401, response.Code)
}
