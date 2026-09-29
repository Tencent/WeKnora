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

func TestPairingOriginUsesLoopbackListenerForWailsPage(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:53124/api/v1/me/browser", nil)
	req.Host = "127.0.0.1:53124"
	require.Equal(t, "http://127.0.0.1:53124", pairingOrigin(req, "wails://wails.localhost"))

	req.Host = "[::1]:8080"
	require.Equal(t, "http://[::1]:8080", pairingOrigin(req, "wails://wails.localhost"))

	req.Host = "weknora.example"
	require.Equal(t, "wails://wails.localhost", pairingOrigin(req, "wails://wails.localhost"))
	require.Equal(t, "https://weknora.example", pairingOrigin(req, "https://weknora.example"))
	require.Equal(t, "https://weknora.example:8443", pairingOrigin(req, "https://weknora.example:8443"))
}

func TestPairingOriginIgnoresForeignHostOnLoopback(t *testing.T) {
	req := httptest.NewRequest("POST", "http://127.0.0.1:53124/api/v1/me/browser", nil)
	req.Host = "127.0.0.1:53124"
	require.Equal(t, "http://127.0.0.1:53124", pairingOrigin(req, "https://evil.example"))
	require.Equal(t, "http://127.0.0.1:53124", pairingOrigin(req, "http://127.0.0.1:9"))

	req.Host = "weknora.example"
	require.NotEqual(t, "https://evil.example", pairingOrigin(req, "https://evil.example"))
	require.Equal(t, "http://weknora.example", pairingOrigin(req, "https://evil.example"))
}

func TestBrowserAccountRequiresUser(t *testing.T) {
	h := &Handler{browserSkill: browserskill.NewManager()}
	response := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(response)
	c.Request = httptest.NewRequest("GET", "/api/v1/me/browser", nil)
	h.BrowserSkillAccount(c)
	require.Equal(t, 401, response.Code)
}
