package router

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/handler"
	sessionhandler "github.com/Tencent/WeKnora/internal/handler/session"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestConversationRoutesDeclareChatCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterSessionRoutes(v1, &sessionhandler.Handler{}, &handler.MessageSuggestionHandler{}, g)
	RegisterChatRoutes(v1, &sessionhandler.Handler{}, g)
	RegisterMessageRoutes(v1, &handler.MessageHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/sessions"},
		{http.MethodGet, "/api/v1/sessions/:id/messages/:message_id/suggestions"},
		{http.MethodPost, "/api/v1/sessions/:session_id/messages/:message_id/suggestions"},
		{http.MethodPost, "/api/v1/sessions/:session_id/suggestion-events"},
		{http.MethodPost, "/api/v1/knowledge-chat/:session_id"},
		{http.MethodPost, "/api/v1/agent-chat/:session_id"},
		{http.MethodGet, "/api/v1/messages/:session_id/load"},
		{http.MethodDelete, "/api/v1/messages/:session_id/:id"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityChat) {
				t.Fatalf("policy capabilities = %#v, want chat", policy.Capabilities)
			}
		})
	}
}

func TestMessageHistoryRoutesDeclareMessageHistoryCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterMessageRoutes(v1, &handler.MessageHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/messages/search"},
		{http.MethodGet, "/api/v1/messages/chat-history-stats"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityMessageHistory) {
				t.Fatalf("policy capabilities = %#v, want message_history", policy.Capabilities)
			}
			if policyHasCapability(policy, types.APIKeyCapabilityChat) {
				t.Fatalf("message-history route must not be granted by chat: %#v", policy.Capabilities)
			}
		})
	}
}

func TestAgentReadRoutesDeclareReadAgentsCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterCustomAgentRoutes(v1, &handler.CustomAgentHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/agents/placeholders"},
		{http.MethodGet, "/api/v1/agents/type-presets"},
		{http.MethodGet, "/api/v1/agents"},
		{http.MethodGet, "/api/v1/agents/:id"},
		{http.MethodGet, "/api/v1/agents/:id/suggested-questions"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityReadAgents) {
				t.Fatalf("policy capabilities = %#v, want read_agents", policy.Capabilities)
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityChat) {
				t.Fatalf("policy capabilities = %#v, want chat for conversation clients", policy.Capabilities)
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityManageAgents) {
				t.Fatalf("policy capabilities = %#v, want manage_agents for authoring clients", policy.Capabilities)
			}
		})
	}
}

func TestAgentWriteRoutesRequireManageAgentsCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterCustomAgentRoutes(v1, &handler.CustomAgentHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/agents"},
		{http.MethodPut, "/api/v1/agents/:id"},
		{http.MethodDelete, "/api/v1/agents/:id"},
		{http.MethodPost, "/api/v1/agents/:id/copy"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityManageAgents) {
				t.Fatalf("policy capabilities = %#v, want manage_agents", policy.Capabilities)
			}
			if policyHasCapability(policy, types.APIKeyCapabilityReadAgents) {
				t.Fatalf("agent write route must not be granted by read_agents: %#v", policy.Capabilities)
			}
		})
	}
}

func TestKnowledgeBaseManagementRoutesDeclareManageKBsCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterKnowledgeBaseRoutes(v1, &handler.KnowledgeBaseHandler{}, g)
	RegisterInitializationRoutes(v1, &handler.InitializationHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPut, "/api/v1/knowledge-bases/:id"},
		{http.MethodDelete, "/api/v1/knowledge-bases/:id"},
		{http.MethodPost, "/api/v1/initialization/initialize/:kbId"},
		{http.MethodPut, "/api/v1/initialization/config/:kbId"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityManageKnowledgeBases) {
				t.Fatalf("policy capabilities = %#v, want manage_kbs", policy.Capabilities)
			}
			if policyHasCapability(policy, types.APIKeyCapabilityIngest) {
				t.Fatalf("KB management route must not be granted by ingest: %#v", policy.Capabilities)
			}
		})
	}
}

func TestKnowledgeBaseCreateRouteRequiresFullAccessForAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterKnowledgeBaseRoutes(v1, &handler.KnowledgeBaseHandler{}, g)

	// Creating a KB is open to full-access keys (tenant-wide authority),
	// matching KB update/delete, but carries no capability so scoped keys
	// stay denied.
	policy := mustLookupAPIKeyPolicy(t, g, http.MethodPost, "/api/v1/knowledge-bases")
	if !policy.RequireFullAccess {
		t.Fatal("KB create should require full access for API keys")
	}
	if len(policy.Capabilities) != 0 {
		t.Fatalf("KB create must not be granted by any capability: %#v", policy.Capabilities)
	}
}

func TestKnowledgeBaseCopyRoutesRemainDefaultDenyForAPIKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterKnowledgeBaseRoutes(v1, &handler.KnowledgeBaseHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/knowledge-bases/copy"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/duplicate"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			if _, ok := g.apiKeyAuthorizer.Lookup(tc.method, tc.path); ok {
				t.Fatalf("route should remain default-deny for API keys: %s %s", tc.method, tc.path)
			}
		})
	}
}

func TestKnowledgeReadRoutesDeclareRetrieveCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterKnowledgeBaseRoutes(v1, &handler.KnowledgeBaseHandler{}, g)
	RegisterKnowledgeRoutes(v1, &handler.KnowledgeHandler{}, g)
	RegisterFAQRoutes(v1, &handler.FAQHandler{}, g)
	RegisterKnowledgeTagRoutes(v1, &handler.TagHandler{}, g)
	RegisterChatRoutes(v1, &sessionhandler.Handler{}, g)
	RegisterInitializationRoutes(v1, &handler.InitializationHandler{}, g)
	RegisterWikiPageRoutes(v1, &handler.WikiPageHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/knowledge-bases"},
		{http.MethodGet, "/api/v1/knowledge-bases/:id"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/hybrid-search"},
		{http.MethodGet, "/api/v1/knowledge-bases/:id/knowledge"},
		{http.MethodGet, "/api/v1/knowledge/:id"},
		{http.MethodPost, "/api/v1/knowledge-bases/:id/faq/search"},
		{http.MethodGet, "/api/v1/knowledge-bases/:id/tags"},
		{http.MethodPost, "/api/v1/knowledge-search"},
		{http.MethodGet, "/api/v1/initialization/config/:kbId"},
		{http.MethodGet, "/api/v1/knowledgebase/:kb_id/wiki/pages"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityRetrieve) {
				t.Fatalf("policy capabilities = %#v, want retrieve", policy.Capabilities)
			}
		})
	}
}

func TestTenantInfrastructureRoutesDeclareSpecificCapabilities(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterTenantRoutes(v1, &handler.TenantHandler{}, nil, nil, nil, g)
	RegisterModelRoutes(v1, &handler.ModelHandler{}, &handler.ModelCredentialsHandler{}, g)
	RegisterEvaluationRoutes(v1, &handler.EvaluationHandler{}, g)
	RegisterSystemRoutes(v1, &handler.SystemHandler{}, g)
	RegisterMCPServiceRoutes(v1, &handler.MCPServiceHandler{}, &handler.MCPCredentialsHandler{}, &handler.MCPOAuthHandler{}, g)
	RegisterWebSearchProviderRoutes(v1, &handler.WebSearchProviderHandler{}, &handler.WebSearchProviderCredentialsHandler{}, g)
	RegisterVectorStoreRoutes(v1, &handler.VectorStoreHandler{}, g)
	RegisterEmbedChannelRoutes(v1, &handler.EmbedChannelHandler{}, g)
	RegisterIMChannelRoutes(v1, &handler.IMHandler{}, g)
	RegisterDataSourceRoutes(v1, &handler.DataSourceHandler{}, &handler.DataSourceCredentialsHandler{}, g)
	RegisterWeKnoraCloudRoutes(v1, &handler.WeKnoraCloudHandler{}, g)

	cases := []struct {
		method string
		path   string
		cap    types.APIKeyCapability
	}{
		{http.MethodGet, "/api/v1/tenants", types.APIKeyCapabilityManageTenantSettings},
		{http.MethodGet, "/api/v1/models", types.APIKeyCapabilityManageModels},
		{http.MethodPost, "/api/v1/evaluation", types.APIKeyCapabilityRunEvaluations},
		{http.MethodGet, "/api/v1/system/info", types.APIKeyCapabilityManageVectorStores},
		{http.MethodGet, "/api/v1/mcp-services", types.APIKeyCapabilityManageMCPServices},
		{http.MethodGet, "/api/v1/web-search-providers", types.APIKeyCapabilityManageWebSearch},
		{http.MethodGet, "/api/v1/vector-stores", types.APIKeyCapabilityManageVectorStores},
		{http.MethodGet, "/api/v1/embed-channels", types.APIKeyCapabilityManageChannels},
		{http.MethodGet, "/api/v1/im-channels", types.APIKeyCapabilityManageChannels},
		{http.MethodGet, "/api/v1/datasource", types.APIKeyCapabilityManageDataSources},
		{http.MethodGet, "/api/v1/models/weknoracloud/status", types.APIKeyCapabilityManageModels},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, tc.cap) {
				t.Fatalf("policy capabilities = %#v, want %s", policy.Capabilities, tc.cap)
			}
		})
	}
}

func TestTenantMemberRoutesDeclareManageMembersCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterTenantRoutes(v1, &handler.TenantHandler{}, &handler.TenantMemberHandler{}, &handler.TenantInvitationHandler{}, nil, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/tenants/:id/members"},
		{http.MethodPost, "/api/v1/tenants/:id/members"},
		{http.MethodPut, "/api/v1/tenants/:id/members/:user_id"},
		{http.MethodDelete, "/api/v1/tenants/:id/members/:user_id"},
		{http.MethodGet, "/api/v1/tenants/:id/invitations"},
		{http.MethodPost, "/api/v1/tenants/:id/invitations"},
		{http.MethodDelete, "/api/v1/tenants/:id/invitations/:inv_id"},
		{http.MethodPost, "/api/v1/tenants/:id/invite-links"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityManageMembers) {
				t.Fatalf("policy capabilities = %#v, want manage_members", policy.Capabilities)
			}
		})
	}

	if _, ok := g.apiKeyAuthorizer.Lookup(http.MethodPost, "/api/v1/tenants/:id/leave"); ok {
		t.Fatal("tenant leave route should remain default-deny for API keys")
	}
}

func TestOrganizationRoutesDeclareManageSpacesCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterOrganizationRoutes(v1, &handler.OrganizationHandler{}, g)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/organizations"},
		{http.MethodGet, "/api/v1/organizations"},
		{http.MethodPost, "/api/v1/organizations/join"},
		{http.MethodGet, "/api/v1/organizations/search"},
		{http.MethodPut, "/api/v1/organizations/:id"},
		{http.MethodPost, "/api/v1/organizations/:id/invite-code"},
		{http.MethodGet, "/api/v1/organizations/:id/members"},
		{http.MethodPut, "/api/v1/organizations/:id/members/:tenant_id"},
		{http.MethodGet, "/api/v1/shared-knowledge-bases"},
		{http.MethodGet, "/api/v1/shared-agents"},
		{http.MethodPost, "/api/v1/shared-agents/disabled"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("policy should require full access without a matching capability")
			}
			if !policyHasCapability(policy, types.APIKeyCapabilityManageSpaces) {
				t.Fatalf("policy capabilities = %#v, want manage_spaces", policy.Capabilities)
			}
		})
	}

	// KB/agent share management is open to full-access keys (tenant-wide
	// authority) but never via a capability.
	shareRoutes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/knowledge-bases/:id/shares"},
		{http.MethodGet, "/api/v1/knowledge-bases/:id/shares"},
		{http.MethodPut, "/api/v1/knowledge-bases/:id/shares/:share_id"},
		{http.MethodDelete, "/api/v1/knowledge-bases/:id/shares/:share_id"},
		{http.MethodPost, "/api/v1/agents/:id/shares"},
		{http.MethodGet, "/api/v1/agents/:id/shares"},
		{http.MethodDelete, "/api/v1/agents/:id/shares/:share_id"},
	}
	for _, tc := range shareRoutes {
		t.Run("share "+tc.method+" "+tc.path, func(t *testing.T) {
			policy := mustLookupAPIKeyPolicy(t, g, tc.method, tc.path)
			if !policy.RequireFullAccess {
				t.Fatal("share route should require full access for API keys")
			}
			if len(policy.Capabilities) != 0 {
				t.Fatalf("share route must not be granted by any capability: %#v", policy.Capabilities)
			}
		})
	}
}

func TestChunkerPreviewRouteRequiresRetrieveOrIngestCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterChunkerDebugRoutes(v1, g)

	policy := mustLookupAPIKeyPolicy(t, g, http.MethodPost, "/api/v1/chunker/preview")
	if !policy.RequireFullAccess {
		t.Fatal("policy should require full access without a matching capability")
	}
	if !policyHasCapability(policy, types.APIKeyCapabilityRetrieve) {
		t.Fatalf("policy capabilities = %#v, want retrieve", policy.Capabilities)
	}
	if !policyHasCapability(policy, types.APIKeyCapabilityIngest) {
		t.Fatalf("policy capabilities = %#v, want ingest", policy.Capabilities)
	}
}

func TestFAQImportProgressRouteRequiresRetrieveOrIngestCapability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	g := &rbacGuards{}
	v1 := gin.New().Group("/api/v1")

	RegisterFAQRoutes(v1, &handler.FAQHandler{}, g)

	policy := mustLookupAPIKeyPolicy(t, g, http.MethodGet, "/api/v1/faq/import/progress/:task_id")
	if !policy.RequireFullAccess {
		t.Fatal("policy should require full access without a matching capability")
	}
	if !policyHasCapability(policy, types.APIKeyCapabilityRetrieve) {
		t.Fatalf("policy capabilities = %#v, want retrieve", policy.Capabilities)
	}
	if !policyHasCapability(policy, types.APIKeyCapabilityIngest) {
		t.Fatalf("policy capabilities = %#v, want ingest", policy.Capabilities)
	}
}

func mustLookupAPIKeyPolicy(
	t *testing.T,
	g *rbacGuards,
	method string,
	path string,
) middleware.APIKeyRoutePolicy {
	t.Helper()
	policy, ok := g.apiKeyAuthorizer.Lookup(method, path)
	if !ok {
		t.Fatalf("missing API-key policy for %s %s", method, path)
	}
	return policy
}

func policyHasCapability(policy middleware.APIKeyRoutePolicy, cap types.APIKeyCapability) bool {
	for _, got := range policy.Capabilities {
		if got == cap {
			return true
		}
	}
	return false
}
