package router

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGraphSearchRouteCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	r := gin.New()
	RegisterGraphSearchRoutes(r.Group("/api/v1"), &handler.GraphSearchHandler{}, g)
	g.assertAPIKeyPoliciesMatchRoutes(r)
	policy := mustLookupAPIKeyPolicy(t, g, http.MethodPost, "/api/v1/knowledge-bases/:id/graph-search")
	require.True(t, policy.RequireFullAccess)
	require.True(t, policyHasCapability(policy, types.APIKeyCapabilityRetrieve))
	require.False(t, policyHasCapability(policy, types.APIKeyCapabilityChat))
	require.False(t, policyHasCapability(policy, types.APIKeyCapabilityIngest))
	require.False(t, policyHasCapability(policy, types.APIKeyCapabilityManageKnowledgeBases))
	require.Len(t, r.Routes(), 1)
	require.Equal(t, http.MethodPost, r.Routes()[0].Method)
}
