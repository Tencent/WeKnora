package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
)

// newEmbedLoadMessagesCtx builds a signed embed request for the message history
// endpoint, with query carrying whatever the visitor asked for.
func newEmbedLoadMessagesCtx(
	ch *types.EmbedChannel, query string,
) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodGet, "/messages/"+testEmbedSessionID+"/load"+query, nil)
	c.Request.Header.Set("X-Embed-Session", service.SignEmbedSessionHandle(ch, testEmbedSessionID))
	c.Params = gin.Params{{Key: "session_id", Value: testEmbedSessionID}}
	ctx := context.WithValue(c.Request.Context(), types.EmbedChannelContextKey, ch)
	c.Request = c.Request.WithContext(ctx)
	return c, w
}

func newEmbedHandlerWithMessage(ch *types.EmbedChannel, content string) *EmbedChannelHandler {
	return &EmbedChannelHandler{
		sessionService: &stubSessionServiceForEmbed{
			sessions: map[string]*types.Session{testEmbedSessionID: validEmbedSession(ch)},
		},
		messageHandler: &MessageHandler{
			MessageService: &stubMessageService{
				getRecent: func(context.Context, string, int) ([]*types.Message, error) {
					return []*types.Message{{
						Content: content,
						Images:  types.MessageImages{{URL: testResourceHandle}},
					}}, nil
				},
			},
			FileService: &stubResourceFileService{url: "https://cdn.example.com/signed.png"},
		},
	}
}

// Embed visitors are anonymous: their images must stay behind the
// channel-scoped /embed/:channel_id/files proxy. A visitor asking for public
// URLs is downgraded rather than rejected, so an embed client that forwards the
// parameter keeps working.
func TestEmbedLoadMessages_IgnoresPublicResourceURLRequest(t *testing.T) {
	ch := testEmbedChannel()
	h := newEmbedHandlerWithMessage(ch, "see ![fig]("+testResourceHandle+")")

	c, w := newEmbedLoadMessagesCtx(ch, "?resource_urls=public")
	h.EmbedLoadMessages(c)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), testResourceHandle)
	assert.NotContains(t, w.Body.String(), "cdn.example.com")
}

// The deployment-wide default must not leak public URLs into embed traffic
// either: switching an integration over is a decision about authenticated API
// callers, not about anonymous website visitors.
func TestEmbedLoadMessages_IgnoresDeploymentPublicDefault(t *testing.T) {
	t.Setenv("RESOURCE_URL_MODE", "public")
	ch := testEmbedChannel()
	h := newEmbedHandlerWithMessage(ch, "see ![fig]("+testResourceHandle+")")

	c, w := newEmbedLoadMessagesCtx(ch, "")
	h.EmbedLoadMessages(c)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), testResourceHandle)
	assert.NotContains(t, w.Body.String(), "cdn.example.com")
}
