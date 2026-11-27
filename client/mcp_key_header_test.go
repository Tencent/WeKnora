package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCreateMCPServicePreservesAPIKeyHeader(t *testing.T) {
	const config = `{"auth_type":"api_key","api_key_header":"X-Provider-Key"}`
	var auth MCPAuthConfig
	if err := json.Unmarshal([]byte(config), &auth); err != nil {
		t.Fatal(err)
	}
	transport := tagTransport(func(req *http.Request) (*http.Response, error) {
		var received struct {
			AuthConfig struct {
				APIKeyHeader string `json:"api_key_header"`
			} `json:"auth_config"`
		}
		if err := json.NewDecoder(req.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		if received.AuthConfig.APIKeyHeader != "X-Provider-Key" {
			t.Errorf("api_key_header = %q, want X-Provider-Key", received.AuthConfig.APIKeyHeader)
		}
		wire := `{"success":true,"data":{"id":"mcp-1","auth_config":` + config + `}}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(wire)),
			Header:     make(http.Header),
		}, nil
	})
	service, err := NewClient("https://example.test", WithTransport(transport)).CreateMCPService(
		context.Background(), &MCPService{Name: "Provider", AuthConfig: &auth},
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(service.AuthConfig)
	if err != nil {
		t.Fatal(err)
	}
	var returned map[string]string
	if err := json.Unmarshal(body, &returned); err != nil {
		t.Fatal(err)
	}
	if returned["api_key_header"] != "X-Provider-Key" {
		t.Errorf("response lost api_key_header: %s", body)
	}
}
