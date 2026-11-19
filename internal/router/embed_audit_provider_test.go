package router

// Guards the wiring that the embed plane's A2UI confirm-once depends on.
//
// emitAgentCardAudit reads the audit service out of the gin context
// (middleware.AuditServiceFromContext) and returns silently when it is not
// there. That provider is installed by r.Use in NewRouter — but the embed
// public routes are registered BEFORE it, because they must also precede the
// global Auth middleware (they authenticate with the channel publish token).
// Gin snapshots the handler chain when a route is registered, so those routes
// never saw the provider: every embed card-action audit row was dropped at the
// nil check, and the confirm-once dedup (CardActionAlreadyExecuted) therefore
// never matched on the embed plane.
//
// The provider must therefore be installed on the embed group itself. This test
// pins that, because the failure is invisible at runtime by design (audit is
// best-effort) and the fix is one middleware in a group. It runs each group
// middleware against a throwaway gin.Context and asks the public accessor
// whether the service landed — func-pointer identity is not usable here, the
// provider is small enough that one call site can inline it and another not.

import (
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Stubs embed the interface so only the methods the group constructor touches
// would need real bodies; the constructor must not call any of them.
type (
	embedGroupStubEmbedService  struct{ interfaces.EmbedChannelService }
	embedGroupStubTenantService struct{ interfaces.TenantService }
	embedGroupStubAuditService  struct{ interfaces.AuditLogService }
)

// hasAuditProvider reports whether any middleware in the chain injects the
// audit service. A middleware that panics on a bare context (EmbedAuth wants a
// live service and a real request) is skipped: the question is only "does one
// of these handlers stash the service", not "does the chain authenticate".
func hasAuditProvider(handlers gin.HandlersChain) bool {
	for _, h := range handlers {
		injects := func() (ok bool) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			defer func() {
				_ = recover()
				ok = middleware.AuditServiceFromContext(c) != nil
			}()
			h(c)
			return
		}()
		if injects {
			return true
		}
	}
	return false
}

func TestEmbedPublicGroupCarriesAuditServiceProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	grp := embedPublicGroup(r, embedGroupStubEmbedService{}, embedGroupStubTenantService{},
		nil, embedGroupStubAuditService{})
	require.Equal(t, "/api/v1/embed/:channel_id", grp.BasePath(),
		"the audited group must be the embed public group itself")
	require.True(t, hasAuditProvider(grp.Handlers),
		"embed public group carries no AuditServiceProvider: AuditServiceFromContext is nil "+
			"in every embed handler, so card-action audit rows (and the confirm-once dedup that "+
			"reads them) are silently lost")
}

// TestHasAuditProviderDetectsAbsence keeps the probe above from passing on an
// empty chain: a guard that cannot say "no" proves nothing.
func TestHasAuditProviderDetectsAbsence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	bare := r.Group("/api/v1/embed/:channel_id")
	require.False(t, hasAuditProvider(bare.Handlers), "the probe must report absence")

	withProvider := r.Group("/another", middleware.AuditServiceProvider(embedGroupStubAuditService{}))
	require.True(t, hasAuditProvider(withProvider.Handlers), "the probe must report the provider")
}
