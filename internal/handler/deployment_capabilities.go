package handler

import (
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/gin-gonic/gin"
)

// DeploymentCapabilityKeys is the canonical capability key list shared with
// frontend/src/config/deploymentCapabilities.ts — keep both in sync.
var DeploymentCapabilityKeys = []string{
	"organizations",
	"agents",
	"integrations.im",
	"integrations.embed",
	"integrations.api",
	"settings.mcp",
	"settings.websearch",
	"settings.vectorstore",
	"settings.storage",
	"settings.sandbox",
	"settings.sandbox.docker",
}

// DeploymentCapability describes whether a deployment exposes a feature route.
type DeploymentCapability struct {
	Supported bool   `json:"supported"`
	Reason    string `json:"reason,omitempty"`
}

// DeploymentCapabilitiesData is returned by GET /system/capabilities.
type DeploymentCapabilitiesData struct {
	Edition      string                          `json:"edition"`
	Capabilities map[string]DeploymentCapability `json:"capabilities"`
}

// DeploymentFeatureAvailability mirrors injected backend handlers/services.
type DeploymentFeatureAvailability struct {
	Organizations bool
	Agents        bool
	IM            bool
	Embed         bool
	API           bool
	MCP           bool
	WebSearch     bool
	VectorStore   bool
	Storage       bool
	Sandbox       bool
	SandboxDocker bool
}

func supportedDeploymentCapability(supported bool) DeploymentCapability {
	if supported {
		return DeploymentCapability{Supported: true}
	}
	return DeploymentCapability{Supported: false, Reason: "route_not_registered"}
}

// BuildDeploymentCapabilities derives the deployment capability snapshot.
func BuildDeploymentCapabilities(
	edition string,
	available DeploymentFeatureAvailability,
) DeploymentCapabilitiesData {
	isLite := strings.EqualFold(strings.TrimSpace(edition), "lite")
	organizations := supportedDeploymentCapability(available.Organizations && !isLite)
	if isLite {
		organizations.Reason = "not_supported_in_lite"
	}

	sandboxDocker := DeploymentCapability{
		Supported: available.Sandbox && available.SandboxDocker,
	}
	if available.Sandbox && !available.SandboxDocker {
		sandboxDocker.Reason = "docker_backend_disabled"
	} else if !available.Sandbox {
		sandboxDocker.Reason = "route_not_registered"
	}

	return DeploymentCapabilitiesData{
		Edition: edition,
		Capabilities: map[string]DeploymentCapability{
			"organizations":           organizations,
			"agents":                  supportedDeploymentCapability(available.Agents),
			"integrations.im":         supportedDeploymentCapability(available.IM),
			"integrations.embed":      supportedDeploymentCapability(available.Embed),
			"integrations.api":        supportedDeploymentCapability(available.API),
			"settings.mcp":            supportedDeploymentCapability(available.MCP),
			"settings.websearch":      supportedDeploymentCapability(available.WebSearch),
			"settings.vectorstore":    supportedDeploymentCapability(available.VectorStore),
			"settings.storage":        supportedDeploymentCapability(available.Storage),
			"settings.sandbox":        supportedDeploymentCapability(available.Sandbox),
			"settings.sandbox.docker": sandboxDocker,
		},
	}
}

// BindDeploymentCapabilities stores the startup snapshot used by GetDeploymentCapabilities.
func (h *SystemHandler) BindDeploymentCapabilities(data DeploymentCapabilitiesData) {
	h.deploymentCapabilities = data
}

// GetDeploymentCapabilities godoc
// @Summary      获取部署能力清单
// @Description  返回当前部署版本及实际注册的后端路由所对应的功能能力；仅 supported=false 表示入口应隐藏
// @Tags         系统
// @Produce      json
// @Success      200  {object}  map[string]interface{}  "标准 code/msg/data 包装，data 为 DeploymentCapabilitiesData"
// @Router       /system/capabilities [get]
func (h *SystemHandler) GetDeploymentCapabilities(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "success",
		"data": overlayLiveDockerSandboxCapability(h.deploymentCapabilities),
	})
}

// overlayLiveDockerSandboxCapability replaces the startup snapshot's Docker
// flag with the live 3-tier value so a System Settings toggle is visible
// without restarting the process.
func overlayLiveDockerSandboxCapability(data DeploymentCapabilitiesData) DeploymentCapabilitiesData {
	caps := make(map[string]DeploymentCapability, len(data.Capabilities))
	for key, capability := range data.Capabilities {
		caps[key] = capability
	}
	sandboxCap := caps["settings.sandbox"]
	docker := DeploymentCapability{
		Supported: sandboxCap.Supported && sandbox.DockerBackendEnabled(),
	}
	if sandboxCap.Supported && !docker.Supported {
		docker.Reason = "docker_backend_disabled"
	} else if !sandboxCap.Supported {
		docker.Reason = "route_not_registered"
	}
	caps["settings.sandbox.docker"] = docker
	data.Capabilities = caps
	return data
}
