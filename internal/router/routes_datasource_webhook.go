package router

import (
	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/handler"
)

// RegisterDataSourceWebhookRoutes registers the public git push webhook
// endpoint (GitLab and GitHub). It must be mounted BEFORE the auth
// middleware: webhook senders cannot attach WeKnora credentials, so the
// handler authenticates with each platform's own token/signature scheme
// (X-Gitlab-Token / X-Hub-Signature-256) instead.
//
// Route shape mirrors the IM callback pattern (RegisterIMRoutes): a public
// group under the datasource namespace, keyed by the unguessable data source
// UUID plus a shared secret.
func RegisterDataSourceWebhookRoutes(r *gin.Engine, webhookHandler *handler.DataSourceWebhookHandler) {
	g := r.Group("/api/v1/datasource/webhooks")
	{
		g.POST("/git/:id", webhookHandler.HandleGitPush)
	}
}
