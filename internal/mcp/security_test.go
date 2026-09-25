package mcp

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestValidateServiceOutboundURLsRejectsOAuthMetadataSSRF(t *testing.T) {
	serviceURL := "https://example.com/mcp"
	err := ValidateServiceOutboundURLs(&types.MCPService{
		URL: &serviceURL,
		AuthConfig: &types.MCPAuthConfig{
			AuthServerMetadataURL: "http://169.254.169.254/latest/meta-data",
		},
	})
	if err == nil {
		t.Fatal("expected OAuth metadata URL to be rejected")
	}
}

func TestNewMCPClientRejectsStoredUnsafeURL(t *testing.T) {
	serviceURL := "http://127.0.0.1:8080/mcp"
	_, err := NewMCPClient(&ClientConfig{Service: &types.MCPService{
		URL:           &serviceURL,
		TransportType: types.MCPTransportHTTPStreamable,
	}})
	if err == nil {
		t.Fatal("expected stored unsafe MCP URL to be rejected at client construction")
	}
}
