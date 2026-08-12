package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func allDeploymentFeaturesAvailable() deploymentFeatureAvailability {
	return deploymentFeatureAvailability{
		organizations: true,
		agents:        true,
		im:            true,
		embed:         true,
		api:           true,
		mcp:           true,
		webSearch:     true,
		vectorStore:   true,
		storage:       true,
		sandbox:       true,
	}
}

func TestBuildDeploymentCapabilitiesHidesOrganizationsInLite(t *testing.T) {
	result := buildDeploymentCapabilities("lite", allDeploymentFeaturesAvailable())

	organization := result.Capabilities[capabilityOrganizations]
	if organization.Supported {
		t.Fatal("organizations should be unsupported in lite edition")
	}
	if organization.Reason != "not_supported_in_lite" {
		t.Fatalf("organization reason = %q, want not_supported_in_lite", organization.Reason)
	}
	if !result.Capabilities[capabilityAgents].Supported {
		t.Fatal("agents should remain supported in lite edition")
	}
}

func TestBuildDeploymentCapabilitiesReflectsMissingRoutes(t *testing.T) {
	available := allDeploymentFeaturesAvailable()
	available.embed = false
	available.mcp = false

	result := buildDeploymentCapabilities("standard", available)

	for _, key := range []string{capabilityIntegrationEmbed, capabilitySettingsMCP} {
		capability := result.Capabilities[key]
		if capability.Supported {
			t.Fatalf("%s should be unsupported", key)
		}
		if capability.Reason != "route_not_registered" {
			t.Fatalf("%s reason = %q, want route_not_registered", key, capability.Reason)
		}
	}
	if !result.Capabilities[capabilitySettingsStorage].Supported {
		t.Fatal("an available route should remain supported")
	}
}

func TestDeploymentCapabilitiesHandlerReturnsSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	want := buildDeploymentCapabilities("standard", allDeploymentFeaturesAvailable())
	engine.GET("/capabilities", deploymentCapabilitiesHandler(want))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/capabilities", nil)
	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body struct {
		Code int                            `json:"code"`
		Data deploymentCapabilitiesResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != 0 || body.Data.Edition != "standard" {
		t.Fatalf("response = %#v", body)
	}
	if !body.Data.Capabilities[capabilityIntegrationEmbed].Supported {
		t.Fatal("embed capability should be returned")
	}
}
