package router

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

func TestFeedbackRoutesDefaultDenyAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")
	RegisterFeedbackRoutes(v1, &handler.FeedbackHandler{}, g)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v1/sessions/:session_id/messages/:message_id/feedback"},
		{http.MethodGet, "/api/v1/chunks/by-id/:id/feedback"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/chunks/:chunk_id/feedback/reset"},
	} {
		if _, ok := g.ensureAPIKeyAuthorizer().Lookup(route.method, route.path); ok {
			t.Fatalf("%s %s must remain undeclared so the API-key gate denies it", route.method, route.path)
		}
	}
}
