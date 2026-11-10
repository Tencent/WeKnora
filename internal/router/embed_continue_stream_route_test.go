package router

// The embed plane needs its own stream re-attach route, and where it is
// registered is the whole point of this test.
//
// The tenant-side GET /api/v1/sessions/continue-stream/:session_id sits behind
// the global Auth middleware, so a channel publish/session token cannot use it:
// a channel credential is refused there with
// {"error":"Unauthorized: missing authentication"}. A route for embed must
// therefore be registered inside embedPublicGroup (which runs BEFORE the global
// Auth and carries EmbedAuth + AuditServiceProvider itself). Registering it in
// the wrong group is invisible in a code review — the handler is the same thin
// shell either way, and the failure only shows up as a 401 in the browser.
//
// This also pins that the whole embed registration still succeeds: gin requires
// one wildcard name per position, so a route spelled /sessions/:id/... next to
// the existing /sessions/:session_id/... panics at startup, not at request time.

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestEmbedPublicRoutesRegisterContinueStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	require.NotPanics(t, func() {
		RegisterEmbedPublicRoutes(engine, &handler.EmbedChannelHandler{},
			embedGroupStubEmbedService{}, embedGroupStubTenantService{},
			nil, nil, nil, embedGroupStubAuditService{})
	}, "embed public route registration panicked — check gin wildcard names match per position")

	routes := map[string]bool{}
	for _, r := range engine.Routes() {
		routes[r.Method+" "+r.Path] = true
	}
	require.True(t,
		routes["GET /api/v1/embed/:channel_id/sessions/:session_id/continue-stream"],
		"embed has no stream re-attach route: the embed page cannot follow a server-started "+
			"run (a2ui confirm / ask_user resume) and is reduced to polling the message list, "+
			"which shows nothing until the run finishes")
}
