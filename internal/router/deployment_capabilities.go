package router

import (
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/handler"
	"github.com/gin-gonic/gin"
)

const (
	capabilityOrganizations       = "organizations"
	capabilityAgents              = "agents"
	capabilityIntegrationIM       = "integrations.im"
	capabilityIntegrationEmbed    = "integrations.embed"
	capabilityIntegrationAPI      = "integrations.api"
	capabilitySettingsMCP         = "settings.mcp"
	capabilitySettingsWebSearch   = "settings.websearch"
	capabilitySettingsVectorStore = "settings.vectorstore"
	capabilitySettingsStorage     = "settings.storage"
	capabilitySettingsSandbox     = "settings.sandbox"
)

type deploymentCapability struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

type deploymentCapabilitiesResponse struct {
	Edition      string                          `json:"edition"`
	Capabilities map[string]deploymentCapability `json:"capabilities"`
}

type deploymentFeatureAvailability struct {
	organizations bool
	agents        bool
	im            bool
	embed         bool
	api           bool
	mcp           bool
	webSearch     bool
	vectorStore   bool
	storage       bool
	sandbox       bool
}

func supportedCapability(supported bool) deploymentCapability {
	if supported {
		return deploymentCapability{Supported: true}
	}
	return deploymentCapability{Supported: false, Reason: "route_not_registered"}
}

func buildDeploymentCapabilities(
	edition string,
	available deploymentFeatureAvailability,
) deploymentCapabilitiesResponse {
	isLite := strings.EqualFold(strings.TrimSpace(edition), "lite")
	organizations := supportedCapability(available.organizations && !isLite)
	if isLite {
		organizations.Reason = "not_supported_in_lite"
	}

	return deploymentCapabilitiesResponse{
		Edition: edition,
		Capabilities: map[string]deploymentCapability{
			capabilityOrganizations:       organizations,
			capabilityAgents:              supportedCapability(available.agents),
			capabilityIntegrationIM:       supportedCapability(available.im),
			capabilityIntegrationEmbed:    supportedCapability(available.embed),
			capabilityIntegrationAPI:      supportedCapability(available.api),
			capabilitySettingsMCP:         supportedCapability(available.mcp),
			capabilitySettingsWebSearch:   supportedCapability(available.webSearch),
			capabilitySettingsVectorStore: supportedCapability(available.vectorStore),
			capabilitySettingsStorage:     supportedCapability(available.storage),
			capabilitySettingsSandbox:     supportedCapability(available.sandbox),
		},
	}
}

func deploymentCapabilitiesFromRouter(params RouterParams) deploymentCapabilitiesResponse {
	return buildDeploymentCapabilities(handler.Edition, deploymentFeatureAvailability{
		organizations: params.OrganizationHandler != nil,
		agents:        params.CustomAgentHandler != nil,
		im:            params.IMHandler != nil,
		embed:         params.EmbedChannelHandler != nil && params.EmbedChannelService != nil,
		api:           params.TenantHandler != nil && params.TenantAPIKeyService != nil,
		mcp:           params.MCPServiceHandler != nil && params.MCPCredentialsHandler != nil && params.MCPOAuthHandler != nil,
		webSearch:     params.WebSearchHandler != nil && params.WebSearchProviderHandler != nil && params.WebSearchCredentialsHandler != nil,
		vectorStore:   params.VectorStoreHandler != nil,
		storage:       params.StorageBackendHandler != nil,
		sandbox:       params.SandboxConfigHandler != nil,
	})
}

func deploymentCapabilitiesHandler(capabilities deploymentCapabilitiesResponse) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"msg":  "success",
			"data": capabilities,
		})
	}
}
