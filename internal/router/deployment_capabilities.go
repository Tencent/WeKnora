package router

import "github.com/Tencent/WeKnora/internal/handler"

func deploymentCapabilitiesFromRouter(params RouterParams) handler.DeploymentCapabilitiesData {
	return handler.BuildDeploymentCapabilities(handler.Edition, handler.DeploymentFeatureAvailability{
		Organizations: params.OrganizationHandler != nil,
		Agents:        params.CustomAgentHandler != nil,
		IM:            params.IMHandler != nil,
		// Match RegisterEmbedChannelRoutes: management routes depend on handler only.
		Embed:       params.EmbedChannelHandler != nil,
		API:         params.TenantHandler != nil && params.TenantAPIKeyService != nil,
		MCP:         params.MCPServiceHandler != nil && params.MCPCredentialsHandler != nil && params.MCPOAuthHandler != nil,
		WebSearch:   params.WebSearchHandler != nil && params.WebSearchProviderHandler != nil && params.WebSearchCredentialsHandler != nil,
		VectorStore: params.VectorStoreHandler != nil,
		Storage:     params.StorageBackendHandler != nil,
		Sandbox:     params.SandboxConfigHandler != nil,
	})
}
