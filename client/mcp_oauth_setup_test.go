package client

import (
	"encoding/json"
	"testing"
)

func TestMCPAuthorizationSetup(t *testing.T) {
	var probe MCPTestResult
	if err := json.Unmarshal([]byte(`{"success":false,"oauth_required":true}`), &probe); err != nil {
		t.Fatal(err)
	}
	if !probe.OAuthRequired {
		t.Fatal("missing authorization challenge")
	}
	body, err := json.Marshal(MCPService{AuthConfig: &MCPAuthConfig{AuthType: "oauth", ClientID: "example-client", Scopes: []string{"files.read"}}})
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Auth struct {
			Type   string `json:"auth_type"`
			Client string `json:"client_id"`
			Scopes []string
		} `json:"auth_config"`
	}
	if err = json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Auth.Type != "oauth" || req.Auth.Client != "example-client" || len(req.Auth.Scopes) != 1 {
		t.Fatalf("unexpected OAuth configuration: %s", body)
	}
}
